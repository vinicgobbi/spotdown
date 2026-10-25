package spotify

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/vinicgobbi/spotdown/internal/track"
)

// As páginas públicas do Spotify trazem os dados em dois formatos:
//   - embed (open.spotify.com/embed/...): JSON em <script id="__NEXT_DATA__">;
//     é a única fonte pública com a lista de uma playlist (até 100 faixas).
//   - página normal (open.spotify.com/track|album/...): estado inicial em
//     base64 em <script id="initialState">, com álbum, número da faixa,
//     disco, data e gravadora.

var (
	nextDataRe     = regexp.MustCompile(`(?s)<script id="__NEXT_DATA__" type="application/json">(.*?)</script>`)
	initialStateRe = regexp.MustCompile(`(?s)<script id="initialState" type="text/plain">(.*?)</script>`)
)

type image struct {
	URL      string `json:"url"`
	Width    int    `json:"width"`
	MaxWidth int    `json:"maxWidth"`
}

func bestImage(imgs []image) string {
	best, bw := "", -1
	for _, i := range imgs {
		w := max(i.Width, i.MaxWidth)
		if i.URL != "" && w > bw {
			best, bw = i.URL, w
		}
	}
	return best
}

// ---- embed ----

type embedEntity struct {
	Type        string                  `json:"type"`
	ID          string                  `json:"id"`
	Name        string                  `json:"name"`
	Title       string                  `json:"title"`
	Subtitle    string                  `json:"subtitle"`
	Artists     []struct{ Name string } `json:"artists"`
	ReleaseDate *struct {
		IsoString string `json:"isoString"`
	} `json:"releaseDate"`
	Duration       int  `json:"duration"`
	IsExplicit     bool `json:"isExplicit"`
	VisualIdentity struct {
		Image []image `json:"image"`
	} `json:"visualIdentity"`
	CoverArt struct {
		Sources []image `json:"sources"`
	} `json:"coverArt"`
	TrackList []embedTrack `json:"trackList"`

	token string // token anônimo do player embutido (ver webplayer.go)
}

type embedTrack struct {
	URI        string `json:"uri"`
	Title      string `json:"title"`
	Subtitle   string `json:"subtitle"`
	Duration   int    `json:"duration"`
	IsExplicit bool   `json:"isExplicit"`
	EntityType string `json:"entityType"`
}

func parseEmbed(html []byte) (*embedEntity, error) {
	m := nextDataRe.FindSubmatch(html)
	if m == nil {
		return nil, fmt.Errorf("embed sem __NEXT_DATA__ (o Spotify mudou a página?)")
	}
	var doc struct {
		Props struct {
			PageProps struct {
				State struct {
					Data struct {
						Entity embedEntity `json:"entity"`
					} `json:"data"`
					Settings struct {
						Session struct {
							AccessToken string `json:"accessToken"`
						} `json:"session"`
					} `json:"settings"`
				} `json:"state"`
			} `json:"pageProps"`
		} `json:"props"`
	}
	if err := json.Unmarshal(m[1], &doc); err != nil {
		return nil, fmt.Errorf("embed: %w", err)
	}
	e := doc.Props.PageProps.State.Data.Entity
	e.token = doc.Props.PageProps.State.Settings.Session.AccessToken
	if e.ID == "" {
		return nil, fmt.Errorf("embed sem dados (link privado ou removido?)")
	}
	return &e, nil
}

func (e *embedEntity) cover() string {
	if u := bestImage(e.VisualIdentity.Image); u != "" {
		return u
	}
	return bestImage(e.CoverArt.Sources)
}

// track converte o embed de uma faixa.
func (e *embedEntity) track() track.Track {
	t := track.Track{ID: e.ID, Title: firstNonEmpty(e.Name, e.Title), DurationMs: e.Duration, Explicit: e.IsExplicit, CoverURL: e.cover()}
	for _, a := range e.Artists {
		t.Artists = append(t.Artists, a.Name)
	}
	if e.ReleaseDate != nil && len(e.ReleaseDate.IsoString) >= 10 {
		t.Date = e.ReleaseDate.IsoString[:10]
	}
	return t
}

