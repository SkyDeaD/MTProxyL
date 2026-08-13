package uplink

import (
	"bytes"
	"context"
	"log"
	"net/http"
	"os"
	"strings"
	"testing"
)

// Сводка движка ровно в том виде, в каком её читает панель: счётчик класса
// называется «total», а секунды приходят дробными. И то, и другое однажды уже
// стоило нам всей статистики в сообщении бота.
const productionSummary = `{
	"uptime_seconds": 83220.47,
	"connections_total": 414515,
	"connections_bad_total": 34556,
	"handshake_timeouts_total": 4487,
	"configured_users": 1,
	"connections_bad_by_class": [
		{"class":"tls_handshake_bad_client","total":23840},
		{"class":"tls_clienthello_truncated","total":518}
	],
	"handshake_failures_by_class": [
		{"class":"timeout","total":4487},
		{"class":"other","total":128}
	]
}`

const productionUsers = `[{"active_unique_ips":40,"total_octets":50000000000},
	{"active_unique_ips":4,"total_octets":27200000000}]`

// statsEngine — движок, отвечающий на всё, что нужно для блока нагрузки.
func statsEngine(path string, w http.ResponseWriter) bool {
	switch path {
	case "/v1/stats/summary":
		ok(w, productionSummary)
		return true
	case "/v1/users":
		ok(w, productionUsers)
		return true
	}
	return healthyEngine(path, w)
}

// Главный регрессионный тест итерации: дробные секунды и поле «total» больше
// не уносят с собой весь ответ. Раньше одна нестыковка типа выбрасывала объект
// целиком, и в сообщении не оставалось ни соединений, ни ошибок, ни аптайма —
// молча, без единой записи в журнале.
func TestCollectReadsProductionSummary(t *testing.T) {
	c := fakeTelemt(t, func(path string, w http.ResponseWriter) {
		if statsEngine(path, w) {
			return
		}
		switch path {
		case "/v1/runtime/me_quality":
			ok(w, productionDCs)
		default:
			ok(w, `{"enabled":false,"reason":"выключено"}`)
		}
	})
	st := newTestWatcher(c).collect(context.Background())

	if st.Connections != 414515 || st.ConnectionsBad != 34556 {
		t.Errorf("соединения: %d всего, %d с ошибкой", st.Connections, st.ConnectionsBad)
	}
	if st.Users != 1 {
		t.Errorf("пользователей: %d", st.Users)
	}
	if st.ActiveIPs != 44 || st.TrafficOct != 77200000000 {
		t.Errorf("адреса и трафик: %d, %d", st.ActiveIPs, st.TrafficOct)
	}
	if st.HandshakeFails != 4615 {
		t.Errorf("сбоев рукопожатия: %d, ожидалось 4615", st.HandshakeFails)
	}
	if len(st.BadClasses) != 2 || st.BadClasses[0].Count != 23840 {
		t.Errorf("классы ошибок разобраны неверно: %+v", st.BadClasses)
	}
	if st.Version != "1.2.3" {
		t.Errorf("версия движка: %q", st.Version)
	}
}

// Счётчик класса принимается под обоими именами: панель читает «total», а мы
// изначально ждали «count». Разные сборки движка тут не сойдутся, а нули
// вместо чисел никто бы не заметил.
func TestClassCountAcceptsTotalAndCount(t *testing.T) {
	c := fakeTelemt(t, func(path string, w http.ResponseWriter) {
		if path == "/v1/stats/summary" {
			ok(w, `{"connections_bad_by_class":[
				{"class":"a","total":5},{"class":"b","count":7}]}`)
			return
		}
		_ = statsEngine(path, w)
	})
	sum, _, err := c.Summary(context.Background())
	if err != nil {
		t.Fatalf("сводка не разобрана: %s", err)
	}
	got := map[string]int64{}
	for _, cc := range sum.ConnectionsBadByClass {
		got[cc.Class] = cc.Count
	}
	if got["a"] != 5 || got["b"] != 7 {
		t.Errorf("счётчики: %+v", got)
	}
}

// Кривое поле теряет только себя. Это и есть смысл щадящего разбора: движок
// волен изменить одно поле, но остальные числа человек увидеть должен.
func TestSummaryKeepsGoodFieldsWhenOneIsBroken(t *testing.T) {
	c := fakeTelemt(t, func(path string, w http.ResponseWriter) {
		if path == "/v1/stats/summary" {
			ok(w, `{"connections_total":100,"configured_users":"много"}`)
			return
		}
		_ = statsEngine(path, w)
	})

	sum, skipped, err := c.Summary(context.Background())
	if err != nil {
		t.Fatalf("сводка не разобрана целиком: %s", err)
	}
	if sum.ConnectionsTotal != 100 {
		t.Errorf("соединения потерялись вместе с кривым полем: %v", sum.ConnectionsTotal)
	}
	if len(skipped) != 1 || skipped[0] != "configured_users" {
		t.Errorf("непонятые поля не названы: %v", skipped)
	}
}

// О непонятых полях надо сказать вслух — но один раз. Опрос идёт каждую
// минуту, и повтор одной и той же жалобы залил бы журнал, после чего её
// перестали бы замечать вместе с настоящими.
func TestComplainRepeatsOnlyOnNewReason(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	w := newTestWatcher(nil)
	for range 3 {
		w.complain("сводка", nil, []string{"configured_users"})
	}
	if n := strings.Count(buf.String(), "configured_users"); n != 1 {
		t.Errorf("жалоба записана %d раз, ожидался один:\n%s", n, buf.String())
	}

	// Сменилась причина — значит появилось что сказать.
	w.complain("сводка", nil, []string{"uptime_seconds"})
	if !strings.Contains(buf.String(), "uptime_seconds") {
		t.Errorf("о новой причине никто не узнал:\n%s", buf.String())
	}

	// Всё наладилось — молчим, а не сообщаем о починке каждую минуту.
	before := buf.Len()
	w.complain("сводка", nil, nil)
	w.complain("сводка", nil, nil)
	if buf.Len() != before {
		t.Errorf("исправное состояние попало в журнал:\n%s", buf.String()[before:])
	}
}
