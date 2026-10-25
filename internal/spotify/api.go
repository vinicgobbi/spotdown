package spotify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/vinicgobbi/spotdown/internal/track"
)

// api é o cliente da API Web oficial, usado quando o usuário configura um
// app do Spotify. Com login (user), lê as playlists dele sem o limite de 100
// faixas do embed; só com Client ID/Secret (client credentials, sem
// usuário), lê faixas e álbuns, mas não o conteúdo de playlists.
type api struct {
	hc           *http.Client
	lim          *limiter
	id, secret   string
	user         *userAuth
	mu           sync.Mutex
	token        string
	tokenExpires time.Time
}

// apiBase e appTokenURL são variáveis só para os testes apontarem para um servidor falso.
var (
	apiBase     = "https://api.spotify.com/v1"
	appTokenURL = "https://accounts.spotify.com/api/token"
)

// auth devolve o token do usuário logado ou, sem login, o do app.
func (a *api) auth(ctx context.Context) (string, error) {
	if a.user != nil {
		tok, err := a.user.access(ctx, a.hc)
		if err == nil || a.secret == "" {
			return tok, err
		}
		// Login vencido/revogado: o token do app ainda serve para faixas e álbuns.
	}
	return a.appToken(ctx)
}

func (a *api) appToken(ctx context.Context) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.token != "" && time.Until(a.tokenExpires) > time.Minute {
		return a.token, nil
	}
	form := url.Values{"grant_type": {"client_credentials"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, appTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.SetBasicAuth(a.id, a.secret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", UserAgent)
	resp, err := a.hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("API do Spotify: %w", err)
	}
	defer resp.Body.Close()
	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		Error       string `json:"error_description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil || tok.AccessToken == "" {
		if tok.Error != "" {
			return "", fmt.Errorf("API do Spotify: credenciais recusadas: %s", tok.Error)
		}
		return "", fmt.Errorf("API do Spotify: falha ao autenticar (HTTP %d)", resp.StatusCode)
	}
	a.token = tok.AccessToken
	a.tokenExpires = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
	return a.token, nil
}

func (a *api) get(ctx context.Context, path string, v any) error {
	tok, err := a.auth(ctx)
	if err != nil {
		return err
	}
	u := path
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		u = apiBase + path
	}
	body, err := get(ctx, a.hc, a.lim, u, http.Header{"Authorization": {"Bearer " + tok}})
	if err != nil {
		return err
	}
	return json.Unmarshal(body, v)
}

type apiImage struct {
	URL   string `json:"url"`
	Width int    `json:"width"`
}

type apiArtist struct {
	Name string `json:"name"`
}

type apiAlbum struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Artists     []apiArtist `json:"artists"`
	ReleaseDate string      `json:"release_date"`
	Images      []apiImage  `json:"images"`
	TotalTracks int         `json:"total_tracks"`
	Label       string      `json:"label"`
	Genres      []string    `json:"genres"`
	Tracks      *apiPage    `json:"tracks"`
}

type apiTrack struct {
	ID          string      `json:"id"`
	Type        string      `json:"type"`
	Name        string      `json:"name"`
	Artists     []apiArtist `json:"artists"`
	Album       *apiAlbum   `json:"album"`
	DiscNumber  int         `json:"disc_number"`
	TrackNumber int         `json:"track_number"`
	DurationMs  int         `json:"duration_ms"`
	Explicit    bool        `json:"explicit"`
	IsLocal     bool        `json:"is_local"`
	ExternalIDs struct {
		ISRC string `json:"isrc"`
	} `json:"external_ids"`
}

// apiPage é uma página de faixas: de álbum (itens são faixas) ou de playlist
// (itens embrulham a faixa em "track" ou, na API nova, em "item").
type apiPage struct {
	Items []json.RawMessage `json:"items"`
	Next  string            `json:"next"`
	Total int               `json:"total"`
}

func (p *apiPage) tracks(wrapped bool) []apiTrack {
	var out []apiTrack
	for _, raw := range p.Items {
		var t apiTrack
		if wrapped {
			// Na API nova a faixa vem em "item" e "track" virou booleano;
			// na antiga, "track" é a faixa. Lido como bruto para aceitar os dois.
			var w struct {
				Track json.RawMessage `json:"track"`
				Item  *apiTrack       `json:"item"`
			}
			if json.Unmarshal(raw, &w) != nil {
				continue
			}
			switch {
			case w.Item != nil:
				t = *w.Item
			case len(w.Track) > 0 && w.Track[0] == '{':
				if json.Unmarshal(w.Track, &t) != nil {
					continue
				}
			default:
				continue
			}
		} else if json.Unmarshal(raw, &t) != nil {
			continue
		}
		if t.ID == "" || t.IsLocal || (t.Type != "" && t.Type != "track") {
			continue
		}
		out = append(out, t)
	}
	return out
}

func (t apiTrack) track() track.Track {
	out := track.Track{
		ID: t.ID, Title: t.Name, DiscNumber: t.DiscNumber, TrackNumber: t.TrackNumber,
		DurationMs: t.DurationMs, Explicit: t.Explicit, ISRC: t.ExternalIDs.ISRC,
	}
	for _, a := range t.Artists {
		out.Artists = append(out.Artists, a.Name)
	}
	if t.Album != nil {
		t.Album.apply(&out)
	}
	return out
}

func (a *apiAlbum) apply(t *track.Track) {
	t.AlbumID = a.ID
	t.Album = firstNonEmpty(a.Name, t.Album)
	var names []string
	for _, ar := range a.Artists {
		names = append(names, ar.Name)
	}
	if len(names) > 0 {
		t.AlbumArtist = strings.Join(names, ", ")
	}
	t.Date = firstNonEmpty(a.ReleaseDate, t.Date)
	best, bw := "", -1
	for _, i := range a.Images {
		if i.Width > bw {
			best, bw = i.URL, i.Width
		}
	}
	t.CoverURL = firstNonEmpty(best, t.CoverURL)
	t.TracksCount = max(a.TotalTracks, t.TracksCount)
	t.Publisher = firstNonEmpty(a.Label, t.Publisher)
	if len(a.Genres) > 0 {
		t.Genre = a.Genres[0]
	}
}

func (a *api) track(ctx context.Context, id string) (track.Track, error) {
	var t apiTrack
	if err := a.get(ctx, "/tracks/"+id, &t); err != nil {
		return track.Track{}, err
	}
	return t.track(), nil
}

// album devolve o álbum com todas as faixas (paginando de 50 em 50).
func (a *api) album(ctx context.Context, id string) (*apiAlbum, []apiTrack, error) {
	var al apiAlbum
	if err := a.get(ctx, "/albums/"+id, &al); err != nil {
		return nil, nil, err
	}
	var tracks []apiTrack
	next := ""
	if al.Tracks != nil {
		tracks, next = al.Tracks.tracks(false), al.Tracks.Next
	}
	for next != "" {
		var page apiPage
		if err := a.get(ctx, next, &page); err != nil {
			return nil, nil, err
		}
		tracks = append(tracks, page.tracks(false)...)
		next = page.Next
	}
	al.Tracks = nil
	return &al, tracks, nil
}

// ErrPlaylistRestricted: a API não entrega o conteúdo desta playlist (apps
// em Development Mode só leem playlists do usuário logado ou colaborativas).
var ErrPlaylistRestricted = errors.New("playlist restrita")

// playlist devolve o nome e todas as faixas da playlist (de 100 em 100).
func (a *api) playlist(ctx context.Context, id string) (string, []apiTrack, error) {
	var meta struct {
		Name string `json:"name"`
	}
	if err := a.get(ctx, "/playlists/"+id+"?fields=name", &meta); err != nil {
		return "", nil, err
	}
	var tracks []apiTrack
	next := fmt.Sprintf("/playlists/%s/items?limit=100", id)
	for next != "" {
		var page apiPage
		if err := a.get(ctx, next, &page); err != nil {
			var se *StatusError
			if errors.As(err, &se) && (se.Code == http.StatusUnauthorized || se.Code == http.StatusForbidden || se.Code == http.StatusNotFound) {
				return "", nil, ErrPlaylistRestricted
			}
			return "", nil, err
		}
		tracks = append(tracks, page.tracks(true)...)
		next = page.Next
	}
	if len(tracks) == 0 {
		// Playlist de outra pessoa: vem só o cabeçalho, sem "items".
		return "", nil, ErrPlaylistRestricted
	}
	return meta.Name, tracks, nil
}

// CheckCredentials confere Client ID/Secret pedindo um token à API.
func CheckCredentials(ctx context.Context, id, secret string) error {
	a := &api{hc: &http.Client{Timeout: 15 * time.Second}, lim: newLimiter(0), id: id, secret: secret}
	_, err := a.auth(ctx)
	return err
}
