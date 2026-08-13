package mtproxylctl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Бот-сторож управляется напрямую, а не подкомандой MTProxyL, — и это
// принципиально. Сторож ставится и поверх официального MTProxyL, где никакой
// подкоманды `alertbot` нет и не будет: она живёт только в форке. Ходить туда
// значило бы, что раздел панели работает у одних и молчит у других.
//
// Поэтому панель разговаривает с самим ботом: его бинарник умеет `config
// show|set`, состояние службы спрашивается у systemd, а установка и удаление —
// это тот же install-alertbot.sh, которым бот ставят из терминала.

const (
	alertbotDir     = "/opt/mtproxyl-alertbot"
	alertbotBin     = alertbotDir + "/mtproxyl-alertbot"
	alertbotService = "mtproxyl-alertbot.service"
	alertbotTimeout = 5 * time.Minute
	// alertbotInstaller — тот же скрипт, которым бота ставят из терминала.
	// Кладётся рядом с ботом при первой установке, чтобы панель не качала его
	// из сети на каждое нажатие.
	alertbotInstaller = alertbotDir + "/install-alertbot.sh"
)

// ErrAlertbotUnsupported — панель не может управлять сторожем: не хватает прав
// sudo. Так бывает, если панель обновили, а права остались от прежней версии.
var ErrAlertbotUnsupported = errors.New("панели не хватает прав для управления ботом-сторожем — переустановите её: mtproxyl panel install")

// AlertbotConfig — настройки сторожа без токена: наружу токен не отдаётся
// никогда, только признак, что он задан.
type AlertbotConfig struct {
	ChatID               int64   `json:"chat_id"`
	AlertThreshold       float64 `json:"alert_threshold"`
	ConnectFailThreshold float64 `json:"connect_fail_threshold"`
	Timezone             string  `json:"timezone"`
	HasToken             bool    `json:"has_token"`
}

// AlertbotStatus — то же, что панель показывает про бота-администратора, но
// собранное из systemd и файла настроек.
type AlertbotStatus struct {
	Installed  bool           `json:"installed"`
	Configured bool           `json:"configured"`
	Active     bool           `json:"active"`
	Enabled    bool           `json:"enabled"`
	Dir        string         `json:"dir"`
	Service    string         `json:"service"`
	Config     AlertbotConfig `json:"config"`
}

// AlertbotChatHint объясняет, что вписать в поле адреса.
func AlertbotChatHint() string {
	return "Свой ID покажет @userinfobot. Можно указать группу или канал — " +
		"тогда бот пишет туда; команд и клавиатуры там нет, а чтобы он мог " +
		"убирать свои прежние сообщения, ему нужны права администратора."
}

// ValidateChatID: отрицательные id — это группы и каналы, и они законны.
func ValidateChatID(id int64) error {
	if id == 0 {
		return fmt.Errorf("нужен ID чата")
	}
	return nil
}

func (c *Client) AlertbotStatus(ctx context.Context) (*AlertbotStatus, error) {
	st := &AlertbotStatus{Dir: alertbotDir, Service: alertbotService}

	st.Active = systemctlQuiet(ctx, "is-active", alertbotService)
	st.Enabled = systemctlQuiet(ctx, "is-enabled", alertbotService)

	// «Установлен» выясняется разговором с самим ботом, а не os.Stat по его
	// файлу: каталог сторожа принадлежит его собственному пользователю и
	// закрыт правами 750, поэтому панель туда даже заглянуть не может — и
	// честно установленный бот выглядел бы отсутствующим.
	out, err := c.runAlertbot(ctx, "config", "show", "--json")
	if err != nil {
		// Ответ службы важнее: если она работает, бот установлен, а спросить
		// настройки помешало что-то другое — права sudo, например.
		st.Installed = st.Active || st.Enabled
		return st, nil
	}
	st.Installed = true

	var cfg AlertbotConfig
	if err := json.Unmarshal([]byte(firstJSONLine(out)), &cfg); err == nil {
		st.Config = cfg
		st.Configured = cfg.HasToken
	}
	return st, nil
}

func (c *Client) AlertbotLogs(ctx context.Context, lines int) (string, error) {
	if lines <= 0 {
		lines = 100
	}
	if lines > 500 {
		lines = 500
	}
	out, err := c.sudo(ctx, 30*time.Second, "journalctl", "-u", alertbotService,
		"-n", strconv.Itoa(lines), "--no-pager")
	if err != nil {
		return "", err
	}
	return out, nil
}

// AlertbotInstall ставит или переустанавливает сторожа тем же скриптом, что и
// установка из терминала: два разных пути установки разошлись бы в первый же
// месяц.
func (c *Client) AlertbotInstall(ctx context.Context, token string, chat int64) (string, error) {
	args := []string{alertbotInstaller, "--token", token, "--chat", strconv.FormatInt(chat, 10)}
	return c.sudo(ctx, alertbotTimeout, "sh", args...)
}

