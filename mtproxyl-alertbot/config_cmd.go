package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/Liafanx/mtproxyl-alertbot/internal/config"
)

// Свои подкоманды нужны там, где ни панели, ни меню MTProxyL нет: сторож
// ставится и поверх оригинального MTProxyL, а он про него ничего не знает.
// Тогда это единственный способ поправить порог или зону, не редактируя JSON
// руками.
const configUsage = `Настройки бота-сторожа.

  mtproxyl-alertbot config show
  mtproxyl-alertbot config set <ключ> <значение>

Ключи:
  alert_threshold          порог доступности, % (1-99)
  connect_fail_threshold   порог отказов к дата-центрам, % (1-99)
  timezone                 часовой пояс; пусто — определять самому
  chat_id                  адрес доставки: личка, группа или канал
  token                    токен от @BotFather
`

// runConfig обрабатывает `config …` и возвращает код возврата.
func runConfig(path string, args []string) int {
	store := config.NewStore(path)
	if err := store.Load(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	if len(args) == 0 {
		fmt.Print(configUsage)
		return 2
	}

	switch args[0] {
	case "show":
		showConfig(store.Get())
		return 0

	case "set":
		if len(args) < 2 {
			fmt.Print(configUsage)
			return 2
		}
		// Значение может быть пустым — так стирается часовой пояс.
		value := ""
		if len(args) > 2 {
			value = strings.Join(args[2:], " ")
		}
		cfg, err := applySetting(store.Get(), args[1], value)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		if err := store.Save(cfg); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Printf("%s: сохранено\n", args[1])
		restartService()
		return 0

	default:
		fmt.Print(configUsage)
		return 2
	}
}

func showConfig(cfg config.Config) {
	// Токен не печатаем никогда — только признак, что он задан: вывод легко
	// попадает в чужой терминал или в журнал.
	token := "не задан"
	if cfg.Token != "" {
		token = "задан"
	}
	zone := cfg.Timezone
	if zone == "" {
		zone = "определяется сам"
	}

	fmt.Printf("Токен:                  %s\n", token)
	fmt.Printf("Чат:                    %s\n", chatLabel(cfg.ChatID))
	fmt.Printf("Порог доступности:      %s%%\n", percentLabel(cfg.AlertThreshold, 60))
	fmt.Printf("Порог отказов к DC:     %s%%\n", percentLabel(cfg.ConnectFailThreshold, 20))
	fmt.Printf("Часовой пояс:           %s\n", zone)
	fmt.Printf("Скрипт MTProxyL:        %s\n", orDefault(cfg.Script, "/opt/mtproxyl/mtproxyl.sh"))
	fmt.Printf("API движка:             %s\n", orDefault(cfg.TelemtURL, "не задан"))
	if cfg.StatusMessageID != 0 {
		fmt.Printf("Живое сообщение:        %d\n", cfg.StatusMessageID)
	}
}

func applySetting(cfg config.Config, key, value string) (config.Config, error) {
	value = strings.TrimSpace(value)
	switch key {
	case "alert_threshold", "connect_fail_threshold":
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 || n > 99 {
			return cfg, fmt.Errorf("порог задаётся числом от 1 до 99")
		}
		if key == "alert_threshold" {
			cfg.AlertThreshold = float64(n)
		} else {
			cfg.ConnectFailThreshold = float64(n)
		}

	case "timezone":
		// Пусто — «определять самому», это законный выбор. Имя проверяем
		// сразу: молча превратиться в UTC оно не должно.
		if value != "" {
			if _, err := time.LoadLocation(value); err != nil {
				return cfg, fmt.Errorf("неизвестный часовой пояс: %s", value)
			}
		}
		cfg.Timezone = value

	case "chat_id":
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil || n == 0 {
			return cfg, fmt.Errorf("ID чата — целое число, не ноль")
		}
		cfg.ChatID = n

	case "token":
		if !looksLikeToken(value) {
			return cfg, fmt.Errorf("не похоже на токен: ожидается 1234567890:AAH…")
		}
		cfg.Token = value

	default:
		return cfg, fmt.Errorf("неизвестный ключ: %s", key)
	}
	return cfg, nil
}

func looksLikeToken(s string) bool {
	id, secret, ok := strings.Cut(s, ":")
	if !ok || len(id) < 5 || len(secret) < 30 {
		return false
	}
	for _, r := range id {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// restartService подхватывает настройки без ручного перезапуска. Молчит, если
// службы нет: бота могли запустить и руками.
func restartService() {
	cmd := exec.Command("systemctl", "restart", "mtproxyl-alertbot.service")
	if err := cmd.Run(); err != nil {
		fmt.Println("Служба не перезапущена — если бот запущен, перезапустите его сами.")
		return
	}
	fmt.Println("Служба перезапущена.")
}

func chatLabel(id int64) string {
	switch {
	case id == 0:
		return "не задан"
	case id < 0:
		return fmt.Sprintf("%d (группа или канал)", id)
	default:
		return strconv.FormatInt(id, 10)
	}
}

func percentLabel(v, def float64) string {
	if v <= 0 {
		v = def
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

func orDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}
