package engine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vinicgobbi/spotdown/internal/config"
	"github.com/vinicgobbi/spotdown/internal/spotify"
	"github.com/vinicgobbi/spotdown/internal/track"
)

func TestPlanDedupesAndAvoidsCollisions(t *testing.T) {
	cfg := config.Default()
	cfg.OutputDir = "/m"
	cfg.Output = "{title}"
	a := track.Track{ID: "a", Title: "Mesmo Nome"}
	b := track.Track{ID: "b", Title: "Mesmo nome"} // outra faixa, mesmo nome (sem diferenciar maiúsculas)
	lists := []*spotify.List{
		{Kind: spotify.KindPlaylist, Tracks: []track.Track{a, b}},
		{Kind: spotify.KindAlbum, Tracks: []track.Track{a}},
	}
	jobs, err := Plan(cfg, lists)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 {
		t.Fatalf("jobs=%d", len(jobs))
	}
	if jobs[0].Path != "/m/Mesmo Nome.mp3" || jobs[1].Path != "/m/Mesmo nome (2).mp3" {
		t.Errorf("paths: %q %q", jobs[0].Path, jobs[1].Path)
	}
}

func TestMoveCreatesDirs(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mp3")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "a", "b", "dst.mp3")
	if err := move(src, dst); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dst); err != nil {
		t.Error(err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Error("origem ainda existe")
	}
}
