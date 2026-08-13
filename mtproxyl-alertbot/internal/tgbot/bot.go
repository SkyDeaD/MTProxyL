package tgbot

import (
	"context"
	"errors"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/Liafanx/mtproxyl-alertbot/internal/mtproxyl"
	"github.com/Liafanx/mtproxyl-alertbot/internal/uplink"
)

const (
	// callbackCheck запускает настоящее измерение — то же, что кнопка
	// «Проверить сейчас» в панели, с её кулдауном и расходом квоты.
	callbackCheck = "check"
	// callbackRefresh перерисовывает сообщение по уже известному вердикту.
	callbackRefresh = "refresh"

	// fastFailWindow — сколько ждём мгновенного отказа от RunCheckNow, прежде
	// чем сказать человеку «проверяю». Кулдаун и исчерпанная квота отвечают
	// сразу, настоящее измерение — десятки секунд.
	fastFailWindow = 2 * time.Second

	// pollBackoffMin/Max — пауза после сетевого сбоя опроса.
	pollBackoffMin = time.Second
	pollBackoffMax = 30 * time.Second

	// coalesceWindow — сколько ждать второй источник, прежде чем оценивать.
	// Достаточно, чтобы совпавшие по времени вердикты попали в одну доставку,
	// и незаметно для человека.
	coalesceWindow = 2 * time.Second

	// quietEditInterval — минимальная пауза между тихими правками сообщения.
	quietEditInterval = 5 * time.Minute

	// tombstone заменяет прежнее сообщение, если удалить его не вышло:
	// Telegram не даёт боту удалять свои сообщения старше 48 часов.
	tombstone = "⤵️ Актуальный статус — ниже"
)

// ErrNoToken и ErrNoAdmin — не сбои, а незаконченная настройка. Панель
// показывает их текст как подсказку, что осталось сделать.
var (
	ErrNoToken = errors.New("токен бота не задан")
	ErrNoAdmin = errors.New("ID админа не задан: напишите боту /start, он пришлёт ваш ID")
)

// Config — то, что оператор задаёт в панели.
type Config struct {
	Enabled        bool
	Token          string
	AdminID        int64
	AlertThreshold float64
}

// PersistedState — то, что обязано пережить перезапуск панели.
//
// ServiceMessageID — последний служебный ответ бота (справка, подтверждение
// паузы, отказ). Он живёт в единственном экземпляре: приходит новый — прежний
// убирается. Хранится вместе с остальным, потому что после перезапуска панели
// висящий в чате служебный ответ иначе стал бы неудаляемым и остался бы
// навсегда — ровно тот мусор, ради избавления от которого всё и затевалось.
type PersistedState struct {
	MessageID        int
	ServiceMessageID int
	Incidents        Incidents
}

// Deps — всё, что боту нужно от остальной панели. Функциями, а не ссылкой на
// *mtproxyl.Checker: пакет знает о проверке только её результат, обратной
// зависимости нет.
type Deps struct {
	RunCheckNow func(context.Context) (*mtproxyl.CheckResult, error)
	Snapshot    func() *mtproxyl.CheckResult
	Quota       func() mtproxyl.QuotaState
	AutoCheck   func() bool
	Target      func(context.Context) (mtproxyl.Target, error)
	Interval    time.Duration
	// UplinkSnapshot отдаёт последний вердикт наблюдения за связью с
	// дата-центрами; nil, если наблюдение выключено.
	UplinkSnapshot func() *uplink.Status
	// UplinkInterval — с какой частотой это наблюдение идёт. Показывается в
	// сообщении: «раз в минуту» — первый вопрос, который задают про этот блок.
	UplinkInterval time.Duration
	// AvailabilityEnabled=false — внешняя проверка выключена в конфиге панели.
	// Бот при этом работает: связь с дата-центрами он наблюдает сам.
	AvailabilityEnabled bool
	// Persist сохраняет PersistedState. Может быть nil — тогда состояние
	// живёт до перезапуска.
	Persist func(PersistedState) error
}

// Status — что показать в панели.
type Status struct {
	Running     bool   `json:"running"`
	BotUsername string `json:"bot_username,omitempty"`
	LastError   string `json:"last_error,omitempty"`
	MessageID   int    `json:"status_message_id,omitempty"`
}

// Bot держит одно живое сообщение в личке админа и обновляет его по каждому
// вердикту проверки доступности.
type Bot struct {
	deps     Deps
	resolver *Resolver
	now      func() time.Time

	mu       sync.Mutex
	cfg      Config
	client   *Client
	state    PersistedState
	lastGood *mtproxyl.CheckResult
	lastFail *Failure
	username string
	lastErr  string
	running  bool

	lastUplink *uplink.Status
	// pending* — то, что пришло и ещё не оценено. Оценка идёт один раз на
	// такт, после короткого окна ожидания: иначе два источника, сработавшие
	// одновременно, дали бы две доставки подряд.
	pendingResult *mtproxyl.CheckResult
	pendingUplink *uplink.Status
	// pendingIP — новый внешний адрес, ждущий подтверждения вторым свежим
	// наблюдением. В памяти, не на диске: это защита от разовой ошибки
	// сервиса определения IP, а не состояние, которое надо помнить.
	pendingIP  string
	lastEditAt time.Time
	// forceRedraw просит следующую перерисовку пройти в обход прореживания:
	// её ждёт человек, нажавший кнопку или набравший команду. relocate вдобавок
	// переносит сообщение вниз чата.
	forceRedraw bool
	relocate    bool

	// results и uplinks принимают вердикты источников. Ёмкость 1 с
	// вытеснением: если воркер занят, копится не очередь, а только самый
	// свежий результат.
	results chan *mtproxyl.CheckResult
	uplinks chan *uplink.Status
	// redraw просит перерисовать сообщение без нового измерения.
	redraw chan struct{}

	baseCtx    context.Context
	pollCancel context.CancelFunc
	pollWG     sync.WaitGroup
	// workerWG держит воркер: при остановке службы надо дать ему дописать
	// начатую правку, иначе в чате останется половина статуса.
	workerWG sync.WaitGroup
	// restartMu держит перезапуски опроса в одну очередь; отдельный от mu,
	// потому что ждать выхода горутины под общим мьютексом нельзя.
	restartMu sync.Mutex
	started   bool
}

