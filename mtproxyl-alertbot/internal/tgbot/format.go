package tgbot

import (
	"fmt"
	"html"
	"strings"
	"time"

	"github.com/Liafanx/mtproxyl-alertbot/internal/mtproxyl"
	"github.com/Liafanx/mtproxyl-alertbot/internal/uplink"
)

// Banner — шапка над телом сообщения. Тело у всех трёх видов одинаковое:
// меняется только то, что сверху, потому что сообщение в чате одно и то же.
type Banner int

const (
	// BannerNone — обычный статус, пришёл тихой правкой.
	BannerNone Banner = iota
	// BannerDown — доступность ушла ниже порога. Такое сообщение отправляется
	// заново, а не правится: правка не даёт звука, и её легко проспать.
	BannerDown
	// BannerRecovered — вернулись выше порога.
	BannerRecovered
	// BannerEngineDown — движок недоступен или ушёл в режим только чтения.
	BannerEngineDown
	// BannerEngineOK — движок вернулся.
	BannerEngineOK
	// BannerUplinkDown — прокси потерял связь с дата-центрами Telegram.
	BannerUplinkDown
	// BannerUplinkOK — связь с дата-центрами восстановлена.
	BannerUplinkOK
	// BannerEngineRestarted и BannerIPChanged — одноразовые: у них нет
	// «восстановления», о них просто сообщают один раз.
	BannerEngineRestarted
	BannerIPChanged
	// BannerMuteExpired — пауза кончилась, а авария всё ещё идёт. Без этой
	// шапки сообщение приходило со звуком и без единого слова о том, почему
	// оно вообще пришло: событий в этот такт не было.
	BannerMuteExpired
)

// Failure — сорвавшаяся попытка проверки. Живёт отдельно от вердикта: неудача
// не обнуляет доступность, она лишь означает, что свежих цифр нет.
type Failure struct {
	Reason string
	At     time.Time
}

// View — всё, из чего собирается сообщение.
type View struct {
	// Result — последний удачный вердикт. nil, если проверок ещё не было.
	Result *mtproxyl.CheckResult
	// Failure — непусто, если последняя попытка сорвалась.
	Failure *Failure

	Target    mtproxyl.Target
	Host      HostInfo
	Quota     mtproxyl.QuotaState
	AutoCheck bool
	Interval  time.Duration
	// AvailabilityEnabled=false — внешняя проверка выключена в конфиге панели.
	// Тогда о ней пишется одна строка, а не «проверок ещё не было»: ждать
	// нечего, и обещать первую проверку было бы неправдой.
	AvailabilityEnabled bool

	// Uplink — состояние исходящей связи и движка. nil, если наблюдение ещё не
	// дало первого результата.
	Uplink *uplink.Status

	// Banners — шапки события. Их может быть несколько: доступность и связь
	// независимы и могут сорваться одновременно, но сообщение всё равно одно.
	Banners []Banner
	// PrevPercentage — доступность до события, для строки «было X% → стало Y%».
	// PrevKnown отличает «раньше было 0%» от «раньше ничего не было».
	PrevPercentage float64
	PrevKnown      bool
	// AlertSince — когда началась авария доступности, для «длится N».
	AlertSince time.Time
	// UplinkSince и EngineSince — то же для двух других аварий.
	UplinkSince time.Time
	EngineSince time.Time
	// PrevIP и NewIP заполняются, когда сменился внешний адрес сервера.
	PrevIP, NewIP string

	// MutedUntil и MuteForever — пауза тревог. Пока она идёт, в шапке висит
	// напоминание: иначе легко забыть, что сирена выключена.
	MutedUntil  time.Time
	MuteForever bool

	Threshold float64
	Now       time.Time
	// UplinkInterval — как часто опрашивается связь с дата-центрами. Приходит
	// снаружи, а не зашит в текст: поменяется период — сообщение не должно
	// врать.
	UplinkInterval time.Duration

	// zone — часовой пояс этого рендера. Снимается один раз в RenderStatus:
	// localZone() перечитывает системную зону по истечении кэша и может
	// смениться прямо посреди отрисовки, а тогда время проверки печатается в
	// одной зоне, а подпись под сообщением — уже в другой. На боевом сервере
	// это выглядело так, будто вердикт помолодел на пять часов.
	zone *time.Location
}

// at приводит время к зоне этого рендера.
func (v View) at(t time.Time) time.Time {
	if v.zone == nil {
		return t.In(localZone())
	}
	return t.In(v.zone)
}

const (
	dotGreen  = "🟢"
	dotYellow = "🟡"
	dotRed    = "🔴"
)

