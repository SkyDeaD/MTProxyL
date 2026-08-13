package tgbot

import (
	"os"
	"strings"
	"sync"
	"time"
)

// tzRecheck — как часто перечитывать часовой пояс системы.
//
// Go определяет time.Local один раз за процесс, поэтому смена пояса на сервере
// работающей панелью не подхватывается: пока её не перезапустят, бот пишет
// время в прежней зоне. Панель этим не страдает — она отдаёт метку в UTC, а
// форматирует её браузер, — и расхождение между сообщением и интерфейсом
// выглядит поломкой. Перечитываем сами.
const tzRecheck = 5 * time.Minute

var (
	tzMu      sync.Mutex
	tzCached  *time.Location
	tzCheckAt time.Time
	// tzOverride — зона, заданная оператором в панели. Пустая строка значит
	// «определять самому». Нужна из-за контейнера: панель в Docker видит зону
	// контейнера, а не хоста, и автоопределение там принципиально не может
	// дать нужный ответ.
	tzOverride string
	tzNow      = time.Now // подменяется в тестах
	// tzSources — где смотреть имя зоны. Порядок важен: переменная окружения
	// перекрывает системную настройку, как и для всего остального в Go.
	tzEnv       = os.Getenv
	tzReadlink  = os.Readlink
	tzLoad      = time.LoadLocation
	tzLocalHint = "/etc/localtime"
)

// SetTimezone задаёт зону, выбранную оператором. Пустая строка возвращает
// автоопределение.
func SetTimezone(name string) {
	tzMu.Lock()
	tzOverride = strings.TrimSpace(name)
	tzCached, tzCheckAt = nil, time.Time{}
	tzMu.Unlock()
}

// ValidTimezone проверяет имя зоны, чтобы опечатка не превратилась молча в UTC.
func ValidTimezone(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return true // пусто — это «определять самому», тоже допустимо
	}
	_, err := tzLoad(name)
	return err == nil
}

// localZone возвращает текущий часовой пояс, перечитывая его не чаще раза в
// tzRecheck.
func localZone() *time.Location {
	tzMu.Lock()
	defer tzMu.Unlock()

	now := tzNow()
	if tzCached != nil && now.Sub(tzCheckAt) < tzRecheck {
		return tzCached
	}
	tzCheckAt = now

	if loc := loadZone(); loc != nil {
		tzCached = loc
	} else if tzCached == nil {
		// Ничего не вышло — остаёмся на том, что определил рантайм при старте.
		tzCached = time.Local
	}
	return tzCached
}

// loadZone. Порядок: заданное оператором, потом TZ, потом система.
// Вызывается под tzMu.
func loadZone() *time.Location {
	if tzOverride != "" {
		if loc, err := tzLoad(tzOverride); err == nil {
			return loc
		}
	}
	if name := strings.TrimSpace(tzEnv("TZ")); name != "" {
		if loc, err := tzLoad(name); err == nil {
			return loc
		}
	}
	// /etc/localtime обычно симлинк вида /usr/share/zoneinfo/Asia/Tashkent —
	// имя зоны достаётся из хвоста пути.
	target, err := tzReadlink(tzLocalHint)
	if err != nil {
		return nil
	}
	if i := strings.Index(target, "zoneinfo/"); i >= 0 {
		if loc, err := tzLoad(target[i+len("zoneinfo/"):]); err == nil {
			return loc
		}
	}
	return nil
}

// resetZoneCache нужен тестам: между случаями кэш не должен протекать.
func resetZoneCache() {
	tzMu.Lock()
	tzCached, tzCheckAt, tzOverride = nil, time.Time{}, ""
	tzMu.Unlock()
}