func New(deps Deps, state PersistedState) *Bot {
	return &Bot{
		deps:     deps,
		resolver: NewResolver(),
		now:      time.Now,
		state:    state,
		results:  make(chan *mtproxyl.CheckResult, 1),
		uplinks:  make(chan *uplink.Status, 1),
		redraw:   make(chan struct{}, 1),
	}
}

// Start запускает воркера. Опрос Telegram поднимется, как только появится
// рабочая конфигурация, — Reconfigure можно звать и до, и после Start.
func (b *Bot) Start(ctx context.Context) {
	b.mu.Lock()
	if b.started {
		b.mu.Unlock()
		return
	}
	b.started = true
	b.baseCtx = ctx
	b.mu.Unlock()

	b.workerWG.Add(1)
	go func() {
		defer b.workerWG.Done()
		b.worker(ctx)
	}()

	// Именно restartPolling, а не applyConfig: настройки обычно уже применены
	// Reconfigure до старта, и applyConfig счёл бы их неизменными и не поднял
	// бы опрос вовсе — бот молчал бы до первой правки настроек.
	b.restartPolling()
	b.requestRedraw()
}

// Stop ждёт, пока воркер и опрос закончат начатое. Контекст ограничивает
// ожидание: служба не должна висеть на остановке из-за медленного Telegram.
func (b *Bot) Stop(ctx context.Context) {
	done := make(chan struct{})
	go func() {
		b.workerWG.Wait()
		b.pollWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

// OnResult — подписчик проверки доступности. Обязан возвращаться мгновенно:
// его зовут из середины doCheck, и любая задержка здесь тормозила бы и
// плановый цикл, и HTTP-ответ на «Проверить сейчас».
func (b *Bot) OnResult(r *mtproxyl.CheckResult) {
	if r == nil {
		return
	}
	select {
	case b.results <- r:
	default:
		// Воркер ещё возится с прошлым вердиктом. Очередь здесь не нужна:
		// важен только самый свежий результат, старый всё равно был бы
		// перерисован поверх.
		select {
		case <-b.results:
		default:
		}
		select {
		case b.results <- r:
		default:
		}
	}
}

// OnUplink — подписчик наблюдения за связью с дата-центрами. Тот же контракт,
// что у OnResult: возвращается мгновенно, копится только самый свежий вердикт.
func (b *Bot) OnUplink(st *uplink.Status) {
	if st == nil {
		return
	}
	select {
	case b.uplinks <- st:
	default:
		select {
		case <-b.uplinks:
		default:
		}
		select {
		case b.uplinks <- st:
		default:
		}
	}
}

// Reconfigure применяет настройки из панели без перезапуска процесса.
func (b *Bot) Reconfigure(cfg Config) {
	b.applyConfig(cfg)
	b.requestRedraw()
}

func (b *Bot) applyConfig(cfg Config) {
	b.mu.Lock()
	prev := b.cfg
	// Прежний клиент снимается ДО замены: разговаривать со старым чатом надо
	// прежним токеном. Новый токен принадлежит другому боту, и в чужой
	// переписке он не может ни снять команды, ни убрать сообщение.
	oldClient := b.client
	b.cfg = cfg
	if cfg.Token != "" {
		if b.client == nil || prev.Token != cfg.Token {
			b.client = NewClient(cfg.Token)
			b.username = ""
		}
	} else {
		b.client = nil
	}
	started := b.started
	// AdminID здесь наравне с токеном: горутина опроса захватывает его при
	// запуске, и без перезапуска смена админа не дошла бы до проверки прав на
	// кнопках — их продолжал бы принимать прежний.
	sameLoop := prev.Token == cfg.Token && prev.Enabled == cfg.Enabled && prev.AdminID == cfg.AdminID
	// Прежнему админу подсказки команд надо снять: Telegram держит их на чат и
	// сам не забывает, так что иначе у бывшего владельца они остались бы
	// навсегда.
	oldAdmin, ctx := prev.AdminID, b.baseCtx
	moved := oldAdmin != 0 && oldAdmin != cfg.AdminID

	// Сообщения принадлежат прежнему чату, и в новом их id ничего не значит:
	// правка вернула бы «сообщения нет», бот отправил бы новое, а прежнее
	// осталось бы висеть навсегда — убрать его было бы уже некому.
	var oldStatus, oldService int
	if moved {
		oldStatus, oldService = b.state.MessageID, b.state.ServiceMessageID
		b.state.MessageID, b.state.ServiceMessageID = 0, 0
	}
	persist, state := b.deps.Persist, b.state
	b.mu.Unlock()

	if moved && persist != nil {
		if err := persist(state); err != nil {
			log.Printf("[tgbot] не удалось забыть прежнее сообщение: %s", err)
		}
	}

	if !started || sameLoop {
		return
	}
	if moved && oldClient != nil && ctx != nil {
		go func() {
			b.unregisterCommands(ctx, oldClient, oldAdmin)
			if oldStatus != 0 {
				b.retireMessage(ctx, oldClient, oldAdmin, oldStatus)
			}
			if oldService != 0 {
				b.dropMessage(ctx, oldClient, oldAdmin, oldService)
			}
		}()
	}
	b.restartPolling()
}

// restartPolling останавливает прежний опрос и, если есть с чем, поднимает
// новый. Именно останавливает и дожидается: двух getUpdates одним токеном
// Telegram не разрешает и отвечает на это 409.
func (b *Bot) restartPolling() {
	// Перезапуски идут строго по одному. Два одновременных сохранения настроек
	// иначе разошлись бы на pollWG: один успел бы добавить горутину, пока
	// другой уже ждёт нулевого счётчика, — WaitGroup такого не допускает.
	b.restartMu.Lock()
	defer b.restartMu.Unlock()

	b.mu.Lock()
	if b.pollCancel != nil {
		b.pollCancel()
		b.pollCancel = nil
	}
	b.mu.Unlock()

	b.pollWG.Wait()

	b.mu.Lock()
	if b.baseCtx == nil || !b.cfg.Enabled || b.client == nil || b.cfg.AdminID == 0 {
		b.running = false
		// Бот выключили или у него сменился владелец — снимаем подсказки
		// команд, чтобы они не остались висеть у прежнего. Сеть под мьютексом
		// не держим: снятие ничего не ждёт и никого не блокирует.
		client, adminID, ctx := b.client, b.cfg.AdminID, b.baseCtx
		b.mu.Unlock()
		if client != nil && ctx != nil {
			go b.unregisterCommands(ctx, client, adminID)
		}
		return
	}
	defer b.mu.Unlock()
	ctx, cancel := context.WithCancel(b.baseCtx)
	b.pollCancel = cancel
	b.running = true
	client, adminID := b.client, b.cfg.AdminID
	b.pollWG.Add(1)
	go func() {
		defer b.pollWG.Done()
		b.poll(ctx, client, adminID)
	}()
}

// Status отдаёт панели то, что она показывает рядом с формой.
func (b *Bot) Status() Status {
	b.mu.Lock()
	defer b.mu.Unlock()
	return Status{
		Running:     b.running,
		BotUsername: b.username,
		LastError:   b.lastErr,
		MessageID:   b.state.MessageID,
	}
}

// TestConnection проверяет токен и шлёт админу пробное сообщение — кнопка
// «Тест» в панели.
func (b *Bot) TestConnection(ctx context.Context) (string, error) {
	b.mu.Lock()
	client, adminID := b.client, b.cfg.AdminID
	b.mu.Unlock()

	if client == nil {
		return "", ErrNoToken
	}
	me, err := client.GetMe(ctx)
	if err != nil {
		return "", err
	}

	b.mu.Lock()
	b.username = me.Username
	b.mu.Unlock()

	if adminID == 0 {
		return me.Username, ErrNoAdmin
	}
	// Тестовое сообщение отправляем напрямую, а не через служебный слот:
	// панель ждёт от него ошибку, чтобы показать её в форме, — а слот ошибку
	// проглатывает в журнал, потому что для чата она несущественна.
	if _, err := client.SendMessage(ctx, adminID, RenderTestMessage(b.now()), nil, false); err != nil {
		return me.Username, err
	}
	return me.Username, nil
}

// ── Воркер ──────────────────────────────────────────────────────────────────

func (b *Bot) worker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case r := <-b.results:
			b.absorbAvailability(r)
			b.coalesce(ctx)
		case u := <-b.uplinks:
			b.absorbUplink(u)
			b.coalesce(ctx)
		case <-b.redraw:
			b.handleRedraw(ctx)
		}
	}
}

