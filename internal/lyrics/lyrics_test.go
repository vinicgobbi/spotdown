package lyrics

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vinicgobbi/spotdown/internal/track"
)

func TestFindFallsBackToSearch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/get":
			http.NotFound(w, r)
		case "/search":
			w.Write([]byte(`[
				{"duration": 300, "plainLyrics": "versão ao vivo"},
				{"duration": 214, "plainLyrics": "simples"},
				{"duration": 213.4, "plainLyrics": "simples", "syncedLyrics": "[00:01.00] sincronizada"}
			]`))
		}
	}))
	defer srv.Close()
	c := New()
	c.Base = srv.URL

	tr := track.Track{Title: "Música", Artists: []string{"Artista"}, DurationMs: 213573}
	l, err := c.Find(context.Background(), tr)
	if err != nil {
		t.Fatal(err)
	}
	if l.Synced != "[00:01.00] sincronizada" {
		t.Errorf("got %+v", l)
	}
	lrc := LRC(tr, l)
	if !strings.Contains(lrc, "[ti:Música]") || !strings.Contains(lrc, "[length:03:34]") || !strings.HasSuffix(lrc, "sincronizada\n") {
		t.Errorf("lrc:\n%s", lrc)
	}
}

func TestFindNothing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/search" {
			w.Write([]byte(`[]`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	c := New()
	c.Base = srv.URL
	l, err := c.Find(context.Background(), track.Track{Title: "x", Artists: []string{"y"}})
	if err != nil || !l.Empty() {
		t.Errorf("l=%+v err=%v", l, err)
	}
}
