// Package spotify transforma links do Spotify em faixas com metadados, sem
// login: a lista vem do embed e os detalhes (álbum, número, disco, data,
// gravadora) da página pública de cada faixa/álbum. Com Client ID/Secret
// configurados, usa a API oficial, que não tem o limite de 100 faixas do embed.
package spotify

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vinicgobbi/spotdown/internal/track"
)

// embedLimit é o máximo de faixas que o embed de uma playlist devolve.
const embedLimit = 100

// List é o resultado de um link: uma faixa avulsa, um álbum ou uma playlist.
type List struct {
	Kind     Kind
	ID       string
	Name     string
	Tracks   []track.Track
	Total    int      // total de faixas segundo o Spotify (pode ser maior que len(Tracks))
	Warnings []string // avisos não fatais (limite do embed, faixas sem detalhes...)
}

// Client busca metadados. É seguro para uso concorrente.
type Client struct {
	hc         *http.Client
	api        *api
	pageLim    *limiter
	cache      *diskCache
	workers    int
	albumExtra bool

	mu     sync.Mutex
	albums map[string]*albumEntry
}

type albumEntry struct {
	once  sync.Once
	page  *pageAlbum
	api   *apiAlbum
	discs int
	err   error
}

// Options configura o cliente.
type Options struct {
	ClientID, ClientSecret string // vazios = só páginas públicas
	CacheDir               string // vazio = sem cache em disco
	// AlbumDetails pede, na API, a consulta extra ao álbum de cada faixa
	// (gravadora, gênero, quantidade de discos). Sem ela, os dados de álbum
	// que já vêm na faixa bastam e a playlist sai com bem menos requisições.
	AlbumDetails bool
}

// Intervalos mínimos entre requisições a cada host.
const (
	pageEvery = 100 * time.Millisecond // open.spotify.com
	apiEvery  = 200 * time.Millisecond // api.spotify.com
)

// New cria o cliente.
func New(o Options) *Client {
	c := &Client{
		hc:         &http.Client{Timeout: 30 * time.Second},
		pageLim:    newLimiter(pageEvery),
		cache:      newDiskCache(o.CacheDir),
		workers:    6,
		albumExtra: o.AlbumDetails,
		albums:     map[string]*albumEntry{},
	}
	if o.ClientID != "" && o.ClientSecret != "" {
		c.api = &api{hc: c.hc, lim: newLimiter(apiEvery), id: o.ClientID, secret: o.ClientSecret}
	}
	return c
}

// HasAPI informa se a API oficial está configurada.
func (c *Client) HasAPI() bool { return c.api != nil }

// Resolve busca as faixas do link. progress (opcional) recebe quantas faixas
// já tiveram os detalhes buscados.
func (c *Client) Resolve(ctx context.Context, link string, progress func(done, total int)) (*List, error) {
	kind, id, err := ParseURL(link)
	if err != nil {
		return nil, err
	}
	if progress == nil {
		progress = func(int, int) {}
	}
	var l *List
	switch kind {
	case KindTrack:
		l, err = c.resolveTrack(ctx, id)
	case KindAlbum:
		l, err = c.resolveAlbum(ctx, id)
	case KindPlaylist:
		l, err = c.resolvePlaylist(ctx, id, progress)
	}
	if err != nil {
		return nil, err
	}
	l.Kind, l.ID = kind, id
	if l.Total == 0 {
		l.Total = len(l.Tracks)
	}
	if kind != KindTrack {
		for i := range l.Tracks {
			l.Tracks[i].ListName = l.Name
			l.Tracks[i].ListPosition = i + 1
			l.Tracks[i].ListLength = len(l.Tracks)
		}
	}
	return l, nil
}

func (c *Client) page(ctx context.Context, path string) ([]byte, error) {
	return get(ctx, c.hc, c.pageLim, "https://open.spotify.com/"+path, nil)
}

// ---- faixa ----

