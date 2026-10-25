package ytmusic

import (
	"os"
	"testing"

	"github.com/vinicgobbi/spotdown/internal/track"
)

func load(t *testing.T, name string, songs bool) []Result {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	rs, err := parseSearch(b, songs)
	if err != nil {
		t.Fatal(err)
	}
	return rs
}

func TestParseSongs(t *testing.T) {
	rs := load(t, "songs.json", true)
	if len(rs) < 5 {
		t.Fatalf("poucos resultados: %d", len(rs))
	}
	r := rs[0]
	if r.VideoID != "lYBUbBu4W08" || r.Title != "Never Gonna Give You Up" || r.Album != "Whenever You Need Somebody" || r.Duration != 214 {
		t.Errorf("%+v", r)
	}
	if len(r.Artists) != 1 || r.Artists[0] != "Rick Astley" {
		t.Errorf("artists=%q", r.Artists)
	}
	if got := rs[1].Artists; len(got) != 2 || got[0] != "Sean Paul" || got[1] != "Keyshia Cole" {
		t.Errorf("artists=%q", got)
	}
}

func TestParseVideos(t *testing.T) {
	rs := load(t, "videos.json", false)
	if len(rs) == 0 || rs[0].Album != "" || rs[0].Duration != 214 || rs[0].IsSong {
		t.Errorf("%+v", rs)
	}
}

func TestScorePicksOriginal(t *testing.T) {
	tr := track.Track{Title: "Never Gonna Give You Up", Artists: []string{"Rick Astley"}, Album: "Whenever You Need Somebody", DurationMs: 213573}
	rs := append(load(t, "songs.json", true), load(t, "videos.json", false)...)
	best, bs := Result{}, -1.0
	for _, r := range rs {
		if s := Score(tr, r); s > bs {
			best, bs = r, s
		}
	}
	if best.VideoID != "lYBUbBu4W08" || bs < goodEnough {
		t.Errorf("escolheu %+v (%.2f)", best, bs)
	}
}

func TestScorePenalizesVersions(t *testing.T) {
	tr := track.Track{Title: "Blinding Lights", Artists: []string{"The Weeknd"}, DurationMs: 200040}
	orig := Result{Title: "Blinding Lights", Artists: []string{"The Weeknd"}, Duration: 200, IsSong: true}
	sped := Result{Title: "Blinding Lights (Sped Up)", Artists: []string{"The Weeknd"}, Duration: 190, IsSong: true}
	other := Result{Title: "Blinding Lights", Artists: []string{"Fulano"}, Duration: 260, IsSong: true}
	if so, ss := Score(tr, orig), Score(tr, sped); so <= ss || ss >= MinScore {
		t.Errorf("orig=%.2f sped=%.2f", so, ss)
	}
	if s := Score(tr, other); s != 0 {
		t.Errorf("duração muito diferente deveria zerar: %.2f", s)
	}
}

func TestScoreFeatAndAccents(t *testing.T) {
	tr := track.Track{Title: "Coração (feat. Fulana)", Artists: []string{"Cantor", "Fulana"}, DurationMs: 180000}
	r := Result{Title: "Coracao", Artists: []string{"Cantor"}, Duration: 181, IsSong: true}
	if s := Score(tr, r); s < goodEnough {
		t.Errorf("score=%.2f", s)
	}
}
