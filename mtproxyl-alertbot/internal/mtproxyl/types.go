// Package mtproxyl читает вердикт «Доступности из России» у самого MTProxyL.
//
// Раньше проверку вела панель, и бот подписывался на её результат прямо в
// памяти процесса. С версии 1.4.8 считает скрипт: зонды опрашивает
// lib/availability.sh по таймеру, результат лежит в /opt/mtproxyl/availability,
// и один и тот же вердикт видят меню, панель и любой бот. Подписки внутри
// процесса больше нет и быть не может — значит вердикт мы спрашиваем, а не
// ждём.
//
// Имена типов оставлены прежними: JSON скрипта повторяет структуру, которую
// панель отдавала раньше, поле в поле.
package mtproxyl

import "time"

// Level — цвет светофора, как его считает скрипт: >=80 зелёный, >=50 жёлтый.
type Level string

const (
	LevelGreen  Level = "green"
	LevelYellow Level = "yellow"
	LevelRed    Level = "red"
)

// TLSInfo — сертификат, который вернуло рукопожатие. Боту он не нужен, но
// приходит в ответе, и молча терять его при разборе незачем.
type TLSInfo struct {
	Authorized bool   `json:"authorized"`
	CreatedAt  string `json:"createdAt"`
	ExpiresAt  string `json:"expiresAt"`
}

// ProbeDetail — что увидел один зонд.
type ProbeDetail struct {
	City      string   `json:"city"`
	Country   string   `json:"country"`
	Region    string   `json:"region"`
	Continent string   `json:"continent"`
	ASN       int      `json:"asn"`
	Network   string   `json:"network"`
	Tags      []string `json:"tags"`
	Status    string   `json:"status"`
	// TLSSuccess — вердикт этого зонда: рукопожатие прошло и вернулся
	// сертификат, то есть ровно то же, что делает клиент Telegram.
	TLSSuccess     bool     `json:"tls_success"`
	TLSInfo        *TLSInfo `json:"tls_info,omitempty"`
	HTTPStatusCode int      `json:"http_status_code,omitempty"`
	RawOutput      string   `json:"raw_output"`
	Error          string   `json:"error,omitempty"`
}

// CheckResult — одна законченная проверка.
type CheckResult struct {
	Percentage    float64       `json:"percentage"`
	Level         Level         `json:"level"`
	TotalProbes   int           `json:"total_probes"`
	SuccessProbes int           `json:"success_probes"`
	Target        string        `json:"target"`
	MeasurementID string        `json:"measurement_id"`
	CheckedAt     time.Time     `json:"checked_at"`
	Probes        []ProbeDetail `json:"probes,omitempty"`
	Error         string        `json:"error,omitempty"`
}

// QuotaState — остаток кредитов Globalping в скользящем часе.
type QuotaState struct {
	Budget         int  `json:"budget"`
	Spent          int  `json:"spent"`
	Remaining      int  `json:"remaining"`
	ResetInSeconds int  `json:"reset_in_seconds"`
	HasToken       bool `json:"has_token"`
}

// Target — куда стучится проверка. Скрипт отдаёт её отдельно от вердикта:
// внутри вердикта цель лежит уже строкой-меткой «host:port (SNI: sni)».
type Target struct {
	Host string `json:"host"`
	Port uint16 `json:"port"`
	SNI  string `json:"sni"`
}

// State — конверт `availability status --json`: настройки проверки, квота,
// цель и последний вердикт вместе.
type State struct {
	Enabled     bool         `json:"enabled"`
	AutoCheck   bool         `json:"auto_check"`
	TimerActive bool         `json:"timer_active"`
	Interval    int          `json:"interval"`
	Probes      int          `json:"probes"`
	Threshold   int          `json:"threshold"`
	NextRun     string       `json:"next_run"`
	Quota       QuotaState   `json:"quota"`
	Target      Target       `json:"target"`
	Result      *CheckResult `json:"result"`
	Message     string       `json:"message"`
}
