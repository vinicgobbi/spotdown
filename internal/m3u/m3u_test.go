package m3u

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vinicgobbi/spotdown/internal/track"
)

func TestWrite(t *testing.T) {
	dir := t.TempDir()
	a := track.Track{Title: "Um", Artists: []string{"A"}, DurationMs: 61000}
	b := track.Track{Title: "Dois", Artists: []string{"B", "C"}, DurationMs: 120400}
	lists := []List{
		{Name: "Minha/Playlist", Entries: []Entry{
			{Track: a, Path: filepath.Join(dir, "A", "Um.mp3")},
			{Track: b, Reason: "sem correspondência"},
			{Track: a, Path: filepath.Join(dir, "A", "Um.mp3")},
		}},
		{Name: "Álbum", Entries: []Entry{{Track: b, Path: filepath.Join(dir, "B", "Dois.mp3")}}},
	}
	paths, err := Write(lists, Options{Name: "playlists/{list-name}", BaseDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 || paths[0] != filepath.Join(dir, "playlists", "Minha-Playlist.m3u8") {
		t.Fatalf("paths=%q", paths)
	}
	got, _ := os.ReadFile(paths[0])
	want := "#EXTM3U\n#EXTINF:61,A - Um\n../A/Um.mp3\n# ausente: B, C - Dois (sem correspondência)\n"
	if string(got) != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestMergeWhenNameHasNoListVar(t *testing.T) {
	dir := t.TempDir()
	lists := []List{
		{Name: "L1", Entries: []Entry{{Track: track.Track{Title: "x"}, Path: filepath.Join(dir, "x.mp3")}}},
		{Name: "L2", Entries: []Entry{{Track: track.Track{Title: "y"}, Path: filepath.Join(dir, "y.mp3")}}},
	}
	paths, err := Write(lists, Options{Name: "tudo.m3u", BaseDir: dir, Absolute: true})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(paths[0])
	if len(paths) != 1 || !strings.Contains(string(got), filepath.Join(dir, "x.mp3")) || !strings.Contains(string(got), filepath.Join(dir, "y.mp3")) {
		t.Errorf("paths=%q\n%s", paths, got)
	}
}