func (b *Bot) requestRedraw() {
	select {
	case b.redraw <- struct{}{}:
	default:
	}
}

// coalesce даёт второму источнику короткое окно, чтобы подоспеть, и только
// потом оценивает всё разом.
//
// Без окна получалось бы так: проверка доступности идёт раз в 15 минут,
// наблюдение за связью — раз в минуту, и каждые пятнадцать минут их будильники
// совпадают. Воркер разобрал бы два результата подряд и прислал два сообщения,
// каждое из которых знает только про свою половину происходящего.
func (b *Bot) coalesce(ctx context.Context) {
	timer := time.NewTimer(coalesceWindow)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case r := <-b.results:
			b.absorbAvailability(r)
		case u := <-b.uplinks:
			b.absorbUplink(u)
		case <-timer.C:
			b.evaluateAndDeliver(ctx)
			return
		}
	}
}

// absorbAvailability запоминает вердикт проверки доступности, не оценивая его.
func (b *Bot) absorbAvailability(r *mtproxyl.CheckResult) {
	if r == nil {
		return
	}
	now := b.now()

	b.mu.Lock()
	defer b.mu.Unlock()

	if r.Error != "" || r.TotalProbes == 0 {
		// Неудача не обнуляет доступность: последний удачный вердикт остаётся
		// на месте, а рядом появляется объяснение, почему свежих цифр нет.
		b.lastFail = &Failure{Reason: r.Error, At: r.CheckedAt}
		if b.lastFail.Reason == "" {
			b.lastFail.Reason = "ни один зонд не ответил"
		}
		if b.lastFail.At.IsZero() {
			b.lastFail.At = now
		}
	} else {
		b.lastGood = r
		b.lastFail = nil
	}
	b.pendingResult = r
}