// tracks converte a lista de faixas do embed (o subtitle traz os artistas
// separados por vírgula).
func (e *embedEntity) tracks() []track.Track {
	var out []track.Track
	for _, it := range e.TrackList {
		if it.EntityType != "" && it.EntityType != "track" {
			continue // episódios de podcast
		}
		id := it.URI[strings.LastIndex(it.URI, ":")+1:]
		if !idRe.MatchString(id) {
			continue
		}
		out = append(out, track.Track{
			ID: id, Title: it.Title, Artists: splitArtists(it.Subtitle),
			DurationMs: it.Duration, Explicit: it.IsExplicit,
		})
	}
	return out
}

func splitArtists(s string) []string {
	var out []string
	for _, a := range strings.Split(s, ",") {
		if a = strings.TrimSpace(a); a != "" {
			out = append(out, a)
		}
	}
	return out
}

// ---- página normal (initialState) ----

type artistList struct {
	Items []struct {
		Profile struct {
			Name string `json:"name"`
		} `json:"profile"`
	} `json:"items"`
}

func (a artistList) names() []string {
	var out []string
	for _, i := range a.Items {
		if i.Profile.Name != "" {
			out = append(out, i.Profile.Name)
		}
	}
	return out
}

type dateParts struct {
	Year  int `json:"year"`
	Month int `json:"month"`
	Day   int `json:"day"`
}

// UnmarshalJSON aceita {year,month,day}, {isoString} ou uma string ISO e
// nunca falha: uma data num formato inesperado não pode derrubar a faixa.
func (d *dateParts) UnmarshalJSON(b []byte) error {
	var obj struct {
		Year      int    `json:"year"`
		Month     int    `json:"month"`
		Day       int    `json:"day"`
		IsoString string `json:"isoString"`
	}
	var iso string
	if json.Unmarshal(b, &obj) == nil {
		d.Year, d.Month, d.Day = obj.Year, obj.Month, obj.Day
		iso = obj.IsoString
	} else {
		_ = json.Unmarshal(b, &iso)
	}
	if d.Year == 0 && len(iso) >= 4 {
		fmt.Sscanf(iso, "%4d-%2d-%2d", &d.Year, &d.Month, &d.Day)
	}
	return nil
}

func (d dateParts) String() string {
	switch {
	case d.Year == 0:
		return ""
	case d.Month == 0:
		return fmt.Sprintf("%04d", d.Year)
	case d.Day == 0:
		return fmt.Sprintf("%04d-%02d", d.Year, d.Month)
	}
	return fmt.Sprintf("%04d-%02d-%02d", d.Year, d.Month, d.Day)
}

type copyright struct {
	Items []struct {
		Text string `json:"text"`
		Type string `json:"type"`
	} `json:"items"`
}

var copyrightPrefix = regexp.MustCompile(`^\s*[©℗]?\s*(\(?[CP]\)?)?\s*\d{4}\s*`)

// publisher extrai a gravadora do copyright (preferindo o ℗, que é o do fonograma).
func (c copyright) publisher() string {
	pick := ""
	for _, it := range c.Items {
		if it.Type == "P" || pick == "" {
			pick = it.Text
		}
	}
	return strings.TrimSpace(copyrightPrefix.ReplaceAllString(pick, ""))
}

type pageTrack struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	TrackNumber int    `json:"trackNumber"`
	DiscNumber  int    `json:"discNumber"`
	Duration    struct {
		TotalMilliseconds int `json:"totalMilliseconds"`
	} `json:"duration"`
	URI           string     `json:"uri"`
	Artists       artistList `json:"artists"`
	FirstArtist   artistList `json:"firstArtist"`
	OtherArtists  artistList `json:"otherArtists"`
	ContentRating struct {
		Label string `json:"label"`
	} `json:"contentRating"`
	AlbumOfTrack *pageAlbum `json:"albumOfTrack"`
}

