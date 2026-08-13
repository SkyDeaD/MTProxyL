// mtproxyl-alertbot — сторож прокси в телеграме.
//
// Он не управляет прокси и намеренно не умеет этого: у MTProxyL для этого есть
// свой бот. Задача сторожа одна — держать в чате единственное живое сообщение
// о том, работает ли прокси, и будить звуком, когда перестал.
//
// Смотрит он в две стороны сразу, и это главное. «Доступность из России»
// отвечает на вопрос, видят ли прокси клиенты, — это вход. Но фильтрация у
// российского хостера чаще режет выход: снаружи сервер доступен, зонды
// показывают прежние 95%, а прокси уже не может писать в дата-центры Telegram,
// и клиенты не работают. Поэтому вторым блоком идёт связь с дата-центрами,
// снятая прямо у движка.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	// Встроенная база часовых поясов. Без неё time.LoadLocation зависит от
	// файлов на диске, а их может не быть: в минимальном образе tzdata нет, и
	// любая зона молча превращалась бы в UTC.
	_ "time/tzdata"

	"github.com/Liafanx/mtproxyl-alertbot/internal/config"
	"github.com/Liafanx/mtproxyl-alertbot/internal/mtproxyl"
	"github.com/Liafanx/mtproxyl-alertbot/internal/tgbot"
	"github.com/Liafanx/mtproxyl-alertbot/internal/uplink"
)

var version = "1.0.0"

func main() {
	configPath := flag.String("config", config.DefaultPath, "путь к файлу настроек")
	showVersion := flag.Bool("version", false, "показать версию и выйти")
	flag.Parse()

	if *showVersion {
		fmt.Println("mtproxyl-alertbot", version)
		return
	}

	// Подкоманда config — единственный способ поправить настройки там, где нет
	// ни панели, ни меню MTProxyL: сторож ставится и поверх оригинала.
	if args := flag.Args(); len(args) > 0 && args[0] == "config" {
		os.Exit(runConfig(*configPath, args[1:]))
	}

	log.SetFlags(log.LstdFlags)
	log.Printf("[alertbot] версия %s", version)

	store := config.NewStore(*configPath)
	if err := store.Load(); err != nil {
		// Битый файл — повод сказать и продолжить: бот поднимется без настроек
		// и будет ждать, пока их зададут, а не уйдёт в цикл перезапусков.
		log.Printf("[alertbot] %s", err)
	}
	cfg := store.Get()

	if cfg.Token == "" || cfg.ChatID == 0 {
		log.Println("[alertbot] бот не настроен: нет токена или адреса чата — жду настроек")
	}

	tgbot.SetTimezone(cfg.Timezone)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Вердикт доступности считает сам MTProxyL по своему таймеру: у него это
	// живёт дольше нашего процесса и не тратит вторую квоту Globalping.
	cli := mtproxyl.NewClient(cfg.Script, cfg.UseSudo())
	poller := mtproxyl.NewPoller(cli, mtproxyl.DefaultPollInterval)

	// Связь с дата-центрами — наше собственное наблюдение: данные локальные,
	// квот нет, поэтому обрыв виден за минуту, а не за пятнадцать.
	watcher := uplink.NewWatcher(
		uplink.NewClient(cfg.TelemtURL, cfg.TelemtAuthHeader),
		uplink.DefaultInterval,
		func() float64 { return cfg.ConnectFailThreshold },
	)

	bot := tgbot.New(deps(store, cli, poller, watcher), persistedState(cfg))
	bot.Reconfigure(tgbot.Config{
		Enabled:        true,
		Token:          cfg.Token,
		AdminID:        cfg.ChatID,
		AlertThreshold: cfg.AlertThreshold,
	})

	// Подписки ставятся до старта наблюдателей: первый же вердикт должен
	// попасть в чат, а не потеряться из-за того, что бот ещё не слушал.
	poller.SetOnResult(func(st *mtproxyl.State) { bot.OnResult(st.Result) })
	watcher.SetOnResult(bot.OnUplink)

	bot.Start(ctx)
	poller.Start(ctx)
	go watcher.Start(ctx)

	<-ctx.Done()
	log.Println("[alertbot] останавливаюсь")

	// Даём воркеру дописать начатое сообщение: оборвать его посреди правки
	// значит оставить в чате половину статуса.
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	bot.Stop(shutdown)
}

