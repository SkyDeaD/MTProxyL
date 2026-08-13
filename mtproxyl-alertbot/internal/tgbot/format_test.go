package tgbot

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Liafanx/mtproxyl-alertbot/internal/mtproxyl"
	"github.com/Liafanx/mtproxyl-alertbot/internal/uplink"
)

func sampleResult(total, ok int) *mtproxyl.CheckResult {
	r := &mtproxyl.CheckResult{
		TotalProbes:   total,
		SuccessProbes: ok,
		CheckedAt:     time.Date(2026, 8, 11, 17, 21, 51, 0, time.Local),
		Level:         mtproxyl.LevelGreen,
	}
	if total > 0 {
		r.Percentage = float64(ok) / float64(total) * 100
	}
	for i := 0; i < total; i++ {
		p := mtproxyl.ProbeDetail{City: fmt.Sprintf("Город-%d", i), Network: "Провайдер", TLSSuccess: i < ok}
		if !p.TLSSuccess {
			p.Error = "соединение не установлено"
		}
		r.Probes = append(r.Probes, p)
	}
	return r
}

func baseView(r *mtproxyl.CheckResult) View {
	return View{
		Result:              r,
		Target:              mtproxyl.Target{Host: "tg-plug.example.uz", Port: 443, SNI: "tg-plug.example.uz"},
		Quota:               mtproxyl.QuotaState{Budget: 250, Remaining: 180},
		AutoCheck:           true,
		AvailabilityEnabled: true,
		Interval:            15 * time.Minute,
		Threshold:           60,
		Now:                 time.Date(2026, 8, 11, 17, 30, 0, 0, time.Local),
	}
}

func TestRenderStatusHasSummaryAndProbes(t *testing.T) {
	out := RenderStatus(baseView(sampleResult(20, 19)))

	for _, want := range []string{
		"Доступность из РФ — 95%",
		"19 из 20 зондов",
		"tg-plug.example.uz:443",
		"SNI:",
		"Проверено: 11.08.2026, 17:21:51",
		"Автопроверка: включена, раз в 15 мин",
		"Квота Globalping: 180 / 250",
		"Зонды (20)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("в сообщении нет %q\n---\n%s", want, out)
		}
	}
	if strings.Count(out, "✅") != 19 || strings.Count(out, "❌") != 1 {
		t.Errorf("ожидалось 19 успешных и 1 упавший зонд:\n%s", out)
	}
}

// Имена провайдеров приходят какими угодно. Неэкранированный «&» ломает
// разметку, и Telegram отвечает 400 вместо сообщения.
func TestRenderStatusEscapesProviderNames(t *testing.T) {
	r := sampleResult(1, 0)
	r.Probes[0].Network = `AT&T <Wireless>`
	r.Probes[0].City = `Москва & область`
	r.Probes[0].Error = `сброс <соединения> & таймаут`

	out := RenderStatus(baseView(r))

	if strings.Contains(out, "AT&T <Wireless>") {
		t.Errorf("имя провайдера не экранировано:\n%s", out)
	}
	for _, want := range []string{"AT&amp;T", "&lt;Wireless&gt;", "Москва &amp; область"} {
		if !strings.Contains(out, want) {
			t.Errorf("нет экранированного %q\n---\n%s", want, out)
		}
	}
}

func TestRenderStatusEscapesTargetAndSNI(t *testing.T) {
	v := baseView(sampleResult(1, 1))
	v.Target.Host = "evil<b>.example"
	v.Target.SNI = "evil&sni"

	out := RenderStatus(v)

	if strings.Contains(out, "evil<b>.example") {
		t.Errorf("хост не экранирован:\n%s", out)
	}
	if !strings.Contains(out, "evil&amp;sni") {
		t.Errorf("SNI не экранирован:\n%s", out)
	}
}

// 50 зондов с длинными именами — предельный случай: probe_limit больше 50 не
// бывает, а имена провайдеров бывают очень длинными.
func TestRenderStatusFitsTelegramLimit(t *testing.T) {
	r := sampleResult(50, 25)
	long := strings.Repeat("Очень Длинное Имя Провайдера ", 6)
	for i := range r.Probes {
		r.Probes[i].Network = long
		r.Probes[i].City = strings.Repeat("Населённый Пункт ", 4)
		r.Probes[i].Error = strings.Repeat("развёрнутое описание отказа ", 5)
	}

	v := baseView(r)
	v.Uplink = loadedUplink()
	out := RenderStatus(v)

	if n := len([]rune(out)); n > MessageLimit {
		t.Fatalf("длина сообщения %d рун, предел %d", n, MessageLimit)
	}
	assertBalancedPre(t, out)
	// Ужиматься должен список зондов, а не показатели связи: они и есть повод
	// читать сообщение.
	for _, want := range []string{"Писатели", "Нагрузка", "Всего соединений"} {
		if !strings.Contains(out, want) {
			t.Errorf("при полусотне зондов пропало %q", want)
		}
	}
}