type pageAlbum struct {
	ID        string     `json:"id"`
	URI       string     `json:"uri"`
	Name      string     `json:"name"`
	Type      string     `json:"type"`
	Artists   artistList `json:"artists"`
	Date      dateParts  `json:"date"`
	Copyright copyright  `json:"copyright"`
	CoverArt  struct {
		Sources []image `json:"sources"`
	} `json:"coverArt"`
	Tracks   pageTrackList `json:"tracks"`
	TracksV2 pageTrackList `json:"tracksV2"`
}

type pageTrackList struct {
	Items []struct {
		Track pageTrack `json:"track"`
	} `json:"items"`
	TotalCount int `json:"totalCount"`
}

type pagePlaylist struct {
	Name    string `json:"name"`
	Content struct {
		TotalCount int `json:"totalCount"`
	} `json:"content"`
}

// parseInitialState devolve a entidade principal da página (a única em entities.items).
func parseInitialState(html []byte, uri string, v any) error {
	m := initialStateRe.FindSubmatch(html)
	if m == nil {
		return fmt.Errorf("página sem initialState (o Spotify mudou a página?)")
	}
	raw, err := base64.StdEncoding.DecodeString(string(bytes.TrimSpace(m[1])))
	if err != nil {
		return fmt.Errorf("initialState: %w", err)
	}
	var doc struct {
		Entities struct {
			Items map[string]json.RawMessage `json:"items"`
		} `json:"entities"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("initialState: %w", err)
	}
	ent, ok := doc.Entities.Items[uri]
	if !ok {
		return fmt.Errorf("initialState sem %s", uri)
	}
	return json.Unmarshal(ent, v)
}

func (p pageTrack) id() string {
	if p.ID != "" {
		return p.ID
	}
	return p.URI[strings.LastIndex(p.URI, ":")+1:]
}

func (p pageTrack) artists() []string {
	if a := p.Artists.names(); len(a) > 0 {
		return a
	}
	return append(p.FirstArtist.names(), p.OtherArtists.names()...)
}

func (a *pageAlbum) id() string {
	if a.ID != "" {
		return a.ID
	}
	return a.URI[strings.LastIndex(a.URI, ":")+1:]
}

func (a *pageAlbum) list() pageTrackList {
	if len(a.TracksV2.Items) > 0 {
		return a.TracksV2
	}
	return a.Tracks
}

// apply copia para t os dados do álbum: nome, artista, data, capa, gravadora,
// contagem de faixas/discos e — se a faixa estiver na lista — disco e número.
func (a *pageAlbum) apply(t *track.Track) {
	t.AlbumID = a.id()
	t.Album = firstNonEmpty(a.Name, t.Album)
	if names := a.Artists.names(); len(names) > 0 {
		t.AlbumArtist = strings.Join(names, ", ")
	}
	t.Date = firstNonEmpty(a.Date.String(), t.Date)
	t.CoverURL = firstNonEmpty(bestImage(a.CoverArt.Sources), t.CoverURL)
	t.Publisher = firstNonEmpty(a.Copyright.publisher(), t.Publisher)

	l := a.list()
	t.TracksCount = max(l.TotalCount, len(l.Items), t.TracksCount)
	discs := 0
	for _, it := range l.Items {
		discs = max(discs, it.Track.DiscNumber)
		if it.Track.id() == t.ID {
			if it.Track.TrackNumber > 0 {
				t.TrackNumber = it.Track.TrackNumber
			}
			if it.Track.DiscNumber > 0 {
				t.DiscNumber = it.Track.DiscNumber
			}
		}
	}
	if discs > 0 {
		t.DiscCount = discs
	}
}

// track converte a página de uma faixa (o álbum vem resumido em albumOfTrack).
func (p pageTrack) track() track.Track {
	t := track.Track{
		ID: p.id(), Title: p.Name, Artists: p.artists(),
		TrackNumber: p.TrackNumber, DiscNumber: p.DiscNumber,
		DurationMs: p.Duration.TotalMilliseconds, Explicit: p.ContentRating.Label == "EXPLICIT",
	}
	if p.AlbumOfTrack != nil {
		p.AlbumOfTrack.apply(&t)
	}
	return t
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}
