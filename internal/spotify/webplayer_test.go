package spotify

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
)

// gqlItem monta uma linha do fetchPlaylist como o web player devolve.
func gqlItem(i int) map[string]any {
	id := fmt.Sprintf("%022d", i)
	return map[string]any{"itemV2": map[string]any{
		"__typename": "TrackResponseWrapper",
		"data": map[string]any{
			"uri": "spotify:track:" + id, "name": "Faixa " + strconv.Itoa(i),
			"artists":       map[string]any{"items": []any{map[string]any{"profile": map[string]any{"name": "Rick Astley"}}}},
			"albumOfTrack":  map[string]any{"uri": "spotify:album:5Z9iiGl2FcIfa3BMiv6OIw", "name": "Whenever You Need Somebody", "date": map[string]any{"isoString": "1987-11-12T00:00:00Z"}},
			"trackDuration": map[string]any{"totalMilliseconds": 200000 + i},
			"trackNumber":   1, "discNumber": 1,
		},
	}}
}

// fakeWeb serve o embed (com token), a página do álbum e o GraphQL.
func fakeWeb(t *testing.T, total int, gql func(w http.ResponseWriter, r *http.Request) bool) *[]int {
	t.Helper()
	album, err := os.ReadFile("testdata/album_page.html")
	if err != nil {
		t.Fatal(err)
	}
	var offsets []int
	mux := http.NewServeMux()
	mux.HandleFunc("/embed/playlist/", func(w http.ResponseWriter, r *http.Request) {
		var list []any
		for i := range min(total, 100) {
			list = append(list, map[string]any{"uri": fmt.Sprintf("spotify:track:%022d", i), "title": "Faixa", "subtitle": "Rick Astley", "entityType": "track"})
		}
		doc := map[string]any{"props": map[string]any{"pageProps": map[string]any{"state": map[string]any{
			"data":     map[string]any{"entity": map[string]any{"id": "pl", "name": "Do embed", "trackList": list}},
			"settings": map[string]any{"session": map[string]any{"accessToken": "anon-token"}},
		}}}}
		b, _ := json.Marshal(doc)
		fmt.Fprintf(w, `<script id="__NEXT_DATA__" type="application/json">%s</script>`, b)
	})
	mux.HandleFunc("/album/", func(w http.ResponseWriter, r *http.Request) { w.Write(album) })
	mux.HandleFunc("/playlist/", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	mux.HandleFunc("/pathfinder", func(w http.ResponseWriter, r *http.Request) {
		if gql != nil && gql(w, r) {
			return
		}
		if r.Header.Get("Authorization") != "Bearer anon-token" || r.Header.Get("App-Platform") != "WebPlayer" {
			w.WriteHeader(401)
			return
		}
		var vars struct{ Offset, Limit int }
		_ = json.Unmarshal([]byte(r.URL.Query().Get("variables")), &vars)
		var ext struct {
			PersistedQuery struct{ Sha256Hash string } `json:"persistedQuery"`
		}
		_ = json.Unmarshal([]byte(r.URL.Query().Get("extensions")), &ext)
		if ext.PersistedQuery.Sha256Hash != DefaultGraphQLHash || r.URL.Query().Get("operationName") != "fetchPlaylist" {
			t.Errorf("consulta inesperada: %s", r.URL.RawQuery)
		}
		offsets = append(offsets, vars.Offset)
		var items []any
		for i := vars.Offset; i < min(total, vars.Offset+vars.Limit); i++ {
			items = append(items, gqlItem(i))
		}
		if vars.Offset == 0 {
			// Arquivo local e episódio: contam no total, mas não viram faixa.
			items[1] = map[string]any{"itemV2": map[string]any{"__typename": "TrackResponseWrapper", "data": map[string]any{"uri": "spotify:local:a:b:c:1", "name": "local"}}}
			items[2] = map[string]any{"itemV2": map[string]any{"__typename": "EpisodeResponseWrapper", "data": map[string]any{}}}
		}
		b, _ := json.Marshal(map[string]any{"data": map[string]any{"playlistV2": map[string]any{
			"name": "My favorites", "content": map[string]any{"totalCount": total, "items": items},
		}}})
		w.Write(b)
	})
	srv := httptest.NewServer(mux)
	oldOpen, oldPartner := openBase, partnerAPI
	openBase, partnerAPI = srv.URL+"/", srv.URL+"/pathfinder"
	t.Cleanup(func() { srv.Close(); openBase, partnerAPI = oldOpen, oldPartner })
	return &offsets
}

func TestWebPlayerFullPlaylist(t *testing.T) {
	offsets := fakeWeb(t, 290, nil)
	c := New(Options{WebPlayer: true})
	l, err := c.Resolve(context.Background(), "https://open.spotify.com/playlist/1KhRVW5ZmTt0N0wNvWi6HW?si=x", nil)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(*offsets) != "[0 100 200]" {
		t.Errorf("páginas pedidas: %v", *offsets)
	}
	if l.Name != "My favorites" || len(l.Tracks) != 288 || len(l.Warnings) != 0 {
		t.Fatalf("name=%q tracks=%d warnings=%v", l.Name, len(l.Tracks), l.Warnings)
	}
	last := l.Tracks[len(l.Tracks)-1]
	if last.Title != "Faixa 289" || last.DurationMs != 200289 || last.ListPosition != 288 || last.ListLength != 288 {
		t.Errorf("última faixa: %+v", last)
	}
	// Completada pela página do álbum (fixture real).
	if last.AlbumArtist != "Rick Astley" || last.TracksCount != 10 || last.Publisher == "" || last.Date != "1987-11-12" || last.CoverURL == "" {
		t.Errorf("sem os dados do álbum: %+v", last)
	}
}

func TestWebPlayerStaleHashFallsBackToEmbed(t *testing.T) {
	fakeWeb(t, 290, func(w http.ResponseWriter, r *http.Request) bool {
		fmt.Fprint(w, `{"errors":[{"message":"PersistedQueryNotFound"}]}`)
		return true
	})
	c := New(Options{WebPlayer: true})
	c.pageLim = newLimiter(0)
	l, err := c.Resolve(context.Background(), "spotify:playlist:1KhRVW5ZmTt0N0wNvWi6HW", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Tracks) != 100 || l.Name != "Do embed" {
		t.Fatalf("tracks=%d name=%q", len(l.Tracks), l.Name)
	}
	if len(l.Warnings) == 0 || !strings.Contains(l.Warnings[0], "spotify_graphql_hash") {
		t.Errorf("avisos: %v", l.Warnings)
	}
}

func TestWebPlayerOff(t *testing.T) {
	offsets := fakeWeb(t, 50, nil)
	c := New(Options{})
	c.pageLim = newLimiter(0)
	l, err := c.Resolve(context.Background(), "spotify:playlist:1KhRVW5ZmTt0N0wNvWi6HW", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(*offsets) != 0 || len(l.Tracks) != 50 {
		t.Errorf("com o web player desligado não pode chamar o GraphQL: %v, %d faixas", *offsets, len(l.Tracks))
	}
}

func TestDatePartsTolerant(t *testing.T) {
	for in, want := range map[string]string{
		`{"year":1987,"month":11,"day":12}`:    "1987-11-12",
		`{"isoString":"2020-05-01T00:00:00Z"}`: "2020-05-01",
		`"2019"`:                               "2019",
		`123`:                                  "",
		`{"year":"estranho"}`:                  "",
	} {
		var d dateParts
		if err := json.Unmarshal([]byte(in), &d); err != nil || d.String() != want {
			t.Errorf("%s: got %q err %v", in, d.String(), err)
		}
	}
}