// absorbUplink запоминает вердикт наблюдения за связью, не оценивая его.
func (b *Bot) absorbUplink(st *uplink.Status) {
	if st == nil {
		return
	}
	b.mu.Lock()
	// Прореживание тихих правок заведено против ежеминутной телеметрии, но оно
	// не должно молчать, когда изменилось то, ради чего это сообщение и
	// читают. Меняющиеся сами по себе RTT и счётчики соединений в расчёт не
	// берутся: иначе исключение стало бы правилом.
	if uplinkChangedMeaningfully(b.lastUplink, st) {
		b.forceRedraw = true
	}
	b.lastUplink = st
	b.pendingUplink = st
	b.mu.Unlock()
}

// uplinkChangedMeaningfully отвечает, стоит ли перерисовать сообщение
// немедленно. Первый вердикт — стоит: до него в блоке было пусто.
func uplinkChangedMeaningfully(prev, next *uplink.Status) bool {
	if prev == nil {
		return true
	}
	switch {
	case prev.Level != next.Level:
		return true
	case prev.AliveWriters != next.AliveWriters:
		return true
	case len(prev.Problems) != len(next.Problems):
		return true
	case prev.Applicable != next.Applicable:
		return true
	case (prev.EngineError == "") != (next.EngineError == ""):
		return true
	case deadDCs(prev.DCs) != deadDCs(next.DCs):
		return true
	}
	return false
}

// deadDCs — сколько дата-центров осталось без живых писателей. Число само по
// себе не тревога (у медиа-дата-центров их штатно нет), но его изменение —
// повод показать свежую картину.
func deadDCs(dcs []uplink.DCRtt) int {
	n := 0
	for _, dc := range dcs {
		if dc.AliveWriters == 0 {
			n++
		}
	}
	return n
}

// evaluateAndDeliver — единственное место, где считаются инциденты и меняется
// сохранённое состояние. Ровно один раз на такт: так гарантируется и одна
// доставка, и отсутствие гонок между двумя источниками.
func (b *Bot) evaluateAndDeliver(ctx context.Context) {
	now := b.now()

	b.mu.Lock()
	hasUplink := b.pendingUplink != nil
	b.mu.Unlock()

	// Внешний адрес выясняется ДО захвата состояния. Это сетевой запрос: при
	// протухшем кэше он обходит несколько сервисов и может занять секунды.
	// Раньше он делался в середине оценки, между снимком состояния и его
	// записью, — и всё, что успевало измениться за это время (например,
	// поставленная в чате пауза), затиралось устаревшим снимком и в таком виде
	// сохранялось на диск. Теперь всё изменение состояния атомарно.
	var ip PublicIPResult
	if hasUplink {
		ip = b.resolver.PublicIPFresh(ctx)
	}

	b.mu.Lock()
	inc := b.state.Incidents
	result, uplinkStatus := b.pendingResult, b.pendingUplink
	b.pendingResult, b.pendingUplink = nil, nil
	threshold := b.cfg.AlertThreshold

	d := delivery{Now: now}
	var av Decision
	var up UplinkDecision
	haveAv, haveUp := false, false

	if result != nil {
		av = Decide(inc.Availability, result, threshold, now)
		inc.Availability = av.State
		haveAv = true
		// Свежий вердикт проверки обязан попасть в сообщение немедленно.
		// Прореживание тихих правок заведено против ежеминутной телеметрии, а
		// проверка идёт раз в 15 минут: попав в чужое окно, её результат
		// потерялся бы, и в чате ещё четверть часа висело бы старое время.
		d.Force = true
	}

	if uplinkStatus != nil {
		up = DecideUplink(inc, uplinkStatus, now)
		inc = up.State
		haveUp = true

		prevIP := inc.LastKnownIP
		known, pending, changed := DecideIPChange(prevIP, b.pendingIP, ip.IP, ip.Fresh)
		b.pendingIP = pending
		inc.LastKnownIP = known
		if changed {
			d.PrevIP, d.NewIP, d.ipChanged = prevIP, known, true
		}
	}

	// Порядок шапок — по важности: упавший движок объясняет всё остальное,
	// поэтому идёт первым, а разовые события — последними.
	if haveUp {
		d.addEngine(up)
	}
	if haveAv {
		d.addAvailability(av)
	}
	if haveUp {
		d.addUplink(up)
		d.addOneShots(up)
	}

	// Пауза глушит только звук. Инциденты считаются и запоминаются как обычно —
	// иначе после снятия паузы бот считал бы аварию новой и разбудил бы ещё раз.
	if inc.Muted(now) {
		d.Loud = false
	} else if !inc.MutedUntil.IsZero() {
		// Пауза истекла сама. Если авария всё ещё идёт, надо сказать вслух:
		// молчание после окончания паузы читается как «всё наладилось».
		inc.MutedUntil = time.Time{}
		if inc.AnyActive() {
			d.Loud = true
			// Со звуком, но без объяснения — худший вид сообщения: человек
			// видит тревогу и не понимает, что именно случилось сейчас.
			d.Banners = append(d.Banners, BannerMuteExpired)
		}
	}

	b.state.Incidents = inc
	persist, state := b.deps.Persist, b.state
	b.mu.Unlock()

	if persist != nil {
		if err := persist(state); err != nil {
			log.Printf("[tgbot] не удалось сохранить состояние: %s", err)
		}
	}

	b.deliver(ctx, d)
}