// RenderStatus собирает текст сообщения. Разметка — HTML: она прощает
// незакрытые символы лучше Markdown, а имена провайдеров у зондов приходят
// какими угодно.
func RenderStatus(v View) string {
	v.zone = localZone()

	var b strings.Builder

	writeBanners(&b, v)

	if v.Failure != nil {
		b.WriteString("⚠️ <b>Проверка не удалась</b>\n")
		b.WriteString(esc(v.Failure.Reason) + "\n")
		b.WriteString("Попытка: " + stamp(v, v.Failure.At) + "\n")
		if v.Result == nil {
			b.WriteString("\nУдачных проверок пока не было.\n")
			writeFooter(&b, v)
			// Блок связи нужен и здесь: он наблюдается раз в минуту и не зависит
			// от того, успела ли пройти проверка доступности.
			writeUplink(&b, v)
			writeZone(&b, v)
			return clamp(b.String())
		}
		b.WriteString("\nНиже — последний известный вердикт.\n\n")
	}

	if v.Result == nil {
		if !v.AvailabilityEnabled {
			b.WriteString("<b>Доступность из РФ</b>: проверка выключена в конфиге панели.\n")
		} else {
			b.WriteString("Проверок ещё не было — первая пройдёт в ближайшие минуты.\n")
		}
		writeFooter(&b, v)
		// Первые четверть часа после запуска вердикта доступности ещё нет, а
		// связь с дата-центрами уже наблюдается — показываем то, что знаем.
		writeUplink(&b, v)
		writeZone(&b, v)
		return clamp(b.String())
	}

	r := v.Result
	fmt.Fprintf(&b, "%s <b>Доступность из РФ — %.0f%%</b>\n", levelDot(r.Level), r.Percentage)
	fmt.Fprintf(&b, "%d из %d зондов", r.SuccessProbes, r.TotalProbes)
	if ago := duration(v.Now, r.CheckedAt); ago != "" {
		b.WriteString(" · " + ago + " назад")
	}
	b.WriteString("\n\n")

	writeTarget(&b, v)
	b.WriteString("Проверено: " + stamp(v, r.CheckedAt) + "\n")
	writeFooter(&b, v)

	writeUplink(&b, v)
	// Зонды — последними: их обрезка считает уже занятое место, поэтому блок
	// связи, поставленный сюда, влезает всегда, а ужимается список зондов. Если
	// поменять местами, при полусотне зондов блок связи просто не поместится —
	// то есть пропадёт ровно тогда, когда он нужнее всего.
	writeProbes(&b, r)
	writeZone(&b, v)
	return clamp(b.String())
}

// writeUplink — второй блок сообщения: исходящая связь прокси с
// дата-центрами Telegram. Отвечает на вопрос, которого не видит проверка
// зондами: снаружи прокси может быть здоров, а писать в Telegram не может.
func writeUplink(b *strings.Builder, v View) {
	u := v.Uplink
	if u == nil {
		return
	}

	b.WriteString("\n──────────\n\n")

	switch {
	case u.EngineError != "":
		b.WriteString("⚠️ <b>Связь с Telegram — нет данных</b>\n")
		b.WriteString(esc(u.EngineError) + "\n")
		return
	case !u.Applicable:
		// Не авария: часть установок работает без middle proxy. Формулировка та
		// же, что в панели, чтобы бот и интерфейс говорили одно и то же.
		b.WriteString("<b>Связь с Telegram</b>: данные недоступны — " + esc(u.NotApplicableReason) + "\n")
		return
	}

	fmt.Fprintf(b, "%s <b>Связь с Telegram — %s</b>\n", levelDot(globalpingLevel(u.Level)), uplinkVerdict(u))
	b.WriteString("<i>выход: прокси → дата-центры</i>")
	// Возраст цифр — там же, где у доступности: без него непонятно, свежие они
	// или наблюдение отвалилось четверть часа назад.
	if ago := duration(v.Now, u.CheckedAt); ago != "" {
		b.WriteString(" · " + ago + " назад")
	}
	b.WriteString("\n\n")

	// Причины идут отдельными строками, а не в заголовке: там они складывались
	// в неразборчивую строку с двойным тире.
	for _, p := range u.Problems {
		b.WriteString("⚠️ " + esc(p) + "\n")
	}
	fmt.Fprintf(b, "Писатели: <b>%d</b> живых / %d нужно\n", u.AliveWriters, u.RequiredWriters)
	if u.HasFailRate && u.Attempts > 0 {
		// Обычные неудачные попытки — справка, а не признак аварии: движок
		// долбится сразу в несколько точек и берёт первую ответившую, поэтому
		// отброшенные ретраи попадают сюда даже при исправной связи.
		fmt.Fprintf(b, "Попыток подключения: %d, отброшено ретраев %.0f%%\n",
			u.Attempts, u.FailRate*100)
		if u.HardFails > 0 {
			fmt.Fprintf(b, "Отказов без повтора: <b>%d</b> (%.0f%%)\n", u.HardFails, u.HardRate*100)
		}
	}
	writeDCs(b, u.DCs)
	writeLoad(b, v, u)
	if u.Version != "" {
		fmt.Fprintf(b, "Движок: %s\n", esc(u.Version))
	}
	b.WriteString("Проверено: " + stampShort(v, u.CheckedAt))
	// Сколько раз в минуту — вопрос, который задают первым: у доступности
	// частота видна строкой «Автопроверка», а здесь её взять было неоткуда.
	if v.UplinkInterval > 0 {
		b.WriteString(" · наблюдение раз в " + humanInterval(v.UplinkInterval))
	}
	b.WriteString("\n")
}

