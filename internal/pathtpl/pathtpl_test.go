package pathtpl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vinicgobbi/spotdown/internal/track"
)

var sample = track.Track{
	ID:           "4cOdK2wGLETKBW3PvgPWqT",
	Title:        "Never Gonna Give You Up",
	Artists:      []string{"Rick Astley", "Convidado"},
	Album:        "Whenever You Need Somebody",
	AlbumArtist:  "Rick Astley",
	DiscNumber:   1,
	DiscCount:    2,
	TrackNumber:  3,
	TracksCount:  10,
	DurationMs:   213573,
	Date:         "1987-11-12",
	ISRC:         "GBARL9300135",
	Publisher:    "BMG",
	ListName:     "Minhas: Favoritas",
	ListPosition: 7,
	ListLength:   120,
}

func TestRender(t *testing.T) {
	cases := []struct {
		tpl, want string
	}{
		{"{artists} - {title}.{output-ext}", "Rick Astley, Convidado - Never Gonna Give You Up.mp3"},
		{"{artist}/{album}/{track-number:02} - {title}", "Rick Astley/Whenever You Need Somebody/03 - Never Gonna Give You Up.mp3"},
		{"{list-name}/{list-position:03} {title}.{output-ext}", "Minhas- Favoritas/007 Never Gonna Give You Up.mp3"},
		{"{year}/", "1987/Rick Astley, Convidado - Never Gonna Give You Up.mp3"},
		{"", "Rick Astley, Convidado - Never Gonna Give You Up.mp3"},
		{"{album-artist}/{disc-number}-{disc-count}/{tracks-count}/{duration}/{isrc}/{track-id}/{publisher}/{original-date}/{list-length}",
			"Rick Astley/1-2/10/214/GBARL9300135/4cOdK2wGLETKBW3PvgPWqT/BMG/1987-11-12/120.mp3"},
		// Variável vazia não deixa pasta vazia.
		{"{genre}/{title}", "Never Gonna Give You Up.mp3"},
	}
	for _, c := range cases {
		got, err := Render(c.tpl, sample, "mp3", Options{})
		if err != nil {
			t.Fatalf("%q: %v", c.tpl, err)
		}
		if got != c.want {
			t.Errorf("%q:\n got %q\nwant %q", c.tpl, got, c.want)
		}
	}
}

func TestRenderSanitizes(t *testing.T) {
	tr := track.Track{Title: `Que? <Isso> "é" | *bom*`, Artists: []string{"AC/DC"}, Album: "...Ponto. "}
	got, err := Render("{artist}/{album}/{title}", tr, "opus", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if want := "AC-DC/Ponto/Que Isso é bom.opus"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRestrict(t *testing.T) {
	tr := track.Track{Title: "Ação & Reação", Artists: []string{"Björk"}}
	got, _ := Render("{artist} - {title}", tr, "mp3", Options{Restrict: RestrictASCII})
	if want := "Bjork - Acao & Reacao.mp3"; got != want {
		t.Errorf("ascii: got %q, want %q", got, want)
	}
	got, _ = Render("{artist} - {title}", tr, "mp3", Options{Restrict: RestrictStrict})
	if want := "Bjork_-_Acao_Reacao.mp3"; got != want {
		t.Errorf("strict: got %q, want %q", got, want)
	}
}

func TestBaseDirAndHome(t *testing.T) {
	got, _ := Render("{title}", sample, "mp3", Options{BaseDir: "/musica"})
	if want := "/musica/Never Gonna Give You Up.mp3"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	got, _ = Render("/abs/{title}", sample, "mp3", Options{BaseDir: "/musica"})
	if want := "/abs/Never Gonna Give You Up.mp3"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	home, _ := os.UserHomeDir()
	got, _ = Render("~/M/{title}", sample, "mp3", Options{})
	if want := filepath.Join(home, "M", "Never Gonna Give You Up.mp3"); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestLongNameKeepsExtension(t *testing.T) {
	tr := track.Track{Title: strings.Repeat("ã", 200)}
	got, _ := Render("{title}", tr, "flac", Options{})
	if len(got) > maxComponent || !strings.HasSuffix(got, ".flac") {
		t.Errorf("len=%d got %q", len(got), got[len(got)-10:])
	}
}

func TestValidate(t *testing.T) {
	if err := Validate("{artista}/{title}"); err == nil {
		t.Error("esperava erro para variável desconhecida")
	}
	if err := Validate("{track-number:2}"); err == nil {
		t.Error("esperava erro para formato inválido")
	}
	if err := Validate("{track-number:02}/{title}"); err != nil {
		t.Error(err)
	}
}