// delivery — что показать и как: тихой правкой или новым сообщением со звуком.
//
// Шапок может быть несколько, а сообщение всё равно одно: две аварии,
// сорвавшиеся одновременно, не должны приходить двумя уведомлениями.
type delivery struct {
	Loud    bool
	Banners []Banner

	PrevPercentage float64
	PrevKnown      bool
	AlertSince     time.Time
	UplinkSince    time.Time
	EngineSince    time.Time
	PrevIP, NewIP  string
	// ipChanged — внешний адрес сервера сменился; шапка добавляется вместе с
	// остальными разовыми событиями, чтобы порядок был предсказуем.
	ipChanged bool

	// Force — правка нужна немедленно, в обход прореживания: её ждёт человек.
	Force bool
	// Relocate — сообщение надо переотправить вниз чата.
	//
	// Отдельно от Force намеренно. Кнопка «Обновить» висит под самим
	// сообщением: человек уже смотрит на него, и правильно поправить его на
	// месте. А команду /status набирают именно тогда, когда сообщение уехало
	// вверх и его надо поднять.
	Relocate bool
	Now      time.Time
}

func (d *delivery) addAvailability(av Decision) {
	d.PrevPercentage, d.PrevKnown, d.AlertSince = av.Prev, av.PrevKnown, av.AlertSince
	switch av.Event {
	case EventDown:
		d.Banners = append(d.Banners, BannerDown)
		d.Loud = true
	case EventRecovered:
		d.Banners = append(d.Banners, BannerRecovered)
		d.Loud = true
	}
}

// addEngine, addUplink и addOneShots разнесены намеренно: порядок шапок
// значим, а собирается он в вызывающем коде. Упавший движок объясняет и
// падение доступности, и потерю связи, поэтому идёт первым; разовые события
// (перезапуск, смена адреса) — последними, они менее срочные.
func (d *delivery) addEngine(up UplinkDecision) {
	d.EngineSince = up.EngineSince
	switch up.EngineEvent {
	case EventDown:
		d.Banners = append(d.Banners, BannerEngineDown)
		d.Loud = true
	case EventRecovered:
		d.Banners = append(d.Banners, BannerEngineOK)
		d.Loud = true
	}
}

func (d *delivery) addUplink(up UplinkDecision) {
	d.UplinkSince = up.UplinkSince
	switch up.UplinkEvent {
	case EventDown:
		d.Banners = append(d.Banners, BannerUplinkDown)
		d.Loud = true
	case EventRecovered:
		d.Banners = append(d.Banners, BannerUplinkOK)
		d.Loud = true
	}
}

func (d *delivery) addOneShots(up UplinkDecision) {
	if up.Restarted {
		d.Banners = append(d.Banners, BannerEngineRestarted)
		d.Loud = true
	}
	if d.ipChanged {
		d.Banners = append(d.Banners, BannerIPChanged)
		d.Loud = true
	}
}

// handleRedraw перерисовывает сообщение по тому, что уже известно: кнопка
// «Обновить», команда, включение бота, старт панели.
func (b *Bot) handleRedraw(ctx context.Context) {
	b.mu.Lock()
	// Перечитываем последние вердикты из хранилищ, а не полагаемся только на
	// подписку: кнопка «Обновить» и /status обещают показать текущее состояние,
	// и брать его надо у источника.
	if b.deps.Snapshot != nil {
		if snap := b.deps.Snapshot(); snap != nil && snap.Error == "" && snap.TotalProbes > 0 {
			if b.lastGood == nil || snap.CheckedAt.After(b.lastGood.CheckedAt) {
				b.lastGood = snap
			}
		}
	}
	if b.deps.UplinkSnapshot != nil {
		if snap := b.deps.UplinkSnapshot(); snap != nil {
			if b.lastUplink == nil || snap.CheckedAt.After(b.lastUplink.CheckedAt) {
				b.lastUplink = snap
			}
		}
	}
	inc := b.state.Incidents
	force, relocate := b.forceRedraw, b.relocate
	b.forceRedraw, b.relocate = false, false
	b.mu.Unlock()

	b.deliver(ctx, delivery{
		PrevPercentage: inc.Availability.LastPct,
		PrevKnown:      inc.Availability.HasPct,
		AlertSince:     inc.Availability.Since,
		UplinkSince:    inc.Uplink.Since,
		EngineSince:    inc.Engine.Since,
		Force:          force,
		Relocate:       relocate,
		Now:            b.now(),
	})
}