// writeLoad — справка о нагрузке. На вердикт не влияет: «ошибочные» здесь про
// клиентов (обрезанный ClientHello, таймаут рукопожатия), а не про связь с
// дата-центрами, и на публичном прокси их доля всегда заметна.
func writeLoad(b *strings.Builder, v View, u *uplink.Status) {
	if u.Connections <= 0 && u.ActiveIPs <= 0 {
		return
	}

	var rows [][]string
	add := func(label, value string) { rows = append(rows, []string{label, value}) }
	if u.UptimeSeconds > 0 {
		add("Время работы", duration(v.Now, v.Now.Add(-time.Duration(u.UptimeSeconds)*time.Second)))
	}
	if u.Connections > 0 {
		add("Всего соединений", groupDigits(u.Connections))
		bad := groupDigits(u.ConnectionsBad)
		if u.ConnectionsBad > 0 {
			bad += fmt.Sprintf(" (%.1f%%)", float64(u.ConnectionsBad)/float64(u.Connections)*100)
		}
		add("Ошибочных", bad)
	}
	if u.Users > 0 {
		add("Пользователей", groupDigits(u.Users))
	}
	if u.ActiveIPs > 0 {
		add("Активных IP", groupDigits(u.ActiveIPs))
	}
	if u.TrafficOct > 0 {
		add("Трафик", humanBytes(u.TrafficOct))
	}

	b.WriteString("\n")
	b.WriteString(table("Нагрузка", nil, []bool{false, true}, rows))
	b.WriteString("\n")

	writeClasses(b, "Ошибки соединений", u.ConnectionsBad, u.BadClasses)
	writeClasses(b, "Сбои рукопожатия", u.HandshakeFails, u.HandshakeClasses)
}

// classLimit — сколько причин показываем. Остальные сворачиваются в строку
// «прочие»: длинный хвост из единичных случаев вытеснил бы список зондов, а
// сказать он способен только то, что уже сказано итогом.
const classLimit = 6

// classLabels — те же подписи, что панель показывает на дашборде
// (frontend/src/components/ConnectionErrors.tsx). Копия небольшая и
// осознанная: движок отдаёт машинные имена вроде tls_clienthello_truncated, и
// без перевода бот говорил бы с человеком не на том языке, что панель.
// Неизвестный класс печатается как есть — так же поступает и панель.
var classLabels = map[string]string{
	"timeout":                           "Таймаут",
	"other":                             "Прочее",
	"eof":                               "Преждевременный EOF",
	"unknown_tls_sni":                   "Неизвестный TLS SNI",
	"tls_clienthello_len_out_of_bounds": "Недопустимая длина TLS ClientHello",
	"tls_clienthello_read_error":        "Ошибка чтения TLS ClientHello",
	"tls_clienthello_truncated":         "Обрезанный TLS ClientHello",
	"tls_handshake_bad_client":          "TLS handshake — bad client",
	"tls_mtproto_bad_client":            "TLS MTProto — bad client",
}

func classLabel(class string) string {
	if name, ok := classLabels[class]; ok {
		return name
	}
	return class
}

// writeClasses печатает разбор по причинам.
func writeClasses(b *strings.Builder, title string, total int64, classes []uplink.ClassCount) {
	if len(classes) == 0 {
		return
	}
	rows := make([][]string, 0, classLimit+1)
	var shown int64
	for i, c := range classes {
		if i == classLimit && len(classes) > classLimit+1 {
			rows = append(rows, []string{"прочие", groupDigits(total - shown)})
			break
		}
		rows = append(rows, []string{shorten(classLabel(c.Class), 30), groupDigits(c.Count)})
		shown += c.Count
	}

	label := title
	if total > 0 {
		label += " " + groupDigits(total)
	}
	b.WriteString(table(label, nil, []bool{false, true}, rows))
	b.WriteString("\n")
}