// loadedUplink — нагруженный сервер: двенадцать дата-центров и полная
// статистика. На нём проверяется, что сообщение остаётся в пределах лимита.
func loadedUplink() *uplink.Status {
	u := liveUplink()
	rtt := 142.0
	u.DCs = nil
	for _, n := range []int{-203, -5, -4, -3, -2, -1, 1, 2, 3, 4, 5, 203} {
		u.DCs = append(u.DCs, uplink.DCRtt{
			DC: n, RTTEmaMs: &rtt, AliveWriters: 3, RequiredWriters: 3, CoveragePct: 100,
		})
	}
	u.Connections, u.ConnectionsBad = 414515, 34556
	u.Users, u.ActiveIPs, u.TrafficOct = 1, 44, 77200000000
	u.HandshakeFails = 12832
	for i := range 6 {
		u.BadClasses = append(u.BadClasses, uplink.ClassCount{
			Class: strings.Repeat("длинное имя класса ", 2) + fmt.Sprint(i), Count: int64(1000 - i),
		})
		u.HandshakeClasses = append(u.HandshakeClasses, uplink.ClassCount{
			Class: strings.Repeat("причина сбоя ", 2) + fmt.Sprint(i), Count: int64(500 - i),
		})
	}
	return u
}

// Незакрытый <pre> Telegram не прощает: он отвечает 400, и сообщения не будет
// вовсе — то есть вместо укороченного статуса не придёт никакого.
func assertBalancedPre(t *testing.T, out string) {
	t.Helper()
	if open, closed := strings.Count(out, "<pre"), strings.Count(out, "</pre>"); open != closed {
		t.Fatalf("таблица оборвана: <pre> открыт %d раз, закрыт %d\n---\n%s", open, closed, out[len(out)-300:])
	}
	// Блок открывается парой тегов, значит и закрываться обязан парой: с одним
	// незакрытым <code> Telegram отвечает 400 так же охотно.
	if open, closed := strings.Count(out, "<code"), strings.Count(out, "</code>"); open != closed {
		t.Fatalf("кодовый блок оборван: <code> открыт %d раз, закрыт %d\n---\n%s", open, closed, out[len(out)-300:])
	}
}

// Когда зонды не влезают, надо сказать сколько их осталось, а не молча обрезать.
func TestRenderStatusReportsTruncatedProbes(t *testing.T) {
	r := sampleResult(50, 0)
	for i := range r.Probes {
		// Уникальные причины у каждого зонда — сводка внизу раздувается и
		// вытесняет строки таблицы.
		r.Probes[i].Error = fmt.Sprintf("отдельная развёрнутая причина отказа номер %d, весьма многословная", i)
	}
	v := baseView(r)
	v.Uplink = liveUplink()

	out := RenderStatus(v)

	if n := len([]rune(out)); n > MessageLimit {
		t.Fatalf("длина %d рун превышает предел %d", n, MessageLimit)
	}
	assertBalancedPre(t, out)
	if !strings.Contains(out, "и ещё") {
		t.Errorf("обрезка произошла молча, без строки об остатке:\n%s", out[len(out)-400:])
	}
}

// Причины отказов не должны повторяться у каждой строки: одна формулировка на
// пять зондов — это одна строка сводки с количеством.
func TestRenderStatusGroupsFailureReasons(t *testing.T) {
	r := sampleResult(20, 17)
	for i := range r.Probes {
		if !r.Probes[i].TLSSuccess {
			r.Probes[i].Error = "Request timeout"
		}
	}

	out := RenderStatus(baseView(r))

	if !strings.Contains(out, "Почему не дошли:") {
		t.Fatalf("нет сводки причин:\n%s", out)
	}
	if !strings.Contains(out, "Request timeout — 3") {
		t.Errorf("причины не сгруппированы с количеством:\n%s", out)
	}
	if n := strings.Count(out, "Request timeout"); n != 1 {
		t.Errorf("причина повторяется %d раз, ожидалась одна строка сводки", n)
	}
}

func TestRenderStatusShortMessageIsNotTruncated(t *testing.T) {
	out := RenderStatus(baseView(sampleResult(20, 19)))
	if strings.Contains(out, "и ещё") {
		t.Errorf("короткое сообщение обрезано зря:\n%s", out)
	}
}

func TestRenderStatusDownBanner(t *testing.T) {
	v := baseView(sampleResult(20, 11))
	v.Banners = []Banner{BannerDown}
	v.PrevPercentage = 95
	v.PrevKnown = true
	v.AlertSince = v.Now.Add(-25 * time.Minute)

	out := RenderStatus(v)

	for _, want := range []string{
		"ПАДЕНИЕ ДОСТУПНОСТИ",
		"Было 95% → стало 55% (11 / 20)",
		"Длится 25 мин · порог 60%",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("в алерте нет %q\n---\n%s", want, out)
		}
	}
}