// deliver собирает текст и кладёт его в чат: тихой правкой, если ничего не
// случилось, и новым сообщением, если случилось. Правка не даёт звука —
// потому событию и нужна отправка заново.
func (b *Bot) deliver(ctx context.Context, d delivery) {
	b.mu.Lock()
	client, cfg := b.client, b.cfg
	messageID := b.state.MessageID
	inc := b.state.Incidents
	view := View{
		Result:              b.lastGood,
		Failure:             b.lastFail,
		Uplink:              b.lastUplink,
		AvailabilityEnabled: b.deps.AvailabilityEnabled,
		Quota:               zeroQuota(b.deps.Quota),
		AutoCheck:           b.deps.AutoCheck == nil || b.deps.AutoCheck(),
		Interval:            b.deps.Interval,
		UplinkInterval:      b.deps.UplinkInterval,
		Banners:             d.Banners,
		PrevPercentage:      d.PrevPercentage,
		PrevKnown:           d.PrevKnown,
		AlertSince:          d.AlertSince,
		UplinkSince:         d.UplinkSince,
		EngineSince:         d.EngineSince,
		PrevIP:              d.PrevIP,
		NewIP:               d.NewIP,
		MutedUntil:          inc.MutedUntil,
		MuteForever:         inc.MuteForever,
		Threshold:           cfg.AlertThreshold,
		Now:                 b.now(),
	}
	targetFn := b.deps.Target
	sinceEdit := view.Now.Sub(b.lastEditAt)
	b.mu.Unlock()

	if !cfg.Enabled || client == nil || cfg.AdminID == 0 {
		return
	}

	// Тихие правки — не чаще, чем раз в quietEditInterval. Наблюдение за связью
	// идёт раз в минуту, и в спокойное время сообщение менялось бы только
	// цифрами RTT и отметкой времени — полторы тысячи правок в сутки ради
	// ничего. Событие, команда и первое сообщение ограничение обходят.
	if !d.Loud && !d.Force && messageID != 0 && sinceEdit < quietEditInterval {
		return
	}
	if view.Threshold <= 0 {
		view.Threshold = DefaultThreshold
	}

	// Адрес и IP выясняются здесь, в воркере: это сеть, и в горутине проверки
	// ей делать нечего.
	if targetFn != nil {
		if target, err := targetFn(ctx); err == nil {
			view.Target = target
			view.Host = b.resolver.Describe(ctx, target.Host)
		}
	}

	text := RenderStatus(view)
	kb := keyboard()

	// Тихая правка — когда события не было. Событие требует звука, а правка
	// его не даёт, поэтому там сообщение отправляется заново.
	//
	// Отдельный случай — Force без события: команда «покажи статус». Она тоже
	// переотправляет сообщение, но без звука: человек попросил показать статус,
	// и он должен оказаться внизу чата, а не остаться где-то выше в истории.
	if !d.Loud && !d.Relocate && messageID != 0 {
		err := client.EditMessageText(ctx, cfg.AdminID, messageID, text, kb)
		if err == nil {
			b.noteEdit()
			b.setLastError("")
			return
		}
		if apiErr, ok := asAPIError(err); ok {
			if apiErr.IsNotModified() {
				// Вердикт повторился слово в слово — это норма, а не сбой.
				b.noteEdit()
				b.setLastError("")
				return
			}
			if !apiErr.IsMessageGone() {
				b.reportError("правка сообщения", err)
				return
			}
			// Сообщения больше нет — отправляем новое ниже.
			messageID = 0
		} else {
			b.reportError("правка сообщения", err)
			return
		}
	}

	// Почему сообщение уходит новым, а не правится, — в журнал. В прошлый раз
	// причину пришлось восстанавливать по косвенным признакам из текста самих
	// сообщений, и это само по себе недоработка.
	log.Printf("[tgbot] отправляю новое сообщение: %s", resendReason(d, messageID))

	// Звук — только у настоящих событий. Ответ на команду переезжает вниз чата
	// молча: человек и так смотрит на экран.
	sent, err := client.SendMessage(ctx, cfg.AdminID, text, kb, !d.Loud)
	if err != nil {
		b.reportError("отправка сообщения", err)
		return
	}
	b.noteEdit()
	b.setLastError("")

	b.mu.Lock()
	b.state.MessageID = sent.MessageID
	persist, state := b.deps.Persist, b.state
	b.mu.Unlock()
	if persist != nil {
		if err := persist(state); err != nil {
			log.Printf("[tgbot] не удалось сохранить id сообщения: %s", err)
		}
	}

	// Старое убираем только теперь: если бы удаляли первым, сорвавшаяся
	// отправка оставила бы чат вообще без статуса.
	if messageID != 0 && messageID != sent.MessageID {
		b.retireMessage(ctx, client, cfg.AdminID, messageID)
	}
	// Заодно уносим служебный ответ: свежий статус только что пришёл вниз
	// чата, и прошлое подтверждение выше по истории уже ничего не добавляет.
	b.clearService(ctx, client, cfg.AdminID)
}

// resendReason называет причину переотправки словами.
func resendReason(d delivery, messageID int) string {
	switch {
	case messageID == 0:
		return "прежнего сообщения нет — первое после запуска, либо его удалили"
	case d.Loud:
		return "событие: тревога или её окончание"
	case d.Relocate:
		return "команда: статус переезжает вниз чата"
	default:
		return "правка не прошла"
	}
}