// table собирает моноширинную таблицу: ширины колонок считаются по факту,
// числовые колонки жмутся вправо, хвостовые пробелы срезаются.
//
// label уходит в class="language-…". Telegram подписывает этим кодовый блок,
// и вместо безличного «копировать» в шапке видно, что в таблице. Документация
// имя блока не оговаривает — там сказано только про язык программирования, —
// поэтому расчёт скромный: не подхватится, останется прежнее «копировать».
//
// Содержимое ячеек экранируется здесь же: имена провайдеров вида «AT&T» иначе
// ломают разметку, и Telegram отклоняет сообщение целиком.
func table(label string, head []string, right []bool, rows [][]string) string {
	if len(rows) == 0 {
		return ""
	}
	cols := 0
	for _, r := range rows {
		cols = max(cols, len(r))
	}
	width := make([]int, cols)
	measure := func(cells []string) {
		for i, c := range cells {
			if i < cols {
				width[i] = max(width[i], len([]rune(c)))
			}
		}
	}
	measure(head)
	for _, r := range rows {
		measure(r)
	}

	var b strings.Builder
	// Пробелы в подписи заменяются неразрывными: в HTML пробел делит атрибут
	// class на несколько классов, и от подписи «Ошибки соединений» осталось бы
	// одно слово. Неразрывный пробел разделителем не считается, а выглядит так
	// же.
	b.WriteString(`<pre><code class="language-` + esc(strings.ReplaceAll(label, " ", " ")) + `">`)

	line := func(cells []string) {
		var row strings.Builder
		for i := range cols {
			cell := ""
			if i < len(cells) {
				cell = cells[i]
			}
			pad := strings.Repeat(" ", width[i]-len([]rune(cell)))
			if i < len(right) && right[i] {
				row.WriteString(pad + esc(cell))
			} else {
				row.WriteString(esc(cell) + pad)
			}
			if i < cols-1 {
				row.WriteString("  ")
			}
		}
		b.WriteString(strings.TrimRight(row.String(), " ") + "\n")
	}

	if len(head) > 0 {
		line(head)
		total := 2 * (cols - 1)
		for _, w := range width {
			total += w
		}
		b.WriteString(strings.Repeat("─", total) + "\n")
	}
	for _, r := range rows {
		line(r)
	}
	b.WriteString("</code></pre>")
	return b.String()
}

// humanBytes — трафик в привычном виде, как на дашборде панели.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d Б", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit && exp < 4; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %s", float64(n)/float64(div), [...]string{"КБ", "МБ", "ГБ", "ТБ", "ПБ"}[exp])
}

// shorten обрезает длинное имя многоточием, чтобы колонки не разъезжались.
func shorten(s string, max int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= max {
		return string(r)
	}
	return string(r[:max-1]) + "…"
}

// groupDigits разделяет тысячи узкими пробелами: 15198 читается плохо.
func groupDigits(n int64) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var out []byte
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ' ')
		}
		out = append(out, c)
	}
	return string(out)
}

func uplinkVerdict(u *uplink.Status) string {
	switch {
	case len(u.Problems) == 0:
		return "норма"
	case u.AliveWriters == 0:
		return "связи нет"
	default:
		return "с перебоями"
	}
}

// globalpingLevel переводит уровень наблюдателя в тот же светофор, которым
// обозначается доступность, — чтобы в одном сообщении цвета читались одинаково.
func globalpingLevel(l uplink.Level) mtproxyl.Level {
	switch l {
	case uplink.LevelGreen:
		return mtproxyl.LevelGreen
	case uplink.LevelYellow:
		return mtproxyl.LevelYellow
	default:
		return mtproxyl.LevelRed
	}
}

// writeDCs печатает дата-центры по два в строке. Показываем все, включая те,
// где писателей нет: на живом сервере это штатно, и человеку полезно видеть
// картину целиком — но решение об аварии по ним не принимается.
func writeDCs(b *strings.Builder, dcs []uplink.DCRtt) {
	if len(dcs) == 0 {
		return
	}
	rows := make([][]string, 0, len(dcs))
	for _, dc := range dcs {
		mark := "✅"
		if dc.AliveWriters == 0 {
			mark = "⚪"
		}
		rtt := "—"
		if dc.RTTEmaMs != nil {
			rtt = fmt.Sprintf("%.0f мс", *dc.RTTEmaMs)
		}
		// Номер печатается как есть, включая отрицательные: движок делит так
		// свою инфраструктуру, а придумывать расшифровку, которой нет ни в
		// панели, ни в самом движке, — значит выдумывать.
		rows = append(rows, []string{
			fmt.Sprintf("%s DC %d", mark, dc.DC),
			rtt,
			fmt.Sprintf("%d / %d", dc.AliveWriters, dc.RequiredWriters),
			fmt.Sprintf("%.0f%%", dc.CoveragePct),
		})
	}
	b.WriteString("\n")
	// Колонки — те же, что в панели: DC, RTT, писатели, покрытие.
	b.WriteString(table("Дата-центры",
		[]string{"   DC", "RTT", "Писатели", "Покрытие"},
		[]bool{false, true, true, true}, rows))
	b.WriteString("\n")
}