func TestRenderStatusRecoveredBanner(t *testing.T) {
	v := baseView(sampleResult(20, 18))
	v.Banners = []Banner{BannerRecovered}
	v.PrevPercentage = 55
	v.PrevKnown = true
	v.AlertSince = v.Now.Add(-90 * time.Minute)

	out := RenderStatus(v)

	if !strings.Contains(out, "ДОСТУПНОСТЬ ВОССТАНОВЛЕНА") {
		t.Errorf("нет шапки восстановления:\n%s", out)
	}
	if !strings.Contains(out, "Авария длилась 1 ч 30 мин") {
		t.Errorf("нет длительности аварии:\n%s", out)
	}
}

// Сорвавшаяся проверка не обнуляет доступность: показываем причину и рядом —
// последний известный вердикт, а не 0%.
func TestRenderStatusFailureKeepsLastVerdict(t *testing.T) {
	v := baseView(sampleResult(20, 19))
	v.Failure = &Failure{Reason: "сервис проверки не отвечает", At: v.Now}

	out := RenderStatus(v)

	for _, want := range []string{
		"Проверка не удалась",
		"сервис проверки не отвечает",
		"Ниже — последний известный вердикт",
		"Доступность из РФ — 95%",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("нет %q\n---\n%s", want, out)
		}
	}
	if strings.Contains(out, "— 0%") {
		t.Errorf("неудача показана как нулевая доступность:\n%s", out)
	}
}

func TestRenderStatusFailureWithoutAnyVerdict(t *testing.T) {
	v := baseView(nil)
	v.Failure = &Failure{Reason: "некуда стучаться", At: v.Now}

	out := RenderStatus(v)

	if !strings.Contains(out, "Удачных проверок пока не было") {
		t.Errorf("нет объяснения про отсутствие вердикта:\n%s", out)
	}
}

func TestRenderStatusNoResultYet(t *testing.T) {
	out := RenderStatus(baseView(nil))
	if !strings.Contains(out, "Проверок ещё не было") {
		t.Errorf("нет заглушки до первой проверки:\n%s", out)
	}
}

func TestRenderStatusIPBlock(t *testing.T) {
	v := baseView(sampleResult(1, 1))
	v.Host = HostInfo{Addrs: []string{"203.0.113.10"}, ServerIP: "203.0.113.10"}

	out := RenderStatus(v)

	if !strings.Contains(out, "A-запись: <code>203.0.113.10</code>") {
		t.Errorf("нет A-записи:\n%s", out)
	}
	if !strings.Contains(out, "IP сервера: <code>203.0.113.10</code>") {
		t.Errorf("нет IP сервера:\n%s", out)
	}
	if strings.Contains(out, "ведёт не на этот сервер") {
		t.Errorf("предупреждение о расхождении при совпадающих адресах:\n%s", out)
	}
}

func TestRenderStatusIPMismatchWarning(t *testing.T) {
	v := baseView(sampleResult(1, 1))
	v.Host = HostInfo{Addrs: []string{"203.0.113.10"}, ServerIP: "198.51.100.7", Mismatch: true}

	out := RenderStatus(v)

	if !strings.Contains(out, "ведёт не на этот сервер") {
		t.Errorf("нет предупреждения о расхождении:\n%s", out)
	}
}

func TestRenderStatusAutoCheckOff(t *testing.T) {
	v := baseView(sampleResult(2, 2))
	v.AutoCheck = false

	out := RenderStatus(v)

	if !strings.Contains(out, "Автопроверка: выключена") {
		t.Errorf("не показано, что автопроверка выключена:\n%s", out)
	}
}