// retireMessage убирает прежнее живое сообщение. Telegram не даёт боту удалять
// свои сообщения старше 48 часов — а панель вполне может проработать двое
// суток без единого события. Что не удалилось, ужимаем в одну строку, чтобы в
// чате не оставалось второго «статуса» с устаревшими цифрами.
func (b *Bot) retireMessage(ctx context.Context, client *Client, chatID int64, messageID int) {
	if err := client.DeleteMessage(ctx, chatID, messageID); err == nil {
		return
	}
	if err := client.EditMessageText(ctx, chatID, messageID, tombstone, nil); err != nil {
		if apiErr, ok := asAPIError(err); ok && (apiErr.IsMessageGone() || apiErr.IsNotModified()) {
			return
		}
		log.Printf("[tgbot] прежнее сообщение осталось в чате: %s", err)
	}
}

// sendService отправляет служебный ответ — справку, подтверждение паузы,
// отказ — и убирает предыдущий такой же. Служебных сообщений в чате всегда не
// больше одного: они нужны ровно в момент прочтения, а копятся навсегда.
//
// Порядок «сначала отправить, потом удалить прежнее» тот же, что у статуса:
// сорвавшаяся отправка не должна оставить человека вообще без ответа.
func (b *Bot) sendService(ctx context.Context, client *Client, chatID int64, text string, kb any, silent bool) {
	sent, err := client.SendMessage(ctx, chatID, text, kb, silent)
	if err != nil {
		log.Printf("[tgbot] служебное сообщение не ушло: %s", err)
		return
	}

	b.mu.Lock()
	prev := b.state.ServiceMessageID
	b.state.ServiceMessageID = sent.MessageID
	persist, state := b.deps.Persist, b.state
	b.mu.Unlock()

	if prev != 0 && prev != sent.MessageID {
		b.dropMessage(ctx, client, chatID, prev)
	}
	if persist != nil {
		if err := persist(state); err != nil {
			log.Printf("[tgbot] не удалось сохранить состояние: %s", err)
		}
	}
}

// clearService убирает служебный ответ, если он ещё висит. Зовётся, когда
// статус переезжает вниз чата: человек и так смотрит на свежие цифры, а
// прошлое подтверждение выше по истории только мешает.
func (b *Bot) clearService(ctx context.Context, client *Client, chatID int64) {
	b.mu.Lock()
	id := b.state.ServiceMessageID
	b.state.ServiceMessageID = 0
	b.mu.Unlock()

	if id != 0 {
		b.dropMessage(ctx, client, chatID, id)
	}
}

// dropMessage удаляет сообщение и молчит о том, что удалять было уже нечего:
// человек мог убрать его сам, и это не повод для записи в журнал.
func (b *Bot) dropMessage(ctx context.Context, client *Client, chatID int64, messageID int) {
	if err := client.DeleteMessage(ctx, chatID, messageID); err != nil {
		if apiErr, ok := asAPIError(err); ok && apiErr.IsMessageGone() {
			return
		}
		log.Printf("[tgbot] сообщение осталось в чате: %s", err)
	}
}

func keyboard() *InlineKeyboardMarkup {
	return &InlineKeyboardMarkup{InlineKeyboard: [][]InlineKeyboardButton{{
		{Text: "🔄 Проверить сейчас", CallbackData: callbackCheck},
		{Text: "↻ Обновить", CallbackData: callbackRefresh},
	}}}
}

func zeroQuota(fn func() mtproxyl.QuotaState) mtproxyl.QuotaState {
	if fn == nil {
		return mtproxyl.QuotaState{}
	}
	return fn()
}

// noteEdit запоминает время последней правки — по нему считается пауза между
// тихими правками.
func (b *Bot) noteEdit() {
	b.mu.Lock()
	b.lastEditAt = b.now()
	b.mu.Unlock()
}

func (b *Bot) setLastError(msg string) {
	b.mu.Lock()
	b.lastErr = msg
	b.mu.Unlock()
}

func (b *Bot) reportError(what string, err error) {
	msg := err.Error()
	if apiErr, ok := asAPIError(err); ok && apiErr.IsChatNotFound() {
		msg = "админ ещё не открывал диалог с ботом — напишите ему /start"
	}
	b.setLastError(what + ": " + msg)
	log.Printf("[tgbot] %s: %s", what, msg)
}

// ── Опрос ───────────────────────────────────────────────────────────────────

func (b *Bot) poll(ctx context.Context, client *Client, adminID int64) {
	if me, err := client.GetMe(ctx); err == nil {
		b.mu.Lock()
		b.username = me.Username
		b.mu.Unlock()
	}
	b.registerCommands(ctx, client, adminID)

	offset := 0
	backoff := pollBackoffMin

	for {
		if ctx.Err() != nil {
			return
		}
		updates, err := client.GetUpdates(ctx, offset)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if apiErr, ok := asAPIError(err); ok {
				switch {
				case apiErr.IsUnauthorized():
					// Дальше опрашивать нечем: токен отозван или неверен.
					b.setLastError("токен бота отклонён Telegram — проверьте его в @BotFather")
					b.mu.Lock()
					b.running = false
					b.mu.Unlock()
					return
				case apiErr.IsConflict():
					b.setLastError("этого бота уже опрашивает другой процесс — токен занят второй панелью")
				case apiErr.RetryAfter > 0:
					backoff = apiErr.RetryAfter
				default:
					b.setLastError("опрос Telegram: " + apiErr.Description)
				}
			} else {
				b.setLastError("опрос Telegram: " + err.Error())
			}

			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff *= 2; backoff > pollBackoffMax {
				backoff = pollBackoffMax
			}
			continue
		}

		backoff = pollBackoffMin
		b.setLastError("")
		for _, u := range updates {
			if u.UpdateID >= offset {
				offset = u.UpdateID + 1
			}
			b.handleUpdate(ctx, client, adminID, u)
		}
	}
}