// clamp — последняя страховка от предела Telegram. Список зондов режется своей
// логикой, но шапка события, длинная цель и текст отказа складываются
// независимо, а сообщение длиннее предела не отправляется вовсе — вместо
// статуса пришло бы ничего. Режем по границе строки: теги разметки не
// пересекают перевод строки, поэтому такой обрыв не ломает HTML.
func clamp(s string) string {
	runes := []rune(s)
	if len(runes) <= MessageLimit {
		return closePre(s)
	}
	cut := string(runes[:MessageLimit-len("…</pre>")])
	if i := strings.LastIndexByte(cut, '\n'); i > 0 {
		cut = cut[:i+1]
	}
	return closePre(cut + "…")
}

// closePre дописывает закрывающий тег, если обрезка пришлась на середину
// таблицы. Незакрытый <pre> Telegram не прощает: он отвечает 400 и сообщения
// не будет вовсе — то есть вместо укороченного статуса не придёт никакого.
func closePre(s string) string {
	// Считаем по «<pre», а не по «<pre>»: блок открывается парой тегов
	// (<pre><code class="…">), и закрывать его надо такой же парой.
	if strings.Count(s, "<pre") > strings.Count(s, "</pre>") {
		return s + "</code></pre>"
	}
	return s
}

// writeBanners печатает шапки событий. Порядок — по важности: упавший движок
// объясняет всё остальное, поэтому идёт первым.
func writeBanners(b *strings.Builder, v View) {
	wrote := false
	for _, banner := range v.Banners {
		if banner == BannerNone {
			continue
		}
		writeBanner(b, v, banner)
		wrote = true
	}
	if muted := muteNote(v); muted != "" {
		b.WriteString(muted)
		wrote = true
	}
	if wrote {
		b.WriteString("\n──────────\n\n")
	}
}

func writeBanner(b *strings.Builder, v View, banner Banner) {
	switch banner {
	case BannerDown:
		b.WriteString("🚨 <b>ПАДЕНИЕ ДОСТУПНОСТИ</b>\n")
		writeChange(b, v)
		if since := duration(v.Now, v.AlertSince); since != "" {
			fmt.Fprintf(b, "Длится %s · порог %.0f%%\n", since, v.Threshold)
		} else {
			fmt.Fprintf(b, "Порог алерта: %.0f%%\n", v.Threshold)
		}
		b.WriteString("Часто это значит, что адрес попал под фильтр у части операторов.\n")

	case BannerRecovered:
		b.WriteString("✅ <b>ДОСТУПНОСТЬ ВОССТАНОВЛЕНА</b>\n")
		writeChange(b, v)
		if since := duration(v.Now, v.AlertSince); since != "" {
			b.WriteString("Авария длилась " + since + "\n")
		}

	case BannerUplinkDown:
		b.WriteString("🚨 <b>ПРОКСИ ПОТЕРЯЛ СВЯЗЬ С TELEGRAM</b>\n")
		if v.Uplink != nil {
			for _, p := range v.Uplink.Problems {
				b.WriteString(esc(p) + "\n")
			}
		}
		if since := duration(v.Now, v.UplinkSince); since != "" {
			b.WriteString("Длится " + since + "\n")
		}
		// Это принципиально другая авария, чем падение доступности, и человеку
		// надо сразу понимать разницу: снаружи прокси при этом здоров.
		b.WriteString("Снаружи прокси доступен — режут выход к дата-центрам, а не вход к прокси.\n")

	case BannerUplinkOK:
		b.WriteString("✅ <b>СВЯЗЬ С TELEGRAM ВОССТАНОВЛЕНА</b>\n")
		if since := duration(v.Now, v.UplinkSince); since != "" {
			b.WriteString("Авария длилась " + since + "\n")
		}

	case BannerEngineDown:
		b.WriteString("🚨 <b>ДВИЖОК НЕДОСТУПЕН</b>\n")
		if v.Uplink != nil {
			switch {
			case v.Uplink.EngineReadOnly:
				b.WriteString("Движок работает в режиме только чтения.\n")
			case v.Uplink.EngineError != "":
				b.WriteString(esc(v.Uplink.EngineError) + "\n")
			}
		}
		if since := duration(v.Now, v.EngineSince); since != "" {
			b.WriteString("Длится " + since + "\n")
		}

	case BannerEngineOK:
		b.WriteString("✅ <b>ДВИЖОК СНОВА НА СВЯЗИ</b>\n")
		if since := duration(v.Now, v.EngineSince); since != "" {
			b.WriteString("Недоступен был " + since + "\n")
		}

	case BannerEngineRestarted:
		b.WriteString("♻️ <b>Движок перезапустился</b>\n")
		if v.Uplink != nil && v.Uplink.UptimeSeconds > 0 {
			b.WriteString("Работает " + duration(v.Now, v.Now.Add(-time.Duration(v.Uplink.UptimeSeconds)*time.Second)) + "\n")
		}

	case BannerIPChanged:
		b.WriteString("📍 <b>Сменился внешний адрес сервера</b>\n")
		fmt.Fprintf(b, "Было <code>%s</code> → стало <code>%s</code>\n", esc(v.PrevIP), esc(v.NewIP))
		b.WriteString("Проверьте DNS и ссылки для клиентов — прежний адрес больше не отвечает.\n")

	case BannerMuteExpired:
		b.WriteString("🔔 <b>Пауза кончилась, авария продолжается</b>\n")
		b.WriteString("Тревоги снова включены. Подробности ниже.\n")
	}
}