func (c *Client) resolveTrack(ctx context.Context, id string) (*List, error) {
	if c.api != nil {
		if t, err := c.apiTrack(ctx, id); err == nil {
			return &List{Name: t.Title, Tracks: []track.Track{t}}, nil
		}
	}
	t, err := c.trackDetails(ctx, id)
	if err != nil {
		// Último recurso: o embed tem título, artistas, duração e capa.
		body, err2 := c.page(ctx, "embed/track/"+id)
		if err2 != nil {
			return nil, fmt.Errorf("faixa %s: %w", id, err)
		}
		e, err2 := parseEmbed(body)
		if err2 != nil {
			return nil, fmt.Errorf("faixa %s: %w", id, err)
		}
		t = e.track()
		return &List{Name: t.Title, Tracks: []track.Track{t}, Warnings: []string{"sem detalhes do álbum: " + err.Error()}}, nil
	}
	return &List{Name: t.Title, Tracks: []track.Track{t}}, nil
}

// trackDetails lê a página da faixa e completa com a página do álbum
// (disco, artista do álbum, quantidade de discos).
func (c *Client) trackDetails(ctx context.Context, id string) (track.Track, error) {
	var t track.Track
	if c.cache.get("track-"+id, &t) {
		return t, nil
	}
	body, err := c.page(ctx, "track/"+id)
	if err != nil {
		return track.Track{}, err
	}
	var p pageTrack
	if err := parseInitialState(body, "spotify:track:"+id, &p); err != nil {
		return track.Track{}, err
	}
	t = p.track()
	if t.AlbumID != "" {
		if e := c.albumPage(ctx, t.AlbumID); e.err == nil {
			e.page.apply(&t)
		}
	}
	c.cache.put("track-"+id, t)
	return t, nil
}

// apiTrack busca a faixa na API (com cache) e, se pedido, os detalhes do álbum.
func (c *Client) apiTrack(ctx context.Context, id string) (track.Track, error) {
	var t track.Track
	if !c.cache.get("api-track-"+id, &t) {
		var err error
		if t, err = c.api.track(ctx, id); err != nil {
			return t, err
		}
		c.cache.put("api-track-"+id, t)
	}
	c.applyAPIAlbum(ctx, &t)
	return t, nil
}

// ---- álbum ----

func (c *Client) albumPage(ctx context.Context, id string) *albumEntry {
	e := c.albumEntry("page:" + id)
	e.once.Do(func() {
		var a pageAlbum
		if c.cache.get("album-"+id, &a) {
			e.page = &a
			return
		}
		body, err := c.page(ctx, "album/"+id)
		if err != nil {
			e.err = err
			return
		}
		if err := parseInitialState(body, "spotify:album:"+id, &a); err != nil {
			e.err = err
			return
		}
		c.cache.put("album-"+id, a)
		e.page = &a
	})
	return e
}

func (c *Client) albumEntry(key string) *albumEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.albums[key]
	if !ok {
		e = &albumEntry{}
		c.albums[key] = e
	}
	return e
}

func (c *Client) resolveAlbum(ctx context.Context, id string) (*List, error) {
	if c.api != nil {
		if al, tracks, err := c.api.album(ctx, id); err == nil {
			discs := 0
			for _, t := range tracks {
				discs = max(discs, t.DiscNumber)
			}
			l := &List{Name: al.Name, Total: al.TotalTracks}
			for _, at := range tracks {
				t := at.track()
				al.apply(&t)
				t.DiscCount = discs
				l.Tracks = append(l.Tracks, t)
			}
			return l, nil
		}
	}
	e := c.albumPage(ctx, id)
	if e.err != nil {
		return nil, fmt.Errorf("álbum %s: %w", id, e.err)
	}
	a := e.page
	l := &List{Name: a.Name, Total: a.list().TotalCount}
	for _, it := range a.list().Items {
		t := it.Track.track()
		a.apply(&t)
		l.Tracks = append(l.Tracks, t)
	}
	if l.Total > len(l.Tracks) {
		l.Warnings = append(l.Warnings, fmt.Sprintf("a página pública trouxe %d de %d faixas; configure a API do Spotify para o álbum completo", len(l.Tracks), l.Total))
	}
	return l, nil
}