func (c *Client) AlertbotService(ctx context.Context, action string) (string, error) {
	switch action {
	case "start", "stop", "restart":
		return c.sudo(ctx, time.Minute, "systemctl", action, alertbotService)
	case "update":
		// Обновление — та же установка: скрипт перекачает бинарник, а конфиг
		// с id живого сообщения не тронет.
		return c.sudo(ctx, alertbotTimeout, "sh", alertbotInstaller)
	default:
		return "", fmt.Errorf("неизвестное действие: %s", action)
	}
}

// AlertbotActivate делает сторожа рабочим ботом, а бота-администратора —
// остановленным. Именно enable, а не только start: без него после перезагрузки
// сервера поднимется прежний, и человек узнает об этом, когда тревоги не
// придут.
//
// Панель делает это сама, а не подкомандой MTProxyL: сторож ставится и поверх
// официального скрипта, где таких подкоманд нет.
func (c *Client) AlertbotActivate(ctx context.Context) (string, error) {
	// Чужого останавливаем первым: две службы в одном чате — это два опросчика
	// на один токен и путаница в том, кто отвечает.
	if _, err := c.run(ctx, "tgbot", "stop"); err != nil {
		// Бот-администратор может быть не установлен вовсе — это не помеха.
		if !errors.Is(err, ErrTgbotUnsupported) {
			var ce *CommandError
			if !errors.As(err, &ce) {
				return "", err
			}
		}
	}
	if _, err := c.sudo(ctx, time.Minute, "systemctl", "enable", alertbotService); err != nil {
		return "", err
	}
	return c.sudo(ctx, time.Minute, "systemctl", "restart", alertbotService)
}

// TgbotActivate — зеркало: поднимает бота-администратора и глушит сторожа.
func (c *Client) TgbotActivate(ctx context.Context) (string, error) {
	if _, err := c.sudo(ctx, time.Minute, "systemctl", "disable", alertbotService); err != nil {
		// Сторожа может не быть — тогда и выключать нечего.
		_ = err
	}
	_, _ = c.sudo(ctx, time.Minute, "systemctl", "stop", alertbotService)
	return c.run(ctx, "tgbot", "start")
}

func (c *Client) AlertbotUninstall(ctx context.Context) (string, error) {
	return c.sudo(ctx, time.Minute, "sh", alertbotInstaller, "--uninstall")
}

// alertbotSettable — что панели позволено менять. Белый список, а не свободный
// ключ: значение уходит в чужой процесс.
var alertbotSettable = map[string]string{
	"alert_threshold":        "percent",
	"connect_fail_threshold": "percent",
	"timezone":               "zone",
	"chat_id":                "chat",
}

func (c *Client) AlertbotSet(ctx context.Context, key, value string) (string, error) {
	kind, ok := alertbotSettable[key]
	if !ok {
		return "", fmt.Errorf("неизвестная настройка: %s", key)
	}
	value = strings.TrimSpace(value)
	switch kind {
	case "percent":
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 || n > 99 {
			return "", fmt.Errorf("порог задаётся числом от 1 до 99")
		}
	case "chat":
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return "", fmt.Errorf("ID чата — целое число")
		}
		if err := ValidateChatID(n); err != nil {
			return "", err
		}
	case "zone":
		// Пусто — «определять самому», это законный выбор. Имя зоны проверяет
		// сам бот: у него под рукой встроенная база часовых поясов.
	}
	return c.runAlertbot(ctx, "config", "set", key, value)
}

// runAlertbot зовёт бинарник сторожа.
func (c *Client) runAlertbot(ctx context.Context, args ...string) (string, error) {
	return c.sudo(ctx, time.Minute, alertbotBin, args...)
}

// sudo запускает команду от root по поимённому списку в sudoers панели.
// Отдельно от run(): тот прибит к скрипту MTProxyL, а сторож живёт сам по себе.
func (c *Client) sudo(ctx context.Context, timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	full := append([]string{"-n", name}, args...)
	cmd := exec.CommandContext(ctx, "sudo", full...)
	cmd.Env = []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"}

	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if sink := progressFrom(ctx); sink != nil {
		cmd.Stdout = io.MultiWriter(&stdout, sink)
		cmd.Stderr = io.MultiWriter(&stderr, sink)
	}

	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return stdout.String(), fmt.Errorf("%s: не ответил за %s", name, timeout)
		}
		detail := lastMeaningfulLine(stderr.String(), stdout.String())
		// «a password is required» и «not allowed» означают одно: правила sudo
		// у панели старше её самой.
		if strings.Contains(detail, "password is required") || strings.Contains(detail, "not allowed") {
			return stdout.String(), ErrAlertbotUnsupported
		}
		if detail == "" {
			detail = err.Error()
		}
		return stdout.String(), fmt.Errorf("%s: %s", name, detail)
	}
	return stdout.String(), nil
}

func systemctlQuiet(ctx context.Context, verb, unit string) bool {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "systemctl", verb, "--quiet", unit).Run() == nil
}