func deps(store *config.Store, cli *mtproxyl.Client, poller *mtproxyl.Poller, watcher *uplink.Watcher) tgbot.Deps {
	return tgbot.Deps{
		RunCheckNow: cli.Check,
		Snapshot: func() *mtproxyl.CheckResult {
			if st := poller.Snapshot(); st != nil {
				return st.Result
			}
			return nil
		},
		Quota: func() mtproxyl.QuotaState {
			if st := poller.Snapshot(); st != nil {
				return st.Quota
			}
			return mtproxyl.QuotaState{}
		},
		AutoCheck: func() bool {
			st := poller.Snapshot()
			return st == nil || st.AutoCheck
		},
		Target: func(ctx context.Context) (mtproxyl.Target, error) {
			st := poller.Snapshot()
			if st == nil {
				return mtproxyl.Target{}, fmt.Errorf("состояние проверки ещё не прочитано")
			}
			return st.Target, nil
		},
		Interval:            interval(poller),
		UplinkSnapshot:      watcher.Snapshot,
		UplinkInterval:      uplink.DefaultInterval,
		AvailabilityEnabled: true,
		Persist:             persist(store),
	}
}

// interval — как часто MTProxyL проверяет доступность. Значение приходит из
// настроек скрипта, поэтому спрашиваем его, а не держим своё.
func interval(poller *mtproxyl.Poller) time.Duration {
	if st := poller.Snapshot(); st != nil && st.Interval > 0 {
		return time.Duration(st.Interval) * time.Minute
	}
	return 15 * time.Minute
}

func persist(store *config.Store) func(tgbot.PersistedState) error {
	return func(st tgbot.PersistedState) error {
		cfg := store.Get()
		cfg.StatusMessageID = st.MessageID
		cfg.ServiceMessageID = st.ServiceMessageID

		inc := st.Incidents
		cfg.AlertActive = inc.Availability.Active
		cfg.AlertSinceUnix = config.Unix(inc.Availability.Since)
		cfg.LastAlertUnix = config.Unix(inc.Availability.LastNotify)
		cfg.LastPercentage = inc.Availability.LastPct
		cfg.HasPercentage = inc.Availability.HasPct

		cfg.UplinkAlertActive = inc.Uplink.Active
		cfg.UplinkAlertSinceUnix = config.Unix(inc.Uplink.Since)
		cfg.UplinkLastAlertUnix = config.Unix(inc.Uplink.LastNotify)

		cfg.EngineAlertActive = inc.Engine.Active
		cfg.EngineAlertSinceUnix = config.Unix(inc.Engine.Since)
		cfg.EngineLastAlertUnix = config.Unix(inc.Engine.LastNotify)

		cfg.LastKnownIP = inc.LastKnownIP
		cfg.MutedUntilUnix = config.Unix(inc.MutedUntil)
		cfg.MuteForever = inc.MuteForever

		return store.Save(cfg)
	}
}

func persistedState(cfg config.Config) tgbot.PersistedState {
	st := tgbot.PersistedState{
		MessageID:        cfg.StatusMessageID,
		ServiceMessageID: cfg.ServiceMessageID,
		Incidents: tgbot.Incidents{
			Availability: tgbot.AlertState{
				Active:  cfg.AlertActive,
				LastPct: cfg.LastPercentage,
				HasPct:  cfg.HasPercentage,
			},
			Uplink:      tgbot.IncidentState{Active: cfg.UplinkAlertActive},
			Engine:      tgbot.IncidentState{Active: cfg.EngineAlertActive},
			LastKnownIP: cfg.LastKnownIP,
			MuteForever: cfg.MuteForever,
		},
	}
	st.Incidents.Availability.Since = config.FromUnix(cfg.AlertSinceUnix)
	st.Incidents.Availability.LastNotify = config.FromUnix(cfg.LastAlertUnix)
	st.Incidents.Uplink.Since = config.FromUnix(cfg.UplinkAlertSinceUnix)
	st.Incidents.Uplink.LastNotify = config.FromUnix(cfg.UplinkLastAlertUnix)
	st.Incidents.Engine.Since = config.FromUnix(cfg.EngineAlertSinceUnix)
	st.Incidents.Engine.LastNotify = config.FromUnix(cfg.EngineLastAlertUnix)
	st.Incidents.MutedUntil = config.FromUnix(cfg.MutedUntilUnix)
	return st
}
