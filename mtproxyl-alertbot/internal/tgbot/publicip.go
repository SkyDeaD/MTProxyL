package tgbot

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

// ipServices опрашиваются по очереди до первого ответа. Их несколько потому,
// что любой из них регулярно недоступен с конкретного хоста, а неизвестный
// адрес — это пропавшая строка в сообщении и мёртвый детектор переезда.
var ipServices = []string{
	"https://api.ipify.org",
	"https://ifconfig.me/ip",
	"https://icanhazip.com",
	"https://ipecho.net/plain",
}

// PublicIP спрашивает у внешней службы, каким видят адрес этого сервера.
//
// Намеренно без кэша внутри: смену адреса (переезд на новый VPS, плавающий IP)
// надо заметить — это и есть повод для отдельного оповещения. Кэширует уже
// Resolver, и он же отличает свежий ответ от прошлого: событие поднимается
// только по свежему.
func PublicIP(ctx context.Context) (string, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	for _, url := range ipServices {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			continue
		}
		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			continue
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 64))
		_ = resp.Body.Close()
		if err != nil {
			continue
		}
		if ip := strings.TrimSpace(string(body)); ip != "" {
			return ip, nil
		}
	}
	return "", errors.New("ни один сервис определения внешнего IP не ответил")
}