// ---- playlist ----

func (c *Client) resolvePlaylist(ctx context.Context, id string, progress func(int, int)) (*List, error) {
	var warnings []string
	if c.api != nil {
		name, tracks, err := c.api.playlist(ctx, id)
		if err == nil {
			l := &List{Name: name}
			for _, at := range tracks {
				t := at.track()
				c.cache.put("api-track-"+t.ID, t)
				l.Tracks = append(l.Tracks, t)
			}
			if c.albumExtra {
				c.parallel(ctx, l.Tracks, progress, func(t *track.Track) error {
					c.applyAPIAlbum(ctx, t)
					return nil
				})
			}
			return l, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		warnings = append(warnings, err.Error()+" — usando a página pública")
	}

	body, err := c.page(ctx, "embed/playlist/"+id)
	if err != nil {
		return nil, fmt.Errorf("playlist %s: %w", id, err)
	}
	e, err := parseEmbed(body)
	if err != nil {
		return nil, fmt.Errorf("playlist %s: %w", id, err)
	}
	l := &List{Name: firstNonEmpty(e.Name, e.Title), Tracks: e.tracks(), Warnings: warnings}
	if len(e.TrackList) >= embedLimit {
		// O embed para em 100; a página da playlist informa o total real.
		if body, err := c.page(ctx, "playlist/"+id); err == nil {
			var p pagePlaylist
			if parseInitialState(body, "spotify:playlist:"+id, &p) == nil {
				l.Total = p.Content.TotalCount
			}
		}
		if l.Total > len(l.Tracks) {
			l.Warnings = append(l.Warnings, fmt.Sprintf(
				"o Spotify só libera as primeiras %d de %d faixas sem login; configure spotify_client_id/spotify_client_secret para a playlist completa",
				len(l.Tracks), l.Total))
		}
	}

	var failed atomic.Int32
	c.parallel(ctx, l.Tracks, progress, func(t *track.Track) error {
		full, err := c.trackDetails(ctx, t.ID)
		if err != nil {
			failed.Add(1)
			return err
		}
		*t = full
		return nil
	})
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if n := failed.Load(); n > 0 {
		l.Warnings = append(l.Warnings, fmt.Sprintf("%d faixa(s) sem detalhes do álbum (só título/artista/duração)", n))
	}
	return l, nil
}

// applyAPIAlbum completa a faixa com gravadora, gênero e contagem de discos do álbum.
func (c *Client) applyAPIAlbum(ctx context.Context, t *track.Track) {
	if !c.albumExtra || t.AlbumID == "" {
		return
	}
	e := c.albumEntry("api:" + t.AlbumID)
	e.once.Do(func() {
		var cached struct {
			Album *apiAlbum
			Discs int
		}
		if c.cache.get("api-album-"+t.AlbumID, &cached) && cached.Album != nil {
			e.api, e.discs = cached.Album, cached.Discs
			return
		}
		al, tracks, err := c.api.album(ctx, t.AlbumID)
		if err != nil {
			e.err = err
			return
		}
		e.api = al
		for _, tr := range tracks {
			e.discs = max(e.discs, tr.DiscNumber)
		}
		cached.Album, cached.Discs = al, e.discs
		c.cache.put("api-album-"+t.AlbumID, cached)
	})
	if e.err != nil {
		return
	}
	e.api.apply(t)
	if e.discs > 0 {
		t.DiscCount = e.discs
	}
}

// parallel aplica fn a cada faixa com c.workers goroutines, informando o progresso.
func (c *Client) parallel(ctx context.Context, tracks []track.Track, progress func(int, int), fn func(*track.Track) error) {
	var (
		wg   sync.WaitGroup
		done atomic.Int32
		sem  = make(chan struct{}, c.workers)
	)
	progress(0, len(tracks))
	for i := range tracks {
		select {
		case <-ctx.Done():
		case sem <- struct{}{}:
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() { <-sem }()
				_ = fn(&tracks[i])
				progress(int(done.Add(1)), len(tracks))
			}()
			continue
		}
		break
	}
	wg.Wait()
}
