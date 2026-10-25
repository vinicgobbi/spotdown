package tag

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vinicgobbi/spotdown/internal/track"
)

func TestEscape(t *testing.T) {
	if got := escape("a=b;c#d\\e\nf"); got != "a\\=b\\;c\\#d\\\\e\\\nf" {
		t.Errorf("got %q", got)
	}
}

func cover(t *testing.T) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for i := range img.Pix {
		img.Pix[i] = 200
	}
	img.Set(0, 0, color.Black)
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// TestWriteFormats gera 1 s de áudio em cada formato, grava as tags e confere
// com o ffprobe que título, artista, faixa, letra e capa chegaram ao arquivo.
func TestWriteFormats(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg ausente")
	}
	codecs := map[string][]string{
		"mp3":  {"-c:a", "libmp3lame"},
		"m4a":  {"-c:a", "aac"},
		"flac": {"-c:a", "flac"},
		"opus": {"-c:a", "libopus"},
		"ogg":  {"-c:a", "libopus"},
	}
	m := Meta{
		Track: track.Track{
			ID: "4cOdK2wGLETKBW3PvgPWqT", Title: "Título = com; símbolos", Artists: []string{"Artista Um", "Dois"},
			Album: "Álbum", TrackNumber: 3, TracksCount: 10, DiscNumber: 1, DiscCount: 1, Date: "1987-11-12",
			Publisher: "Gravadora", ISRC: "GBARL9300135", DurationMs: 1000,
		},
		Cover:  cover(t),
		Lyrics: "[00:00.10] primeira\n[00:00.50] segunda",
		Source: "https://music.youtube.com/watch?v=x",
	}
	for ext, codec := range codecs {
		t.Run(ext, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "a."+ext)
			args := append([]string{"-v", "error", "-f", "lavfi", "-i", "sine=duration=1"}, codec...)
			if b, err := exec.Command("ffmpeg", append(args, path)...).CombinedOutput(); err != nil {
				t.Skipf("codec indisponível: %s", b)
			}
			// Duas vezes: regravar (modo overwrite=metadata) não pode duplicar a capa.
			for range 2 {
				if err := Write(context.Background(), path, m); err != nil {
					t.Fatal(err)
				}
			}
			out, err := exec.Command("ffprobe", "-v", "error", "-show_entries",
				"stream=codec_type:stream_tags:format_tags", "-of", "default=nw=1", path).CombinedOutput()
			if err != nil {
				t.Fatalf("%v: %s", err, out)
			}
			s := strings.ToLower(string(out))
			for _, want := range []string{"título = com; símbolos", "artista um", "álbum", "3/10", "primeira"} {
				if !strings.Contains(s, want) {
					t.Errorf("falta %q em:\n%s", want, out)
				}
			}
			if n := strings.Count(s, "codec_type=video"); n != 1 {
				t.Errorf("esperava 1 capa, achei %d:\n%s", n, out)
			}
		})
	}
}
