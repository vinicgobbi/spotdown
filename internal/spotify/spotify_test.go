package spotify

import (
	"os"
	"testing"
)

func TestParseURL(t *testing.T) {
	cases := []struct {
		in   string
		kind Kind
		id   string
	}{
		{"https://open.spotify.com/track/4cOdK2wGLETKBW3PvgPWqT?si=abc", KindTrack, "4cOdK2wGLETKBW3PvgPWqT"},
		{"https://open.spotify.com/intl-pt/album/5Z9iiGl2FcIfa3BMiv6OIw", KindAlbum, "5Z9iiGl2FcIfa3BMiv6OIw"},
		{"https://open.spotify.com/embed/playlist/37i9dQZF1DXcBWIGoYBM5M", KindPlaylist, "37i9dQZF1DXcBWIGoYBM5M"},
		{"spotify:playlist:37i9dQZF1DXcBWIGoYBM5M", KindPlaylist, "37i9dQZF1DXcBWIGoYBM5M"},
	}
	for _, c := range cases {
		k, id, err := ParseURL(c.in)
		if err != nil || k != c.kind || id != c.id {
			t.Errorf("%s: got %s %s %v", c.in, k, id, err)
		}
	}
	for _, bad := range []string{"https://youtube.com/watch?v=x", "https://open.spotify.com/artist/0gxyHStUsqpMadRV0Di1Qt", "spotify:track:curto"} {
		if _, _, err := ParseURL(bad); err == nil {
			t.Errorf("%s: esperava erro", bad)
		}
	}
}

func read(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestEmbedPlaylist(t *testing.T) {
	e, err := parseEmbed(read(t, "embed_playlist.html"))
	if err != nil {
		t.Fatal(err)
	}
	tracks := e.tracks()
	if e.Name != "Today’s Top Hits" || len(tracks) != 50 {
		t.Fatalf("name=%q tracks=%d", e.Name, len(tracks))
	}
	if tracks[0].ID != "2sFepQNyl1WjpROruP8bJ0" || tracks[0].Title != "Housewife" || tracks[0].Artist() != "Tate McRae" {
		t.Errorf("primeira faixa: %+v", tracks[0])
	}
	multi := false
	for _, tr := range tracks {
		multi = multi || len(tr.Artists) > 1
	}
	if !multi {
		t.Error("nenhuma faixa com vários artistas — o split do subtitle quebrou?")
	}
}

func TestEmbedTrack(t *testing.T) {
	e, err := parseEmbed(read(t, "embed_track.html"))
	if err != nil {
		t.Fatal(err)
	}
	tr := e.track()
	if tr.Title != "Never Gonna Give You Up" || tr.Artist() != "Rick Astley" || tr.Date != "1987-11-12" || tr.DurationMs != 213573 {
		t.Errorf("%+v", tr)
	}
}

func TestTrackPage(t *testing.T) {
	var p pageTrack
	if err := parseInitialState(read(t, "track_page.html"), "spotify:track:4cOdK2wGLETKBW3PvgPWqT", &p); err != nil {
		t.Fatal(err)
	}
	tr := p.track()
	if tr.Album != "Whenever You Need Somebody" || tr.AlbumID != "5Z9iiGl2FcIfa3BMiv6OIw" {
		t.Errorf("álbum: %q %q", tr.Album, tr.AlbumID)
	}
	if tr.TrackNumber != 1 || tr.TracksCount != 10 || tr.Date != "1987-11-12" || tr.Year() != "1987" {
		t.Errorf("num=%d count=%d date=%q", tr.TrackNumber, tr.TracksCount, tr.Date)
	}
	if tr.Publisher != "BMG Rights Management (UK) Limited" {
		t.Errorf("publisher=%q", tr.Publisher)
	}
	if tr.CoverURL == "" || tr.Artist() != "Rick Astley" {
		t.Errorf("cover=%q artists=%v", tr.CoverURL, tr.Artists)
	}
}

func TestAlbumPage(t *testing.T) {
	var a pageAlbum
	if err := parseInitialState(read(t, "album_page.html"), "spotify:album:5Z9iiGl2FcIfa3BMiv6OIw", &a); err != nil {
		t.Fatal(err)
	}
	l := a.list()
	if len(l.Items) != 10 || l.TotalCount != 10 {
		t.Fatalf("items=%d total=%d", len(l.Items), l.TotalCount)
	}
	tr := l.Items[1].Track.track()
	a.apply(&tr)
	if tr.Title != "Whenever You Need Somebody" || tr.TrackNumber != 2 || tr.DiscNumber != 1 || tr.DiscCount != 1 {
		t.Errorf("%+v", tr)
	}
	if tr.AlbumArtist != "Rick Astley" || tr.CoverURL == "" {
		t.Errorf("albumArtist=%q cover=%q", tr.AlbumArtist, tr.CoverURL)
	}
}

func TestPublisher(t *testing.T) {
	c := copyright{}
	c.Items = append(c.Items, struct {
		Text string `json:"text"`
		Type string `json:"type"`
	}{"(P) 2020 Gravadora X", "P"})
	if got := c.publisher(); got != "Gravadora X" {
		t.Errorf("got %q", got)
	}
}
