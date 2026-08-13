// Package config — настройки алерт-бота на диске.
//
// Формат повторяет config.json бота MTProxyL: один файл с правами 600, который
// пишут с двух сторон — установщик от root и сам бот, когда запоминает id
// живого сообщения. Поэтому запись атомарная, а чтение терпит и отсутствие
// файла, и мусор в нём: бот без настроек просто ждёт, пока их зададут, и не
// падает в цикле перезапусков.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// DefaultPath — там же, где живёт сам бот.
const DefaultPath = "/opt/mtproxyl-alertbot/config.json"

// Config — всё, что оператор задаёт, плюс то, что бот помнит между запусками.
type Config struct {
	// Token и ChatID — куда писать. ChatID может быть личкой, группой или
	// каналом: у отрицательного id другие правила про команды и удаление, и
	// бот их учитывает сам.
	Token  string `json:"token"`
	ChatID int64  `json:"chat_id"`

	// Пороги тревог. Ноль означает «взять значение по умолчанию».
	AlertThreshold       float64 `json:"alert_threshold,omitempty"`
	ConnectFailThreshold float64 `json:"connect_fail_threshold,omitempty"`

	// Timezone — пусто значит «определять самому». Нужен, потому что служба
	// может жить в окружении с чужой зоной.
	Timezone string `json:"timezone,omitempty"`

	// Mtproxyl — как звать CLI за вердиктом доступности.
	Script string `json:"script,omitempty"`
	Sudo   *bool  `json:"sudo,omitempty"`

	// Telemt — API движка для наблюдения за связью с дата-центрами.
	TelemtURL        string `json:"telemt_url,omitempty"`
	TelemtAuthHeader string `json:"telemt_auth_header,omitempty"`

	// Состояние, переживающее перезапуск. Плоскими полями — отсутствующий ключ
	// читается нулём, а ноль здесь значит «этого не было».
	StatusMessageID  int `json:"status_message_id,omitempty"`
	ServiceMessageID int `json:"service_message_id,omitempty"`

	AlertActive    bool    `json:"alert_active,omitempty"`
	AlertSinceUnix int64   `json:"alert_since_unix,omitempty"`
	LastAlertUnix  int64   `json:"last_alert_unix,omitempty"`
	LastPercentage float64 `json:"last_percentage,omitempty"`
	HasPercentage  bool    `json:"has_percentage,omitempty"`

	UplinkAlertActive    bool  `json:"uplink_alert_active,omitempty"`
	UplinkAlertSinceUnix int64 `json:"uplink_alert_since_unix,omitempty"`
	UplinkLastAlertUnix  int64 `json:"uplink_last_alert_unix,omitempty"`

	EngineAlertActive    bool  `json:"engine_alert_active,omitempty"`
	EngineAlertSinceUnix int64 `json:"engine_alert_since_unix,omitempty"`
	EngineLastAlertUnix  int64 `json:"engine_last_alert_unix,omitempty"`

	LastKnownIP    string `json:"last_known_ip,omitempty"`
	MutedUntilUnix int64  `json:"muted_until_unix,omitempty"`
	MuteForever    bool   `json:"mute_forever,omitempty"`
}

// UseSudo: по умолчанию да — служба работает от пользователя без прав, а к
// MTProxyL ходит по поимённому списку подкоманд в sudoers.
func (c Config) UseSudo() bool {
	return c.Sudo == nil || *c.Sudo
}

// Store читает и пишет файл настроек.
type Store struct {
	mu   sync.RWMutex
	path string
	cur  Config
}

func NewStore(path string) *Store {
	if path == "" {
		path = DefaultPath
	}
	return &Store{path: path}
}

// Load читает файл. Отсутствие файла — не ошибка: бота могли поставить, но ещё
// не настроить.
func (s *Store) Load() error {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("настройки не прочитаны: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("настройки не разобраны: %w", err)
	}

	s.mu.Lock()
	s.cur = cfg
	s.mu.Unlock()
	return nil
}

func (s *Store) Get() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cur
}

// Save перезаписывает файл целиком. Пишем во временный рядом и переименовываем:
// оборванная запись не должна оставить бота без токена.
func (s *Store) Save(cfg Config) error {
	s.mu.Lock()
	s.cur = cfg
	s.mu.Unlock()

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("настройки не сохранены: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("настройки не сохранены: %w", err)
	}
	return nil
}

// Dir — каталог файла настроек; рядом с ним бот больше ничего не держит.
func (s *Store) Dir() string { return filepath.Dir(s.path) }

func Unix(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

func FromUnix(v int64) time.Time {
	if v <= 0 {
		return time.Time{}
	}
	return time.Unix(v, 0)
}