// muteNote напоминает о выключенной сирене. Без этой строки легко поставить
// паузу на время работ и забыть о ней до следующей настоящей аварии.
func muteNote(v View) string {
	switch {
	case v.MuteForever:
		return "🔕 <b>Тревоги заглушены до отмены</b> — /unmute\n"
	case !v.MutedUntil.IsZero() && v.Now.Before(v.MutedUntil):
		return "🔕 <b>Тревоги заглушены до " + v.at(v.MutedUntil).Format("15:04") + "</b> — /unmute\n"
	}
	return ""
}

func writeChange(b *strings.Builder, v View) {
	if v.Result == nil {
		return
	}
	if !v.PrevKnown {
		// Первый вердикт в жизни бота: сравнивать не с чем, и «Было 0%» здесь
		// было бы выдумкой.
		fmt.Fprintf(b, "Сейчас %.0f%% (%d / %d)\n",
			v.Result.Percentage, v.Result.SuccessProbes, v.Result.TotalProbes)
		return
	}
	fmt.Fprintf(b, "Было %.0f%% → стало %.0f%% (%d / %d)\n",
		v.PrevPercentage, v.Result.Percentage, v.Result.SuccessProbes, v.Result.TotalProbes)
}

func writeTarget(b *strings.Builder, v View) {
	host := v.Target.Host
	if host == "" && v.Result != nil {
		// Цель не переспросили — берём ту, что записал сам вердикт.
		host = v.Result.Target
	}
	if host == "" {
		return
	}
	if v.Target.Host != "" && v.Target.Port != 0 {
		fmt.Fprintf(b, "Цель: <code>%s:%d</code>\n", esc(v.Target.Host), v.Target.Port)
	} else {
		fmt.Fprintf(b, "Цель: <code>%s</code>\n", esc(host))
	}
	if v.Target.SNI != "" {
		b.WriteString("  SNI: <code>" + esc(v.Target.SNI) + "</code>\n")
	}
	if len(v.Host.Addrs) > 0 {
		b.WriteString("  A-запись: <code>" + esc(strings.Join(v.Host.Addrs, ", ")) + "</code>\n")
	}
	if v.Host.ServerIP != "" {
		b.WriteString("  IP сервера: <code>" + esc(v.Host.ServerIP) + "</code>\n")
	}
	if v.Host.Mismatch {
		b.WriteString("  ⚠️ домен ведёт не на этот сервер — проверьте DNS, если переезжали\n")
	}
}

func writeFooter(b *strings.Builder, v View) {
	if !v.AvailabilityEnabled {
		// Строка про автопроверку без самой проверки только запутала бы.
		return
	}
	if v.AutoCheck {
		b.WriteString("Автопроверка: включена, раз в " + humanInterval(v.Interval) + "\n")
	} else {
		b.WriteString("Автопроверка: выключена — только по кнопке\n")
	}
	if v.Quota.Budget > 0 {
		fmt.Fprintf(b, "Квота Globalping: %d / %d кредитов\n", v.Quota.Remaining, v.Quota.Budget)
	}
}

// writeZone печатает пояс один раз на всё сообщение: повторять его у каждой
// метки времени было бы шумом.
func writeZone(b *strings.Builder, v View) {
	when := v.Now
	if v.Result != nil && !v.Result.CheckedAt.IsZero() {
		when = v.Result.CheckedAt
	}
	if note := zoneNote(v, when); note != "" {
		b.WriteString("\n<i>время сервера: " + note + "</i>\n")
	}
}

