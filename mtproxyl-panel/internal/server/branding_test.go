package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Liafanx/mtproxyl-panel/internal/config"
	"github.com/Liafanx/mtproxyl-panel/internal/telemt_config"
)

// Имя панели должно доживать до перезапуска: иначе человек переименует её,
// обрадуется, а после обновления получит прежнюю безымянную вкладку.
func TestDisplayNamePersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[panel]\nservice_name = \"mtproxyl-panel\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := telemt_config.QuickUpdate(path, map[string]interface{}{
		"panel.display_name": "Прокси в Ташкенте",
	}); err != nil {
		t.Fatalf("настройка не сохранилась: %s", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "Прокси в Ташкенте") {
		t.Errorf("имени нет в конфиге:\n%s", data)
	}
	// Соседние ключи не должны пострадать — конфиг правится, а не переписывается.
	if !strings.Contains(string(data), "mtproxyl-panel") {
		t.Errorf("прежние настройки затёрты:\n%s", data)
	}
}

// Пустая настройка означает обычное имя, а не пустую шапку.
func TestDisplayNameFallsBackToDefault(t *testing.T) {
	s := &Server{cfg: &config.Config{}}
	if got := s.displayName(); got != "MTProxyL-Panel" {
		t.Errorf("имя по умолчанию = %q", got)
	}

	s.cfg.Panel.DisplayName = "  Панель у клиента  "
	if got := s.displayName(); got != "Панель у клиента" {
		t.Errorf("имя = %q, пробелы по краям должны срезаться", got)
	}
}
