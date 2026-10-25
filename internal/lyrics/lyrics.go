// Package lyrics busca letras no LRCLIB (lrclib.net), que tem letras
// sincronizadas (LRC) e simples, sem chave de API.
package lyrics

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/vinicgobbi/spotdown/internal/track"
)

// UserAgent é definido pelo main; o LRCLIB pede que os clientes se identifiquem.
var UserAgent = "spotdown/dev (+https://github.com/vinicgobbi/spotdown)"

// Lyrics é a letra encontrada. Synced está no formato LRC ("[mm:ss.xx] linha").
type Lyrics struct {
	Plain        string
	Synced       string
	Instrumental bool
}

// Best devolve a sincronizada, se houver, senão a simples.
func (l Lyrics) Best() string {
	if l.Synced != "" {
		return l.Synced
	}
	return l.Plain
}

// Empty informa se não há letra nenhuma.
func (l Lyrics) Empty() bool { return l.Plain == "" && l.Synced == "" }

// Client consulta o LRCLIB.
type Client struct {
	HTTP *http.Client
	Base string
}

// New cria o cliente.
func New() *Client {
	return &Client{HTTP: &http.Client{Timeout: 15 * time.Second}, Base: "https://lrclib.net/api"}
}

type entry struct {
	Duration     float64 `json:"duration"`
	Instrumental bool    `json:"instrumental"`
	PlainLyrics  string  `json:"plainLyrics"`
	SyncedLyrics string  `json:"syncedLyrics"`
}

func (e entry) lyrics() Lyrics {
	return Lyrics{Plain: e.PlainLyrics, Synced: e.SyncedLyrics, Instrumental: e.Instrumental}
}

// Find busca pela assinatura exata (artista, título, álbum, duração) e, se não
// achar, faz uma busca livre e fica com o resultado de duração mais próxima.
// Sem letra, devolve Lyrics vazia e nil.
func (c *Client) Find(ctx context.Context, t track.Track) (Lyrics, error) {
	q := url.Values{"artist_name": {t.Artist()}, "track_name": {t.Title}}
	if t.Album != "" {
		q.Set("album_name", t.Album)
	}
	if t.DurationMs > 0 {
		q.Set("duration", strconv.Itoa(t.Seconds()))
	}
	var e entry
	err := c.get(ctx, "/get?"+q.Encode(), &e)
	if err == nil && (!e.lyrics().Empty() || e.Instrumental) {
		return e.lyrics(), nil
	}
	if err != nil && err != errNotFound {
		return Lyrics{}, err
	}

	var list []entry
	if err := c.get(ctx, "/search?"+url.Values{"artist_name": {t.Artist()}, "track_name": {t.Title}}.Encode(), &list); err != nil {
		if err == errNotFound {
			return Lyrics{}, nil
		}
		return Lyrics{}, err
	}
	var best *entry
	bestDiff := 1e9
	for i, it := range list {
		if it.lyrics().Empty() {
			continue
		}
		diff := 0.0
		if t.DurationMs > 0 {
			diff = abs(it.Duration - float64(t.DurationMs)/1000)
			if diff > 5 {
				continue // provavelmente outra versão
			}
		}
		// Prefere a sincronizada quando as durações empatam.
		if it.SyncedLyrics != "" {
			diff -= 0.5
		}
		if diff < bestDiff {
			best, bestDiff = &list[i], diff
		}
	}
	if best == nil {
		return Lyrics{}, nil
	}
	return best.lyrics(), nil
}

var errNotFound = fmt.Errorf("letra não encontrada")

func (c *Client) get(ctx context.Context, path string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Base+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", UserAgent)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("LRCLIB: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return errNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("LRCLIB: HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(v)
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

// LRC monta um arquivo .lrc com cabeçalho de metadados. Letras sem
// sincronização viram LRC sem tempos (aceito pela maioria dos players).
func LRC(t track.Track, l Lyrics) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[ti:%s]\n[ar:%s]\n", t.Title, t.ArtistsString())
	if t.Album != "" {
		fmt.Fprintf(&b, "[al:%s]\n", t.Album)
	}
	if s := t.Seconds(); s > 0 {
		fmt.Fprintf(&b, "[length:%02d:%02d]\n", s/60, s%60)
	}
	b.WriteString("[by:spotdown/lrclib]\n\n")
	b.WriteString(strings.TrimSpace(l.Best()))
	b.WriteString("\n")
	return b.String()
}