func TestHumanInterval(t *testing.T) {
	cases := map[time.Duration]string{
		15 * time.Minute:                  "15 мин",
		time.Hour:                         "1 ч",
		90 * time.Minute:                  "1 ч 30 мин",
		0:                                 "15 мин",
		6*time.Hour + 30*time.Minute:      "6 ч 30 мин",
		time.Duration(45) * time.Minute:   "45 мин",
		time.Duration(2) * time.Hour:      "2 ч",
		time.Duration(3660) * time.Second: "1 ч 1 мин",
	}
	for d, want := range cases {
		if got := humanInterval(d); got != want {
			t.Errorf("humanInterval(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestRenderStartReplyCarriesChatID(t *testing.T) {
	out := RenderStartReply(123456789, false)
	if !strings.Contains(out, "<code>123456789</code>") {
		t.Errorf("в ответе на /start нет chat_id:\n%s", out)
	}
	if !strings.Contains(out, "ID админа") {
		t.Errorf("не сказано, куда вписывать ID:\n%s", out)
	}
}

// Список зондов режется своей логикой, но шапка события, длинная цель и текст
// отказа складываются независимо от неё. Сообщение длиннее предела Telegram не
// отправляется вовсе — вместо статуса пришло бы ничего.
func TestRenderStatusClampsEvenWithoutProbes(t *testing.T) {
	v := baseView(&mtproxyl.CheckResult{
		Percentage: 55, TotalProbes: 20, SuccessProbes: 11,
		CheckedAt: time.Now(), Level: mtproxyl.LevelYellow,
	})
	v.Banners = []Banner{BannerDown}
	v.PrevKnown = true
	v.PrevPercentage = 95
	v.Target.Host = strings.Repeat("длинный-хост.", 400)
	v.Target.SNI = strings.Repeat("длинный-sni.", 400)
	v.Failure = &Failure{Reason: strings.Repeat("развёрнутая причина отказа ", 200), At: v.Now}

	out := RenderStatus(v)

	if n := len([]rune(out)); n > MessageLimit {
		t.Fatalf("длина %d рун превышает предел %d", n, MessageLimit)
	}
}

func TestClampCutsOnLineBoundary(t *testing.T) {
	long := strings.Repeat("строка сообщения\n", 500)
	out := clamp(long)

	if n := len([]rune(out)); n > MessageLimit {
		t.Fatalf("длина %d рун превышает предел %d", n, MessageLimit)
	}
	if !strings.HasSuffix(out, "…") {
		t.Errorf("обрезка не помечена многоточием: ...%q", out[len(out)-20:])
	}
	// Обрыв по границе строки не разрывает теги разметки.
	if strings.Count(out, "<") != strings.Count(out, ">") {
		t.Error("обрезка разорвала HTML-тег")
	}
}

// ── Второй блок: связь с дата-центрами ──────────────────────────────────────

func liveUplink() *uplink.Status {
	rtt := 142.0
	return &uplink.Status{
		CheckedAt:       time.Date(2026, 8, 11, 17, 34, 2, 0, time.Local),
		EngineUp:        true,
		Version:         "1.2.3",
		UptimeSeconds:   3600,
		Applicable:      true,
		AliveWriters:    65,
		RequiredWriters: 43,
		DCs: []uplink.DCRtt{
			{DC: 1, RTTEmaMs: &rtt, AliveWriters: 19, RequiredWriters: 3, CoveragePct: 100},
			{DC: 3, RTTEmaMs: nil, AliveWriters: 0, RequiredWriters: 3, CoveragePct: 0},
		},
		HasFailRate: true,
		Attempts:    1000,
		Fails:       4,
		FailRate:    0.004,
		HardFails:   0,
		Level:       uplink.LevelGreen,
	}
}

func TestRenderStatusHasUplinkBlock(t *testing.T) {
	v := baseView(sampleResult(20, 19))
	v.Uplink = liveUplink()

	out := RenderStatus(v)

	for _, want := range []string{
		"Связь с Telegram — норма",
		"выход: прокси → дата-центры",
		"Писатели: <b>65</b> живых / 43 нужно",
		"Попыток подключения: 1000",
		"отброшено ретраев",
		"DC 1",
		"142 мс",
		"Движок: 1.2.3",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("в блоке связи нет %q\n---\n%s", want, out)
		}
	}
	// Дата-центр без писателей показывается, но аварией не объявляется.
	if !strings.Contains(out, "⚪ DC 3") || !strings.Contains(out, "—") {
		t.Errorf("дата-центр без RTT не показан:\n%s", out)
	}
}

// Блок связи обязан идти до списка зондов: обрезка считает уже занятое место,
// и поставленный после зондов блок при полусотне зондов просто не поместился бы.
func TestUplinkBlockComesBeforeProbes(t *testing.T) {
	v := baseView(sampleResult(50, 25))
	long := strings.Repeat("Очень Длинное Имя Провайдера ", 6)
	for i := range v.Result.Probes {
		v.Result.Probes[i].Network = long
	}
	v.Uplink = liveUplink()

	out := RenderStatus(v)

	if n := len([]rune(out)); n > MessageLimit {
		t.Fatalf("длина %d рун превышает предел %d", n, MessageLimit)
	}
	uplinkAt := strings.Index(out, "Связь с Telegram")
	probesAt := strings.Index(out, "<b>Зонды (50):</b>")
	if uplinkAt < 0 {
		t.Fatalf("блок связи вытеснен списком зондов:\n%s", out)
	}
	if probesAt >= 0 && uplinkAt > probesAt {
		t.Error("блок связи стоит после списка зондов — при полусотне зондов он пропадёт")
	}
}

// Выключенный middle proxy — не авария: так работает часть установок.
func TestRenderStatusUplinkNotApplicable(t *testing.T) {
	v := baseView(sampleResult(2, 2))
	v.Uplink = &uplink.Status{EngineUp: true, Applicable: false, NotApplicableReason: "функция выключена в конфиге движка"}

	out := RenderStatus(v)

	if !strings.Contains(out, "данные недоступны — функция выключена в конфиге движка") {
		t.Errorf("нет объяснения про выключенный middle proxy:\n%s", out)
	}
	if strings.Contains(out, "ПОТЕРЯЛ СВЯЗЬ") {
		t.Errorf("выключенный middle proxy показан как авария:\n%s", out)
	}
}

func TestRenderStatusUplinkNoData(t *testing.T) {
	v := baseView(sampleResult(2, 2))
	v.Uplink = &uplink.Status{EngineError: "движок telemt не отвечает"}

	out := RenderStatus(v)

	if !strings.Contains(out, "Связь с Telegram — нет данных") {
		t.Errorf("нет строки об отсутствии данных:\n%s", out)
	}
	if !strings.Contains(out, "движок telemt не отвечает") {
		t.Errorf("не показана причина:\n%s", out)
	}
}

func TestRenderStatusUplinkDownBanner(t *testing.T) {
	v := baseView(sampleResult(20, 19))
	u := liveUplink()
	u.AliveWriters = 0
	u.Level = uplink.LevelRed
	u.Problems = []string{"нет ни одного живого писателя — прокси не может писать в Telegram"}
	v.Uplink = u
	v.Banners = []Banner{BannerUplinkDown}
	v.UplinkSince = v.Now.Add(-4 * time.Minute)

	out := RenderStatus(v)

	for _, want := range []string{
		"ПРОКСИ ПОТЕРЯЛ СВЯЗЬ С TELEGRAM",
		"не может писать в Telegram",
		"Длится 4 мин",
		"режут выход к дата-центрам, а не вход к прокси",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("в тревоге нет %q\n---\n%s", want, out)
		}
	}
}

// Две аварии сразу дают одно сообщение с двумя шапками, а не два сообщения.
func TestRenderStatusShowsSeveralBanners(t *testing.T) {
	v := baseView(sampleResult(20, 5))
	v.Uplink = liveUplink()
	v.Banners = []Banner{BannerEngineDown, BannerDown}
	v.PrevKnown = true
	v.PrevPercentage = 95

	out := RenderStatus(v)

	if !strings.Contains(out, "ДВИЖОК НЕДОСТУПЕН") {
		t.Errorf("нет шапки движка:\n%s", out)
	}
	if !strings.Contains(out, "ПАДЕНИЕ ДОСТУПНОСТИ") {
		t.Errorf("нет шапки доступности:\n%s", out)
	}
}

func TestRenderStatusIPChangedBanner(t *testing.T) {
	v := baseView(sampleResult(2, 2))
	v.Banners = []Banner{BannerIPChanged}
	v.PrevIP, v.NewIP = "203.0.113.10", "198.51.100.7"

	out := RenderStatus(v)

	if !strings.Contains(out, "Сменился внешний адрес сервера") {
		t.Errorf("нет шапки смены адреса:\n%s", out)
	}
	if !strings.Contains(out, "198.51.100.7") || !strings.Contains(out, "203.0.113.10") {
		t.Errorf("не показаны оба адреса:\n%s", out)
	}
}

// Пока сирена выключена, об этом надо напоминать в каждом сообщении: иначе
// легко поставить паузу на время работ и забыть о ней.
func TestRenderStatusShowsMuteNote(t *testing.T) {
	v := baseView(sampleResult(2, 2))
	v.MutedUntil = v.Now.Add(30 * time.Minute)

	out := RenderStatus(v)

	if !strings.Contains(out, "Тревоги заглушены до") {
		t.Errorf("нет напоминания о паузе:\n%s", out)
	}
	if !strings.Contains(out, "/unmute") {
		t.Errorf("не сказано, как снять паузу:\n%s", out)
	}
}

func TestRenderStatusMuteForever(t *testing.T) {
	v := baseView(sampleResult(2, 2))
	v.MuteForever = true

	if out := RenderStatus(v); !strings.Contains(out, "заглушены до отмены") {
		t.Errorf("нет напоминания о бессрочной паузе:\n%s", out)
	}
}

// Внешняя проверка может быть выключена в конфиге панели — тогда бот всё равно
// работает и показывает связь с дата-центрами, которая от неё не зависит.
func TestRenderStatusWithoutAvailabilityCheck(t *testing.T) {
	v := baseView(nil)
	v.AvailabilityEnabled = false
	v.Uplink = liveUplink()

	out := RenderStatus(v)

	if !strings.Contains(out, "проверка выключена в конфиге панели") {
		t.Errorf("не сказано, что внешняя проверка выключена:\n%s", out)
	}
	if strings.Contains(out, "Проверок ещё не было") {
		t.Errorf("обещана первая проверка, которой не будет:\n%s", out)
	}
	if strings.Contains(out, "Автопроверка:") {
		t.Errorf("показана автопроверка при выключенной проверке:\n%s", out)
	}
	if !strings.Contains(out, "Связь с Telegram") {
		t.Errorf("блок связи пропал вместе с выключенной проверкой:\n%s", out)
	}
}

// ── Часовой пояс ────────────────────────────────────────────────────────────

// Панель форматирует время в браузере читателя, бот — на сервере. Без явного
// указания зоны расхождение выглядит поломкой.
func TestRenderStatusShowsServerZone(t *testing.T) {
	out := RenderStatus(baseView(sampleResult(2, 2)))

	if !strings.Contains(out, "время сервера:") {
		t.Errorf("не указана зона, в которой показано время:\n%s", out)
	}
}

func TestZoneNoteFormat(t *testing.T) {
	resetZoneCache()
	t.Cleanup(resetZoneCache)

	tashkent := time.FixedZone("+05", 5*3600)
	tzCached, tzCheckAt = tashkent, time.Now()

	if got := zoneNote(View{}, time.Now()); got != "UTC+05" {
		t.Errorf("zoneNote = %q, ожидалось UTC+05", got)
	}

	tzCached, tzCheckAt = time.FixedZone("MSK", 3*3600), time.Now()
	if got := zoneNote(View{}, time.Now()); got != "MSK, UTC+03" {
		t.Errorf("zoneNote = %q, ожидалось «MSK, UTC+03»", got)
	}

	tzCached, tzCheckAt = time.FixedZone("-0330", -3*3600-1800), time.Now()
	if got := zoneNote(View{}, time.Now()); got != "UTC-03:30" {
		t.Errorf("zoneNote = %q, ожидалось UTC-03:30", got)
	}
}

// Смена пояса на сервере должна подхватываться без перезапуска панели: Go
// определяет time.Local один раз за процесс, поэтому зону перечитываем сами.
func TestLocalZoneIsRereadAfterTTL(t *testing.T) {
	resetZoneCache()
	t.Cleanup(func() {
		resetZoneCache()
		tzEnv, tzNow = os.Getenv, time.Now
	})

	zone := "UTC"
	tzEnv = func(k string) string {
		if k == "TZ" {
			return zone
		}
		return ""
	}
	base := time.Now()
	tzNow = func() time.Time { return base }

	if got := localZone().String(); got != "UTC" {
		t.Fatalf("зона = %q, ожидалась UTC", got)
	}

	zone = "Asia/Tashkent"
	// В пределах TTL зона не перечитывается — лишних обращений к диску не надо.
	if got := localZone().String(); got != "UTC" {
		t.Errorf("зона перечитана раньше срока: %q", got)
	}

	tzNow = func() time.Time { return base.Add(tzRecheck + time.Minute) }
	if got := localZone().String(); got != "Asia/Tashkent" {
		t.Errorf("зона = %q, смена пояса не подхватилась", got)
	}
}

// ── Нагрузка ────────────────────────────────────────────────────────────────

func TestRenderStatusShowsLoad(t *testing.T) {
	v := baseView(sampleResult(2, 2))
	u := liveUplink()
	u.Connections = 15198
	u.ConnectionsBad = 2105
	u.BadClasses = []uplink.ClassCount{{Class: "TLS handshake — bad client", Count: 1998}}
	v.Uplink = u

	out := RenderStatus(v)

	for _, want := range []string{"Всего соединений", "15 198", "Ошибочных", "TLS handshake"} {
		if !strings.Contains(out, want) {
			t.Errorf("в блоке нагрузки нет %q\n---\n%s", want, out)
		}
	}
}

func TestRenderStatusHidesLoadWhenUnknown(t *testing.T) {
	v := baseView(sampleResult(2, 2))
	v.Uplink = liveUplink() // без счётчиков соединений

	if out := RenderStatus(v); strings.Contains(out, "Соединений:") {
		t.Errorf("показан пустой блок нагрузки:\n%s", out)
	}
}

// ── Таблица зондов и статистика ─────────────────────────────────────────────

func TestRenderStatusProbeTableHasHeaderAndAlignment(t *testing.T) {
	r := sampleResult(3, 3)
	r.Probes[0].City, r.Probes[0].Network = "Novosibirsk", "MTS"
	r.Probes[1].City, r.Probes[1].Network = "Saint Petersburg", "SkyNET"
	r.Probes[2].City, r.Probes[2].Network = "Ufa", "New Hosting Technologies"

	out := RenderStatus(baseView(r))

	if !strings.Contains(out, "Город") || !strings.Contains(out, "Провайдер") {
		t.Errorf("нет заголовка колонок:\n%s", out)
	}
	assertBalancedPre(t, out)
	// Города разной длины должны занимать одинаковую ширину — иначе колонка
	// провайдеров разъедется и смысл таблицы теряется.
	if !strings.Contains(out, "✅ Novosibirsk       MTS") {
		t.Errorf("колонки не выровнены:\n%s", out)
	}
	if strings.Contains(out, "MTS   \n") {
		t.Errorf("в конце строк остались лишние пробелы:\n%s", out)
	}
}

func TestRenderStatusLoadBlock(t *testing.T) {
	v := baseView(sampleResult(2, 2))
	u := liveUplink()
	u.Connections, u.ConnectionsBad = 395317, 34029
	u.ActiveIPs, u.TrafficOct, u.Users = 86, 74500000000, 1
	u.HandshakeFails = 11936
	u.BadClasses = []uplink.ClassCount{{Class: "TLS handshake — bad client", Count: 23326}}
	u.HandshakeClasses = []uplink.ClassCount{{Class: "Таймаут", Count: 4234}}
	v.Uplink = u

	out := RenderStatus(v)

	for _, want := range []string{
		"Всего соединений",
		"395 317",
		"34 029 (8.6%)",
		"Активных IP",
		"69.4 ГБ",
		"Пользователей",
		"Ошибки соединений",
		"Сбои рукопожатия",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("в блоке нагрузки нет %q\n---\n%s", want, out)
		}
	}
	assertBalancedPre(t, out)
}