// writeProbes рисует таблицу зондов моноширинно: город и провайдер выровнены
// в колонки, чтобы взгляд шёл по столбцу, а не выискивал названия в строках.
// Причины отказов не повторяются у каждой строки — они собраны сводкой ниже.
//
// Таблица идёт последней и рисуется в остаток бюджета сообщения. Это важно:
// она внутри <pre>, и если бы её обрезал общий clamp, закрывающий тег потерялся
// бы, а Telegram отклонил бы сообщение целиком — то есть статуса не было бы
// вовсе.
func writeProbes(b *strings.Builder, r *mtproxyl.CheckResult) {
	if len(r.Probes) == 0 {
		return
	}

	cityW, netW := 0, 0
	for _, p := range r.Probes {
		cityW = max(cityW, len([]rune(probeCity(p))))
		netW = max(netW, len([]rune(probeNet(p))))
	}
	cityW, netW = min(cityW, 16), min(netW, 18)

	tail := renderReasons(groupReasons(r.Probes))
	// Запас в 24 знака — на служебную разметку блока и строку «…и ещё N».
	budget := MessageLimit - len([]rune(b.String())) - len([]rune(tail)) - 24

	rows := make([][]string, 0, len(r.Probes))
	used := 0
	for _, p := range r.Probes {
		mark := "✅"
		if !p.TLSSuccess {
			mark = "❌"
		}
		city, net := shorten(probeCity(p), cityW), shorten(probeNet(p), netW)
		used += len([]rune(mark)) + len([]rune(city)) + len([]rune(net)) + 4
		if used > budget {
			break
		}
		rows = append(rows, []string{mark + " " + city, net})
	}

	b.WriteString("\n")
	b.WriteString(table(fmt.Sprintf("Зонды (%d)", len(r.Probes)),
		[]string{"   Город", "Провайдер"}, nil, rows))
	b.WriteString("\n")
	if len(rows) < len(r.Probes) {
		fmt.Fprintf(b, "…и ещё %d зондов\n", len(r.Probes)-len(rows))
	}
	b.WriteString(tail)
}

// groupReasons сводит причины отказов в список с количеством: одна и та же
// формулировка у пяти зондов — это одна строка, а не пять.
func groupReasons(probes []mtproxyl.ProbeDetail) []uplink.ClassCount {
	counts := map[string]int64{}
	var order []string
	for _, p := range probes {
		if p.TLSSuccess {
			continue
		}
		reason := oneLine(p.Error)
		if reason == "" {
			reason = "причина не указана"
		}
		if _, seen := counts[reason]; !seen {
			order = append(order, reason)
		}
		counts[reason]++
	}
	out := make([]uplink.ClassCount, 0, len(order))
	for _, r := range order {
		out = append(out, uplink.ClassCount{Class: r, Count: counts[r]})
	}
	return out
}

func renderReasons(reasons []uplink.ClassCount) string {
	if len(reasons) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n<b>Почему не дошли:</b>\n")
	for _, r := range reasons {
		fmt.Fprintf(&b, "  • %s — %d\n", esc(shorten(r.Class, 46)), r.Count)
	}
	return b.String()
}

func probeCity(p mtproxyl.ProbeDetail) string {
	if p.City != "" {
		return p.City
	}
	if p.Country != "" {
		return p.Country
	}
	return "зонд"
}

func probeNet(p mtproxyl.ProbeDetail) string {
	if p.Network != "" {
		return p.Network
	}
	if p.ASN > 0 {
		return fmt.Sprintf("AS%d", p.ASN)
	}
	return "—"
}

// RenderStartReply — ответ на /start. Человеку нужен его chat_id, чтобы
// вписать его в панель: другого способа узнать этот номер у него нет.
func RenderStartReply(chatID int64, known bool) string {
	if known {
		return fmt.Sprintf("Бот на связи. Ваш ID: <code>%d</code>\n\n"+
			"Статус доступности приходит одним сообщением и обновляется сам "+
			"после каждой проверки.", chatID)
	}
	return fmt.Sprintf("Ваш ID: <code>%d</code>\n\n"+
		"Впишите его в панели: «Доступность из России» → «Телеграм-бот» → "+
		"ID админа, — и сохраните. После этого сюда придёт статус.", chatID)
}

// RenderHelp объясняет, что означают цифры. Главное здесь — разница между
// двумя блоками: она неочевидна, а именно она и объясняет, почему прокси
// может быть «доступен» и при этом не работать.
func RenderHelp() string {
	return "<b>Что показывает бот</b>\n\n" +
		"<b>Доступность из РФ</b> — вход: видят ли вас клиенты. Зонды из российских " +
		"резидентских сетей делают TLS-рукопожатие с портом прокси. Падение обычно значит, " +
		"что адрес попал под фильтр у части операторов.\n\n" +
		"<b>Связь с Telegram</b> — выход: может ли прокси писать в дата-центры. Фильтрация " +
		"часто режет именно это: снаружи сервер доступен, зонды показывают 95%, а клиенты " +
		"не работают.\n\n" +
		"<b>Писатели</b> — соединения, через которые движок пишет в Telegram. Если живых " +
		"меньше, чем нужно, трафик идёт хуже; если ноль — связи нет вовсе.\n\n" +
		"Дата-центры показаны для полноты: у части из них писателей штатно нет, и само по " +
		"себе это не авария — поэтому тревога поднимается по общему счёту, а не по ним.\n\n" +
		"<b>Команды</b>\n" +
		"/status — показать статус сейчас\n" +
		"/check — запустить проверку (тратит квоту)\n" +
		"/mute — заглушить тревоги на время работ\n" +
		"/unmute — снять паузу"
}

