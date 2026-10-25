package spotify

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// UserAgent identifica o spotdown nas requisições (definido pelo main com a versão).
var UserAgent = "spotdown/dev (+https://github.com/vinicgobbi/spotdown)"

// limiter espaça as requisições a um host (no máximo uma a cada "every") e,
// quando o servidor responde 429, pausa todas as goroutines de uma vez em vez
// de cada uma insistir por conta própria.
type limiter struct {
	mu    sync.Mutex
	next  time.Time
	every time.Duration
}

func newLimiter(every time.Duration) *limiter { return &limiter{every: every} }

func (l *limiter) wait(ctx context.Context) error {
	l.mu.Lock()
	now := time.Now()
	at := l.next
	if at.Before(now) {
		at = now
	}
	l.next = at.Add(l.every)
	l.mu.Unlock()

	d := time.Until(at)
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// pause adia a próxima requisição de todos por d.
func (l *limiter) pause(d time.Duration) {
	l.mu.Lock()
	if until := time.Now().Add(d); until.After(l.next) {
		l.next = until
	}
	l.mu.Unlock()
}

// get baixa url com até 4 tentativas em 429/5xx, respeitando o Retry-After.
func get(ctx context.Context, hc *http.Client, lim *limiter, url string, header http.Header) ([]byte, error) {
	var last error
	for attempt := range 4 {
		if attempt > 0 {
			wait := time.Duration(attempt) * time.Second
			if ra, ok := last.(retryAfter); ok && ra.d > 0 {
				wait = min(ra.d, 30*time.Second)
			}
			lim.pause(wait)
		}
		if err := lim.wait(ctx); err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		for k, v := range header {
			req.Header[k] = v
		}
		req.Header.Set("User-Agent", UserAgent)
		req.Header.Set("Accept-Language", "en")
		resp, err := hc.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			last = err
			continue
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		resp.Body.Close()
		switch {
		case err != nil:
			last = err
		case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
			secs, _ := strconv.Atoi(resp.Header.Get("Retry-After"))
			last = retryAfter{status: resp.StatusCode, d: time.Duration(secs) * time.Second}
		case resp.StatusCode != http.StatusOK:
			return nil, &StatusError{URL: url, Code: resp.StatusCode}
		default:
			return body, nil
		}
	}
	return nil, last
}

// StatusError é uma resposta HTTP diferente de 200 que não vale repetir.
type StatusError struct {
	URL  string
	Code int
}

func (e *StatusError) Error() string { return fmt.Sprintf("HTTP %d em %s", e.Code, e.URL) }

type retryAfter struct {
	status int
	d      time.Duration
}

func (e retryAfter) Error() string { return fmt.Sprintf("HTTP %d (tentativas esgotadas)", e.status) }