// Подпись зоны должна называть саму зону, а не только смещение: «Asia/Tashkent»
// — это то, что человек вводил в панели и видит в timedatectl.
func TestZoneNoteNamesTheZone(t *testing.T) {
	resetZoneCache()
	t.Cleanup(resetZoneCache)
	SetTimezone("Asia/Tashkent")

	out := RenderStatus(baseView(sampleResult(2, 2)))

	if !strings.Contains(out, "Asia/Tashkent") {
		t.Errorf("в подписи нет имени зоны:\n%s", out[len(out)-200:])
	}
	if !strings.Contains(out, "UTC+05") {
		t.Errorf("в подписи нет смещения:\n%s", out[len(out)-200:])
	}
}

// Заданная в панели зона перекрывает системную — без этого панель в контейнере
// всегда писала бы UTC.
func TestTimezoneOverrideWins(t *testing.T) {
	resetZoneCache()
	t.Cleanup(func() {
		resetZoneCache()
		tzEnv = os.Getenv
	})
	tzEnv = func(string) string { return "UTC" }

	SetTimezone("Europe/Moscow")
	if got := localZone().String(); got != "Europe/Moscow" {
		t.Errorf("зона = %q, заданная в панели не применилась", got)
	}

	SetTimezone("")
	if got := localZone().String(); got != "UTC" {
		t.Errorf("зона = %q, после сброса ожидалось автоопределение", got)
	}
}