// RenderTestMessage — то, что уходит по кнопке «Тест» в панели.
func RenderTestMessage(now time.Time) string {
	return "✅ <b>Бот подключён</b>\n\nСвязь с Telegram есть, время на сервере — " +
		stamp(View{zone: localZone()}, now) + ".\nСтатус придёт отдельным сообщением."
}

// ── Мелочи ──────────────────────────────────────────────────────────────────

func levelDot(l mtproxyl.Level) string {
	switch l {
	case mtproxyl.LevelGreen:
		return dotGreen
	case mtproxyl.LevelYellow:
		return dotYellow
	default:
		return dotRed
	}
}

// esc обязателен для всего, что пришло снаружи: имена провайдеров вида «AT&T»
// иначе ломают разметку, и Telegram отвечает 400 вместо сообщения.
func esc(s string) string { return html.EscapeString(s) }

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.Join(strings.Fields(s), " ")
	if len([]rune(s)) > 80 {
		return string([]rune(s)[:80]) + "…"
	}
	return s
}

// stampShort — время без секунд. Блок связи обновляется раз в минуту, и
// секунды в нём ничего не добавляют.
// plural выбирает форму слова: «1 день», «2 дня», «5 дней».
func plural(n int, one, few, many string) string {
	n %= 100
	if n >= 11 && n <= 14 {
		return many
	}
	switch n % 10 {
	case 1:
		return one
	case 2, 3, 4:
		return few
	default:
		return many
	}
}

func stampShort(v View, t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return v.at(t).Format("02.01.2006, 15:04")
}

// zoneNote — обозначение часового пояса, в котором показано время.
//
// Без него расхождение с интерфейсом панели выглядит поломкой: там время
// форматирует браузер в зоне читателя, а здесь — сервер в своей. Плюс сам
// сервер могли переключить, и полезно видеть, в какой зоне бот сейчас пишет.
func zoneNote(v View, t time.Time) string {
	if t.IsZero() {
		return ""
	}
	loc := v.zone
	if loc == nil {
		loc = localZone()
	}
	name, offset := t.In(loc).Zone()
	// У многих зон Zone() возвращает «+05», а человеку полезнее «Asia/Tashkent»
	// — это то, что он вводил в панели или видит в timedatectl.
	if n := loc.String(); n != "" && n != "Local" && strings.Contains(n, "/") {
		name = n
	}
	sign, hours, mins := "+", offset/3600, (offset%3600)/60
	if offset < 0 {
		sign, hours, mins = "-", -hours, -mins
	}
	utc := fmt.Sprintf("UTC%s%02d", sign, hours)
	if mins != 0 {
		utc += fmt.Sprintf(":%02d", mins)
	}
	// У многих зон имя выглядит как «+05» — дублировать его рядом с UTC+05
	// незачем.
	if name != "" && !strings.HasPrefix(name, "+") && !strings.HasPrefix(name, "-") {
		return name + ", " + utc
	}
	return utc
}

func stamp(v View, t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return v.at(t).Format("02.01.2006, 15:04:05")
}

func humanInterval(d time.Duration) string {
	switch {
	case d <= 0:
		return "15 мин"
	case d < time.Hour:
		return fmt.Sprintf("%d мин", int(d.Minutes()))
	case d%time.Hour == 0:
		return fmt.Sprintf("%d ч", int(d.Hours()))
	default:
		return fmt.Sprintf("%d ч %d мин", int(d.Hours()), int(d.Minutes())%60)
	}
}

// duration печатает, сколько длится авария. Пустая строка — начала не знаем.
func duration(now, since time.Time) string {
	if since.IsZero() || now.IsZero() || !now.After(since) {
		return ""
	}
	d := now.Sub(since)
	switch {
	case d < time.Minute:
		return "меньше минуты"
	case d < time.Hour:
		return fmt.Sprintf("%d мин", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d ч %d мин", int(d.Hours()), int(d.Minutes())%60)
	default:
		days := int(d.Hours()) / 24
		return fmt.Sprintf("%d %s %d ч", days, plural(days, "день", "дня", "дней"), int(d.Hours())%24)
	}
}