func (b *Bot) handleUpdate(ctx context.Context, client *Client, adminID int64, u Update) {
	switch {
	case u.Message != nil:
		b.handleMessage(ctx, client, adminID, u.Message)
	case u.CallbackQuery != nil:
		b.handleCallback(ctx, client, adminID, u.CallbackQuery)
	}
}

func (b *Bot) handleMessage(ctx context.Context, client *Client, adminID int64, m *Message) {
	cmd := normalizeCommand(m.Text)
	if cmd == "" || !strings.HasPrefix(cmd, "/") {
		// Обычный текст админ пишет боту сам и осознанно — стирать его не наше
		// дело. Убираем только то, что человек нажал ради действия.
		return
	}

	// На /start отвечаем всем, пока админ не задан: иначе оператору неоткуда
	// узнать свой ID — Telegram его нигде не показывает. Как только ID
	// известен, посторонние получают отказ на любую команду.
	if adminID != 0 && m.Chat.ID != adminID {
		_, _ = client.SendMessage(ctx, m.Chat.ID, "Этот бот обслуживает чужую панель.", nil, true)
		return
	}
	if adminID == 0 && cmd != cmdStart {
		return
	}

	// Само нажатие убираем: кнопки клавиатуры шлют обычный текст, и без этого
	// чат зарастает лентой из «Статус» и «Проверить». Bot API разрешает боту
	// удалять входящие в личной переписке — но только моложе 48 часов, поэтому
	// делаем это сразу, а не когда-нибудь потом.
	b.dropMessage(ctx, client, m.Chat.ID, m.MessageID)

	b.handleCommand(ctx, client, m.Chat.ID, cmd)
}

// replyStart отвечает на /start и заодно ставит постоянную клавиатуру.
func (b *Bot) replyStart(ctx context.Context, client *Client, chatID int64) {
	b.mu.Lock()
	adminID := b.cfg.AdminID
	b.mu.Unlock()

	known := chatID == adminID
	var kb any
	if known {
		// Клавиатура — только своему: постороннему она ни к чему.
		kb = replyKeyboard()
	}
	if _, err := client.SendMessage(ctx, chatID, RenderStartReply(chatID, known), kb, false); err != nil {
		log.Printf("[tgbot] ответ на /start не ушёл: %s", err)
		return
	}
	if known {
		// Теперь чат точно существует — можно сузить подсказки команд до него.
		b.registerCommands(ctx, client, adminID)
		b.refreshStatus()
	}
}

func (b *Bot) handleCallback(ctx context.Context, client *Client, adminID int64, q *CallbackQuery) {
	if q.From.ID != adminID {
		_ = client.AnswerCallbackQuery(ctx, q.ID, "Эта кнопка не для вас.", true)
		return
	}

	switch q.Data {
	case callbackRefresh:
		_ = client.AnswerCallbackQuery(ctx, q.ID, "Обновляю по последним данным", false)
		// Правим на месте: кнопка висит под этим же сообщением, человек уже
		// смотрит на него — переносить его вниз чата незачем.
		b.refreshStatus()

	case callbackCheck:
		if b.deps.RunCheckNow == nil {
			_ = client.AnswerCallbackQuery(ctx, q.ID, "Проверка недоступна.", true)
			return
		}
		// Кулдаун и исчерпанная квота отвечают мгновенно, настоящее измерение
		// идёт десятки секунд. Ждём короткое окно: успели получить отказ —
		// показываем его всплывашкой, не успели — говорим «проверяю», а
		// результат придёт обычным путём и перерисует сообщение сам.
		done := make(chan error, 1)
		go func() {
			_, err := b.deps.RunCheckNow(ctx)
			done <- err
		}()
		select {
		case err := <-done:
			if err != nil {
				_ = client.AnswerCallbackQuery(ctx, q.ID, err.Error(), true)
				return
			}
			_ = client.AnswerCallbackQuery(ctx, q.ID, "Готово", false)
		case <-time.After(fastFailWindow):
			_ = client.AnswerCallbackQuery(ctx, q.ID, "Проверяю — сообщение обновится само", false)
			go func() {
				if err := <-done; err != nil {
					log.Printf("[tgbot] проверка по кнопке не удалась: %s", err)
				}
			}()
		}

	case callbackMute30:
		_ = client.AnswerCallbackQuery(ctx, q.ID, "Пауза на 30 минут", false)
		b.setMute(ctx, client, q.From.ID, 30*time.Minute, false)

	case callbackMute2h:
		_ = client.AnswerCallbackQuery(ctx, q.ID, "Пауза на 2 часа", false)
		b.setMute(ctx, client, q.From.ID, 2*time.Hour, false)

	case callbackMuteOn:
		_ = client.AnswerCallbackQuery(ctx, q.ID, "Пауза до отмены", false)
		b.setMute(ctx, client, q.From.ID, 0, true)

	default:
		_ = client.AnswerCallbackQuery(ctx, q.ID, "", false)
	}
}
