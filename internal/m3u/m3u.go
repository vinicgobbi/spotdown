// Package m3u gera as playlists .m3u8 dos álbuns e playlists baixados.
// Faixas avulsas não geram playlist.
package m3u

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/vinicgobbi/spotdown/internal/pathtpl"
	"github.com/vinicgobbi/spotdown/internal/track"
)

// DefaultName é o template padrão do nome do arquivo.
const DefaultName = "{list-name}.m3u8"

// Options configura a geração.
type Options struct {
	Name     string // template do nome (variáveis de pathtpl; o normal é usar {list-name})
	BaseDir  string // pasta base para nomes relativos (a mesma das músicas)
	Restrict pathtpl.Restrict
	Absolute bool // caminhos absolutos em vez de relativos à pasta do .m3u8
}

// Entry é uma faixa da lista, na ordem original. Path vazio = não baixada.
type Entry struct {
	Track  track.Track
	Path   string
	Reason string // por que ficou de fora (sem match, erro...)
}

// List é um álbum ou playlist baixado.
type List struct {
	Name    string
	Entries []Entry
}

// Write grava um .m3u8 por lista e devolve os caminhos gravados. Listas cujo
// nome renderizado coincide (template sem {list-name}) são juntadas num
// arquivo só, como no spotdl.
func Write(lists []List, o Options) ([]string, error) {
	name := strings.TrimSpace(o.Name)
	if name == "" {
		name = DefaultName
	}
	if ext := strings.ToLower(filepath.Ext(name)); ext != ".m3u8" && ext != ".m3u" {
		name += ".m3u8"
	}

	var order []string
	merged := map[string][]Entry{}
	for _, l := range lists {
		ref := track.Track{ListName: l.Name, ListLength: len(l.Entries)}
		if len(l.Entries) > 0 {
			ref = l.Entries[0].Track
			ref.ListName, ref.ListLength = l.Name, len(l.Entries)
		}
		p, err := pathtpl.RenderList(name, ref, "m3u8", pathtpl.Options{BaseDir: o.BaseDir, Restrict: o.Restrict})
		if err != nil {
			return nil, fmt.Errorf("nome do M3U: %w", err)
		}
		if _, seen := merged[p]; !seen {
			order = append(order, p)
		}
		merged[p] = append(merged[p], l.Entries...)
	}

	var written []string
	for _, p := range order {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return written, err
		}
		if err := os.WriteFile(p, []byte(Render(merged[p], filepath.Dir(p), o.Absolute)), 0o644); err != nil {
			return written, err
		}
		written = append(written, p)
	}
	return written, nil
}

// Render monta o conteúdo do .m3u8. Faixas repetidas entram uma vez; as que
// não foram baixadas viram comentário, para dar para ver o que faltou.
func Render(entries []Entry, dir string, absolute bool) string {
	var b strings.Builder
	b.WriteString("#EXTM3U\n")
	seen := map[string]bool{}
	for _, e := range entries {
		t := e.Track
		label := strings.TrimSpace(t.ArtistsString() + " - " + t.Title)
		if e.Path == "" {
			reason := e.Reason
			if reason == "" {
				reason = "não baixada"
			}
			fmt.Fprintf(&b, "# ausente: %s (%s)\n", oneLine(label), oneLine(reason))
			continue
		}
		if seen[e.Path] {
			continue
		}
		seen[e.Path] = true
		p := e.Path
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		if !absolute {
			if absDir, err := filepath.Abs(dir); err == nil {
				if rel, err := filepath.Rel(absDir, p); err == nil {
					p = rel
				}
			}
		}
		dur := -1
		if t.DurationMs > 0 {
			dur = t.Seconds()
		}
		fmt.Fprintf(&b, "#EXTINF:%d,%s\n%s\n", dur, oneLine(label), filepath.ToSlash(p))
	}
	return b.String()
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
