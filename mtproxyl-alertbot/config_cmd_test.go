package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Liafanx/mtproxyl-alertbot/internal/config"
)

// Настройки правятся этими командами там, где нет ни панели, ни меню
// MTProxyL, — значит проверка значений должна жить здесь же, а не надеяться
// на чужую форму.
func TestApplySetting(t *testing.T) {
	cases := []struct {
		name    string
		key     string
		value   string
		wantErr bool
		check   func(config.Config) bool
	}{
		{"порог доступности", "alert_threshold", "70", false,
			func(c config.Config) bool { return c.AlertThreshold == 70 }},
		{"порог отказов", "connect_fail_threshold", "35", false,
			func(c config.Config) bool { return c.ConnectFailThreshold == 35 }},
		{"порог вне диапазона", "alert_threshold", "120", true, nil},
		{"порог не число", "alert_threshold", "много", true, nil},
		{"зона", "timezone", "Asia/Tashkent", false,
			func(c config.Config) bool { return c.Timezone == "Asia/Tashkent" }},
		// Пусто — «определять самому», это законный выбор, а не ошибка.
		{"зона сброшена", "timezone", "", false,
			func(c config.Config) bool { return c.Timezone == "" }},
		{"зона с опечаткой", "timezone", "Asia/Tashkent2", true, nil},
		{"канал", "chat_id", "-1001234567890", false,
			func(c config.Config) bool { return c.ChatID == -1001234567890 }},
		{"чат не число", "chat_id", "@channel", true, nil},
		{"токен", "token", "1234567890:AAHqwertyuiopasdfghjklzxcvbnm12345", false,
			func(c config.Config) bool { return c.Token != "" }},
		{"токен без двоеточия", "token", "1234567890AAH", true, nil},
		{"неизвестный ключ", "notify", "true", true, nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := applySetting(config.Config{}, tc.key, tc.value)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ошибки не было, хотя значение негодное")
				}
				return
			}
			if err != nil {
				t.Fatalf("неожиданная ошибка: %s", err)
			}
			if tc.check != nil && !tc.check(got) {
				t.Errorf("значение не применилось: %+v", got)
			}
		})
	}
}

// Запись идёт в тот же файл, что читает служба, и переживает перезапуск.
func TestConfigSetPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")

	if code := runConfig(path, []string{"set", "alert_threshold", "70"}); code != 0 {
		t.Fatalf("код возврата %d", code)
	}

	store := config.NewStore(path)
	if err := store.Load(); err != nil {
		t.Fatalf("настройки не прочитались: %s", err)
	}
	if store.Get().AlertThreshold != 70 {
		t.Errorf("порог не сохранён: %+v", store.Get())
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("файла нет: %s", err)
	}
	// В файле лежит токен: права шире 600 означали бы, что его прочтёт любой.
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("права на файл %o, ожидались 600", perm)
	}
}
