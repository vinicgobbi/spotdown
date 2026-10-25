package spotify

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLimiterSpacesRequests(t *testing.T) {
	l := newLimiter(20 * time.Millisecond)
	start := time.Now()
	var wg sync.WaitGroup
	for range 5 {
		wg.Add(1)
		go func() { defer wg.Done(); _ = l.wait(context.Background()) }()
	}
	wg.Wait()
	// 5 requisições: a primeira na hora, as outras 4 espaçadas de 20 ms.
	if d := time.Since(start); d < 75*time.Millisecond {
		t.Errorf("rápido demais: %v", d)
	}
}

func TestGetRetriesOn429(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Write([]byte("ok"))
	}))
	defer srv.Close()
	body, err := get(context.Background(), srv.Client(), newLimiter(0), srv.URL, nil)
	if err != nil || string(body) != "ok" || calls.Load() != 2 {
		t.Errorf("body=%q err=%v calls=%d", body, err, calls.Load())
	}
}

func TestGetDoesNotRetry404(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.NotFound(w, r)
	}))
	defer srv.Close()
	_, err := get(context.Background(), srv.Client(), newLimiter(0), srv.URL, nil)
	if se, ok := err.(*StatusError); !ok || se.Code != 404 || calls.Load() != 1 {
		t.Errorf("err=%v calls=%d", err, calls.Load())
	}
}

func TestDiskCache(t *testing.T) {
	c := newDiskCache(t.TempDir())
	c.put("x", map[string]int{"a": 1})
	var v map[string]int
	if !c.get("x", &v) || v["a"] != 1 {
		t.Errorf("v=%v", v)
	}
	if c.get("nada", &v) {
		t.Error("achou chave inexistente")
	}
	var nilCache *diskCache
	nilCache.put("x", 1) // não pode entrar em pânico
	if nilCache.get("x", &v) {
		t.Error("cache nil achou algo")
	}
}
