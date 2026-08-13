package mtproxylctl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Бот-сторож ставится тем же CLI, что и бот-администратор, и панель относится
// к нему так же: показывает состояние и дёргает подкоманды. Своего доступа к
// /opt/mtproxyl-alertbot у неё нет — каталог принадлежит боту и закрыт.
//
// Ботов два, а работает один: выбор хранит сам MTProxyL, поэтому панель его не
// дублирует, а спрашивает.

// ErrAlertbotUnsupported — установленный MTProxyL про сторожа ещё не знает.
var ErrAlertbotUnsupported = errors.New("установленный MTProxyL не умеет управлять ботом-сторожем")

// AlertbotConfig — настройки сторожа без токена: наружу токен не отдаётся
// никогда, только признак, что он задан.
type AlertbotConfig struct {
	ChatID               int64   `json:"chat_id"`
	AlertThreshold       float64 `json:"alert_threshold"`
	ConnectFailThreshold float64 `json:"connect_fail_threshold"`
	Timezone             string  `json:"timezone"`
	HasToken             bool    `json:"has_token"`
}

// AlertbotStatus — `mtproxyl alertbot status --json`.
type AlertbotStatus struct {
	Installed  bool           `json:"installed"`
	Configured bool           `json:"configured"`
	Active     bool           `json:"active"`
	Enabled    bool           `json:"enabled"`
	Dir        string         `json:"dir"`
	Service    string         `json:"service"`
	Config     AlertbotConfig `json:"config"`
}

// AlertbotChatHint объясняет, что вписать в поле адреса. Текст здесь, а не во
// фронтенде: панель и меню должны говорить одно и то же.
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
	out, err := c.run(ctx, "alertbot", "status", "--json")
	if err != nil {
		if unsupportedCommand(out, err) {
			return nil, ErrAlertbotUnsupported
		}
		return nil, err
	}
	var st AlertbotStatus
	if err := json.Unmarshal([]byte(firstJSONLine(out)), &st); err != nil {
		return nil, fmt.Errorf("состояние бота-сторожа не разобрано: %w", err)
	}
	return &st, nil
}

func (c *Client) AlertbotLogs(ctx context.Context, lines int) (string, error) {
	if lines <= 0 {
		lines = 100
	}
	out, err := c.run(ctx, "alertbot", "logs", "--json", strconv.Itoa(lines))
	if err != nil {
		if unsupportedCommand(out, err) {
			return "", ErrAlertbotUnsupported
		}
		return "", err
	}
	var payload struct {
		Lines string `json:"lines"`
	}
	if err := json.Unmarshal([]byte(firstJSONLine(out)), &payload); err != nil {
		return "", fmt.Errorf("журнал не разобран: %w", err)
	}
	return payload.Lines, nil
}

// AlertbotInstall ставит сторожа. Пустой токен допустим при переустановке —
// тогда скрипт берёт прежний из своего конфига.
func (c *Client) AlertbotInstall(ctx context.Context, token string, chat int64) (string, error) {
	args := []string{"alertbot", "install"}
	if token != "" {
		args = append(args, "--token", token)
	}
	if chat != 0 {
		args = append(args, "--chat", strconv.FormatInt(chat, 10))
	}
	return c.run(ctx, args...)
}

func (c *Client) AlertbotService(ctx context.Context, action string) (string, error) {
	switch action {
	case "start", "stop", "restart", "update":
	default:
		return "", fmt.Errorf("неизвестное действие: %s", action)
	}
	return c.run(ctx, "alertbot", action)
}

func (c *Client) AlertbotUninstall(ctx context.Context) (string, error) {
	return c.run(ctx, "alertbot", "uninstall", "--yes")
}

// alertbotSettable — что панели позволено менять. Белый список, а не свободный
// ключ: настройки уходят в CLI, и произвольная строка там ни к чему.
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
	switch kind {
	case "percent":
		n, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || n < 1 || n > 99 {
			return "", fmt.Errorf("порог задаётся числом от 1 до 99")
		}
	case "chat":
		n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil {
			return "", fmt.Errorf("ID чата — целое число")
		}
		if err := ValidateChatID(n); err != nil {
			return "", err
		}
	case "zone":
		// Пусто — «определять самому», это законный выбор. Имя зоны проверяет
		// скрипт: у него под рукой /usr/share/zoneinfo, а у панели может не
		// быть — она вправе жить в контейнере без tzdata.
	}
	return c.run(ctx, "alertbot", "set", key, value)
}