func TestValidTimezone(t *testing.T) {
	for _, ok := range []string{"", "Asia/Tashkent", "Europe/Moscow", "UTC"} {
		if !ValidTimezone(ok) {
			t.Errorf("ValidTimezone(%q) = false", ok)
		}
	}
	for _, bad := range []string{"Asia/Tashkent2", "чепуха", "Moscow"} {
		if ValidTimezone(bad) {
			t.Errorf("ValidTimezone(%q) = true, ожидался отказ", bad)
		}
	}
}

// ── Таблица дата-центров ────────────────────────────────────────────────────

// Боевой снимок: номера бывают отрицательными, у части дата-центров писателей
// нет вовсе. Всё это показывается — решение об аварии принимается по сумме.
func TestRenderStatusDCTableHandlesProductionSnapshot(t *testing.T) {
	rtt := 37.7
	v := baseView(sampleResult(2, 2))
	u := liveUplink()
	u.DCs = []uplink.DCRtt{
		{DC: -203, RTTEmaMs: &rtt, AliveWriters: 3, RequiredWriters: 3, CoveragePct: 100},
		{DC: -3, RTTEmaMs: nil, AliveWriters: 0, RequiredWriters: 3, CoveragePct: 0},
		{DC: 4, RTTEmaMs: &rtt, AliveWriters: 6, RequiredWriters: 10, CoveragePct: 60},
	}
	v.Uplink = u

	out := RenderStatus(v)

	for _, want := range []string{
		"Писатели", "Покрытие",
		"✅ DC -203", // отрицательные номера печатаются как есть
		"⚪ DC -3",   // дата-центр без писателей виден, но не тревога
		"6 / 10",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("в таблице дата-центров нет %q\n---\n%s", want, out)
		}
	}
	// Колонки должны совпадать по ширине, иначе таблица теряет смысл.
	if !strings.Contains(out, "✅ DC -203") || !strings.Contains(out, "⚪ DC -3   ") {
		t.Errorf("колонка номеров не выровнена:\n%s", out)
	}
	assertBalancedPre(t, out)
}

