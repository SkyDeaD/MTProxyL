package mtproxyl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Права у бота урезаны до трёх подкоманд чтения (см. sudoers), поэтому вызов
// не может ни запустить прокси, ни тронуть пользователей: сетевой процесс не
// должен уметь больше, чем ему нужно для рассказа о состоянии.
const (
	// statusTimeout — чтение готового вердикта с диска, это быстро.
	statusTimeout = 20 * time.Second
	// checkTimeout — настоящее измерение: зонды опрашиваются до минуты, плюс
	// запас на медленный ответ Globalping.
	checkTimeout = 150 * time.Second
)

// Rejected — отказ, который не является поломкой: квота выбрана или прошлая
// проверка была только что. Текст берём у скрипта, чтобы человек читал ту же
// формулировку, что и в меню.
type Rejected struct{ Message string }

func (e *Rejected) Error() string { return e.Message }

// ErrUnsupported — установленный MTProxyL старше 1.4.8 и проверки не знает.
// Бот и скрипт обновляются порознь, так что это обычное дело, а не сбой.
var ErrUnsupported = errors.New("установленный MTProxyL не умеет проверять доступность")

// Client зовёт CLI MTProxyL.
type Client struct {
	script string
	sudo   bool
}

func NewClient(script string, sudo bool) *Client {
	if script == "" {
		script = "/opt/mtproxyl/mtproxyl.sh"
	}
	return &Client{script: script, sudo: sudo}
}

// Status — последний вердикт без списка зондов.
func (c *Client) Status(ctx context.Context) (*State, error) {
	return c.state(ctx, statusTimeout, "availability", "status", "--json")
}

// Details — то же, но с каждым зондом. Именно его показывает бот: список
// зондов и есть половина смысла сообщения.
func (c *Client) Details(ctx context.Context) (*State, error) {
	return c.state(ctx, statusTimeout, "availability", "details")
}

// Check запускает настоящее измерение. Отказ по квоте или кулдауну приходит
// как *Rejected — это ответ, а не сбой, и человеку его надо показать словами.
func (c *Client) Check(ctx context.Context) (*CheckResult, error) {
	out, err := c.run(ctx, checkTimeout, "availability", "check", "--json")
	line := firstJSONLine(out)
	if err != nil {
		if unsupported(out) {
			return nil, ErrUnsupported
		}
		// Скрипт отвечает отказом через тот же JSON, только с кодом возврата 1.
		if msg := rejection(line); msg != "" {
			return nil, &Rejected{Message: msg}
		}
		return nil, err
	}
	if msg := rejection(line); msg != "" {
		return nil, &Rejected{Message: msg}
	}

	var res CheckResult
	if err := json.Unmarshal([]byte(line), &res); err != nil {
		return nil, fmt.Errorf("проверка вернула неразборчивый ответ: %w", err)
	}
	return &res, nil
}

func (c *Client) state(ctx context.Context, timeout time.Duration, args ...string) (*State, error) {
	out, err := c.run(ctx, timeout, args...)
	if err != nil {
		if unsupported(out) {
			return nil, ErrUnsupported
		}
		return nil, err
	}
	var st State
	if err := json.Unmarshal([]byte(firstJSONLine(out)), &st); err != nil {
		return nil, fmt.Errorf("состояние проверки не разобрано: %w", err)
	}
	return &st, nil
}

func (c *Client) run(ctx context.Context, timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	name, full := c.script, args
	if c.sudo {
		// -n: никогда не спрашивать пароль. Без этого кривой sudoers подвесил
		// бы вызов до самого таймаута вместо честной ошибки.
		name, full = "sudo", append([]string{"-n", c.script}, args...)
	}

	cmd := exec.CommandContext(ctx, name, full...)
	// Окружение не наследуем: своё скрипту не нужно, а унаследованный PATH
	// однажды меняет поведение так, что это не воспроизвести.
	cmd.Env = []string{
		"MTPROXYL_ASSUME_YES=1",
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return stdout.String(), fmt.Errorf("mtproxyl %s: не ответил за %s", strings.Join(args, " "), timeout)
		}
		detail := lastMeaningfulLine(stderr.String(), stdout.String())
		if detail == "" {
			detail = err.Error()
		}
		return stdout.String(), fmt.Errorf("mtproxyl %s: %s", strings.Join(args, " "), detail)
	}
	return stdout.String(), nil
}

// firstJSONLine достаёт объект из вывода: скрипт может напечатать перед ним
// предупреждение, и это не повод считать ответ негодным.
func firstJSONLine(out string) string {
	for line := range strings.SplitSeq(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "{") && strings.HasSuffix(line, "}") {
			return line
		}
	}
	return ""
}

// rejection достаёт причину отказа: у отказа в ответе есть «error» и нет
// ничего другого — вердикт с полями пришёл бы вместе с процентом и зондами.
func rejection(line string) string {
	if line == "" {
		return ""
	}
	var probe struct {
		Error       string `json:"error"`
		TotalProbes *int   `json:"total_probes"`
	}
	if err := json.Unmarshal([]byte(line), &probe); err != nil {
		return ""
	}
	if probe.TotalProbes != nil {
		return ""
	}
	return probe.Error
}

func unsupported(out string) bool {
	return strings.Contains(out, "Неизвестная команда")
}

func lastMeaningfulLine(sources ...string) string {
	for _, src := range sources {
		lines := strings.Split(strings.TrimSpace(src), "\n")
		for i := len(lines) - 1; i >= 0; i-- {
			if line := strings.TrimSpace(lines[i]); line != "" {
				return line
			}
		}
	}
	return ""
}
