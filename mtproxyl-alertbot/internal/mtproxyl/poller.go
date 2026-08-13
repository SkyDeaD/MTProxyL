package mtproxyl

import (
	"context"
	"log"
	"sync"
	"time"
)

// DefaultPollInterval — как часто спрашивать скрипт о вердикте.
//
// Это не проверка, а чтение готового файла: квота не тратится, зонды не
// беспокоятся. Минута выбрана в пару к наблюдению за связью с дата-центрами —
// тогда оба блока сообщения обновляются одним тактом, а не вразнобой.
const DefaultPollInterval = time.Minute

// Poller следит за вердиктом и зовёт подписчика, когда тот сменился.
//
// Прежде панель считала проверку сама и объявляла результат прямо в памяти.
// Теперь считает скрипт по своему таймеру, и узнать о новом вердикте можно
// только спросив. Признак новизны — checked_at: скрипт ставит его в момент
// измерения, поэтому одна и та же метка означает один и тот же вердикт, даже
// если файл перезаписали.
type Poller struct {
	client   *Client
	interval time.Duration

	mu       sync.Mutex
	last     *State
	lastAt   time.Time
	lastErr  string
	onResult func(*State)
}

func NewPoller(client *Client, interval time.Duration) *Poller {
	if interval <= 0 {
		interval = DefaultPollInterval
	}
	return &Poller{client: client, interval: interval}
}

// SetOnResult ставит подписчика. Вызов обязан возвращаться сразу: опрос не
// должен ждать чужую сеть.
func (p *Poller) SetOnResult(fn func(*State)) {
	p.mu.Lock()
	p.onResult = fn
	p.mu.Unlock()
}

// Snapshot — последнее, что удалось прочитать.
func (p *Poller) Snapshot() *State {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.last
}

func (p *Poller) Start(ctx context.Context) {
	go func() {
		p.Poll(ctx)

		t := time.NewTicker(p.interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				p.Poll(ctx)
			}
		}
	}()
}

// Poll читает состояние один раз и, если вердикт новый, будит подписчика.
func (p *Poller) Poll(ctx context.Context) *State {
	st, err := p.client.Details(ctx)
	if err != nil {
		// Молчим о повторе той же беды: опрос идёт каждую минуту, и один и тот
		// же текст в журнале круглосуточно никто читать не станет.
		p.complain(err)
		return p.Snapshot()
	}

	p.mu.Lock()
	fresh := st.Result != nil && !st.Result.CheckedAt.Equal(p.lastAt)
	if st.Result != nil {
		p.lastAt = st.Result.CheckedAt
	}
	p.last = st
	fn := p.onResult
	p.mu.Unlock()

	if fresh && fn != nil {
		fn(st)
	}
	return st
}

func (p *Poller) complain(err error) {
	p.mu.Lock()
	same := p.lastErr == err.Error()
	p.lastErr = err.Error()
	p.mu.Unlock()
	if !same {
		log.Printf("[mtproxyl] вердикт доступности не прочитан: %s", err)
	}
}