// Длинный хвост из единичных причин не должен вытеснять список зондов.
func TestRenderStatusFoldsLongClassTail(t *testing.T) {
	v := baseView(sampleResult(2, 2))
	u := liveUplink()
	u.Connections, u.ConnectionsBad = 1000, 120
	for i := range 12 {
		u.BadClasses = append(u.BadClasses, uplink.ClassCount{
			Class: fmt.Sprintf("класс_%d", i), Count: int64(10),
		})
	}
	v.Uplink = u

	out := RenderStatus(v)

	if !strings.Contains(out, "прочие") {
		t.Errorf("хвост причин не свёрнут:\n%s", out)
	}
	if strings.Contains(out, "класс_11") {
		t.Errorf("показаны все причины, хотя хвост должен быть свёрнут:\n%s", out)
	}
}

// Зона снимается один раз на рендер. Иначе истёкший кэш посреди отрисовки даёт
// сообщение, где время проверки в одной зоне, а подпись — про другую: ровно
// это и случилось на боевом сервере, где вердикт «помолодел» на пять часов.
func TestRenderStatusUsesOneZoneThroughout(t *testing.T) {
	resetZoneCache()
	t.Cleanup(resetZoneCache)

	tashkent, err := time.LoadLocation("Asia/Tashkent")
	if err != nil {
		t.Fatalf("зона не загрузилась: %s", err)
	}
	tzCached, tzCheckAt = tashkent, time.Now()

	// Кэш протухает после первого же обращения, а системная зона к этому
	// моменту «сменилась» на UTC.
	calls := 0
	tzNow = func() time.Time {
		calls++
		if calls > 1 {
			return time.Now().Add(2 * tzRecheck)
		}
		return time.Now()
	}
	tzEnv = func(string) string { return "UTC" }
	t.Cleanup(func() {
		tzNow = time.Now
		tzEnv = os.Getenv
	})

	checked := time.Date(2026, 8, 13, 4, 27, 13, 0, time.UTC)
	r := sampleResult(20, 20)
	r.CheckedAt = checked
	v := baseView(r)
	v.Now = checked.Add(13 * time.Minute)

	out := RenderStatus(v)

	if !strings.Contains(out, "09:27:13") {
		t.Errorf("время проверки не в зоне рендера:\n%s", out)
	}
	if strings.Contains(out, "04:27:13") {
		t.Errorf("время осталось в прежней зоне, хотя подпись уже другая:\n%s", out)
	}
	if !strings.Contains(out, "Asia/Tashkent") {
		t.Errorf("подпись зоны разошлась со временем:\n%s", out)
	}
}

// Пауза кончилась, а авария идёт — сообщение приходит со звуком и обязано
// объяснить, почему пришло.
func TestRenderStatusMuteExpiredBanner(t *testing.T) {
	v := baseView(sampleResult(20, 20))
	v.Banners = []Banner{BannerMuteExpired}

	out := RenderStatus(v)

	if !strings.Contains(out, "Пауза кончилась") {
		t.Errorf("нет шапки про истёкшую паузу:\n%s", out)
	}
}

// ── Время и частота в блоке связи ───────────────────────────────────────────

// У доступности возраст цифр виден в шапке, и у связи с дата-центрами он нужен
// ровно так же: иначе непонятно, свежие они или наблюдение отвалилось.
func TestRenderStatusUplinkShowsAgeAndInterval(t *testing.T) {
	v := baseView(sampleResult(20, 20))
	u := liveUplink()
	u.CheckedAt = v.Now.Add(-3 * time.Minute)
	v.Uplink = u
	v.UplinkInterval = time.Minute

	out := RenderStatus(v)

	if !strings.Contains(out, "3 мин назад") {
		t.Errorf("в блоке связи нет возраста цифр:\n%s", out)
	}
	if !strings.Contains(out, "наблюдение раз в 1 мин") {
		t.Errorf("в блоке связи не сказано, как часто он обновляется:\n%s", out)
	}
}

// Частота приходит снаружи: не задана — строки нет, врать про период нельзя.
func TestRenderStatusUplinkOmitsIntervalWhenUnknown(t *testing.T) {
	v := baseView(sampleResult(20, 20))
	v.Uplink = liveUplink()

	if out := RenderStatus(v); strings.Contains(out, "наблюдение раз в") {
		t.Errorf("частота напечатана, хотя её никто не сообщал:\n%s", out)
	}
}
