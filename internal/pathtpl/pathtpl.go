// Package pathtpl monta o caminho de saída a partir de um template no estilo
// do spotdl, por exemplo "{artist}/{album}/{track-number:02} - {title}.{output-ext}".
package pathtpl

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"github.com/vinicgobbi/spotdown/internal/track"
)

// Restrict controla quais caracteres sobram nos nomes, como o --restrict do spotdl.
type Restrict string

const (
	RestrictNone   Restrict = "none"   // só remove o que o sistema de arquivos não aceita
	RestrictASCII  Restrict = "ascii"  // translitera para ASCII ("Ação" → "Acao")
	RestrictStrict Restrict = "strict" // ASCII, sem espaços e só [A-Za-z0-9._-]
)

// DefaultFile é o nome usado quando o template termina em "/" (como no spotdl).
const DefaultFile = "{artists} - {title}.{output-ext}"

// maxComponent é o limite de bytes por componente na maioria dos sistemas de arquivos.
const maxComponent = 255

// Options ajusta a renderização.
type Options struct {
	BaseDir  string // base para templates relativos
	Restrict Restrict
}

// Var descreve uma variável aceita no template.
type Var struct {
	Name string
	Desc string
}

// Vars lista as variáveis aceitas, na ordem em que aparecem na ajuda.
var Vars = []Var{
	{"title", "título da faixa"},
	{"artists", "todos os artistas (A, B, C)"},
	{"artist", "artista principal"},
	{"album", "nome do álbum"},
	{"album-artist", "artista do álbum"},
	{"genre", "gênero (raramente disponível)"},
	{"disc-number", "número do disco"},
	{"disc-count", "quantidade de discos"},
	{"duration", "duração em segundos"},
	{"year", "ano de lançamento"},
	{"original-date", "data de lançamento (AAAA-MM-DD)"},
	{"track-number", "número da faixa no álbum"},
	{"tracks-count", "faixas no álbum"},
	{"isrc", "código ISRC"},
	{"track-id", "ID da faixa no Spotify"},
	{"publisher", "gravadora/editora"},
	{"list-length", "faixas na playlist/álbum baixado"},
	{"list-position", "posição na playlist/álbum baixado"},
	{"list-name", "nome da playlist/álbum baixado"},
	{"output-ext", "extensão do arquivo (mp3, m4a...)"},
}

var placeholder = regexp.MustCompile(`\{([a-z-]+)(?::([^}]*))?\}`)

// values devolve o valor bruto de cada variável para a faixa.
func values(t track.Track, ext string) map[string]string {
	num := func(n int) string {
		if n <= 0 {
			return ""
		}
		return strconv.Itoa(n)
	}
	albumArtist := t.AlbumArtist
	if albumArtist == "" {
		albumArtist = t.Artist()
	}
	dur := ""
	if t.DurationMs > 0 {
		dur = strconv.Itoa(t.Seconds())
	}
	return map[string]string{
		"title":         t.Title,
		"artists":       t.ArtistsString(),
		"artist":        t.Artist(),
		"album":         t.Album,
		"album-artist":  albumArtist,
		"genre":         t.Genre,
		"disc-number":   num(t.DiscNumber),
		"disc-count":    num(t.DiscCount),
		"duration":      dur,
		"year":          t.Year(),
		"original-date": t.Date,
		"track-number":  num(t.TrackNumber),
		"tracks-count":  num(t.TracksCount),
		"isrc":          t.ISRC,
		"track-id":      t.ID,
		"publisher":     t.Publisher,
		"list-length":   num(t.ListLength),
		"list-position": num(t.ListPosition),
		"list-name":     t.ListName,
		"output-ext":    ext,
	}
}

// Normalize completa o template como o spotdl: termina em "/" (ou vazio) →
// acrescenta DefaultFile; sem {output-ext} → acrescenta ".{output-ext}".
func Normalize(tpl string) string {
	tpl = strings.TrimSpace(tpl)
	if tpl == "" || strings.HasSuffix(tpl, "/") {
		tpl += DefaultFile
	}
	if !strings.Contains(tpl, "{output-ext}") {
		tpl += ".{output-ext}"
	}
	return tpl
}

// Validate confere se todas as variáveis do template existem e se os
// formatos (":02") são válidos.
func Validate(tpl string) error {
	known := values(track.Track{}, "")
	for _, m := range placeholder.FindAllStringSubmatch(tpl, -1) {
		if _, ok := known[m[1]]; !ok {
			return fmt.Errorf("variável desconhecida {%s}", m[1])
		}
		if _, err := parseSpec(m[2]); err != nil {
			return fmt.Errorf("{%s:%s}: %w", m[1], m[2], err)
		}
	}
	return nil
}

// parseSpec interpreta o formato após ":" — hoje só "0N" (zeros à esquerda).
func parseSpec(spec string) (int, error) {
	if spec == "" {
		return 0, nil
	}
	if len(spec) < 2 || spec[0] != '0' {
		return 0, fmt.Errorf("formato inválido (use :02, :03...)")
	}
	n, err := strconv.Atoi(spec[1:])
	if err != nil || n < 1 || n > 10 {
		return 0, fmt.Errorf("formato inválido (use :02, :03...)")
	}
	return n, nil
}

// Render aplica o template à faixa e devolve o caminho final do arquivo.
func Render(tpl string, t track.Track, ext string, o Options) (string, error) {
	return render(Normalize(tpl), values(t, ext), o)
}

// RenderList aplica um template que só usa as variáveis de lista (nome do
// M3U, por exemplo). As demais variáveis vêm da faixa de referência.
func RenderList(tpl string, ref track.Track, ext string, o Options) (string, error) {
	return render(strings.TrimSpace(tpl), values(ref, ext), o)
}

func render(tpl string, vals map[string]string, o Options) (string, error) {
	if err := Validate(tpl); err != nil {
		return "", err
	}
	tpl = expandHome(os.ExpandEnv(tpl))
	// Decidido pelo template: uma primeira variável vazia não torna o caminho absoluto.
	abs := strings.HasPrefix(tpl, "/")

	out := placeholder.ReplaceAllStringFunc(tpl, func(s string) string {
		m := placeholder.FindStringSubmatch(s)
		v := vals[m[1]]
		if pad, _ := parseSpec(m[2]); pad > 0 && v != "" {
			if _, err := strconv.Atoi(v); err == nil {
				v = strings.Repeat("0", max(0, pad-len(v))) + v
			}
		}
		return cleanValue(v)
	})

	parts := strings.Split(out, "/")
	clean := make([]string, 0, len(parts))
	for i, p := range parts {
		p = cleanComponent(p, o.Restrict, i == len(parts)-1)
		if p == "" || p == "." {
			continue
		}
		clean = append(clean, p)
	}
	if len(clean) == 0 {
		return "", fmt.Errorf("o template gerou um caminho vazio")
	}
	path := filepath.Join(clean...)
	if abs {
		return "/" + path, nil
	}
	if o.BaseDir != "" {
		path = filepath.Join(expandHome(o.BaseDir), path)
	}
	return path, nil
}

// ExpandPath expande variáveis de ambiente e "~" no início de p.
func ExpandPath(p string) string { return expandHome(os.ExpandEnv(p)) }

// expandHome troca "~" no início pelo diretório do usuário.
func expandHome(p string) string {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return home + p[1:]
}

// cleanValue tira de um valor substituído o que não pode ir num nome de
// arquivo, inclusive "/" — senão "AC/DC" viraria uma pasta.
func cleanValue(v string) string {
	var b strings.Builder
	for _, r := range v {
		switch {
		case r == '/' || r == '\\':
			b.WriteRune('-')
		case r == ':':
			b.WriteRune('-')
		case strings.ContainsRune(`*?"<>|`, r):
		case unicode.IsControl(r):
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// cleanComponent aplica a restrição de caracteres, remove espaços/pontos nas
// pontas, colapsa espaços repetidos e limita o tamanho preservando a extensão.
func cleanComponent(p string, r Restrict, last bool) string {
	switch r {
	case RestrictASCII:
		p = toASCII(p)
	case RestrictStrict:
		p = strict(toASCII(p))
	}
	p = strings.Join(strings.Fields(p), " ")
	p = strings.TrimRight(strings.TrimSpace(p), ". ")
	if strings.HasPrefix(p, "..") {
		p = strings.TrimLeft(p, ".")
	}
	if len(p) <= maxComponent {
		return p
	}
	ext := ""
	if last {
		ext = filepath.Ext(p)
		if len(ext) > 10 {
			ext = ""
		}
	}
	base := p[:len(p)-len(ext)]
	cut := maxComponent - len(ext)
	for cut > 0 && !utf8.RuneStart(base[cut]) {
		cut--
	}
	return strings.TrimRight(base[:cut], ". ") + ext
}

// toASCII remove acentos (decompõe e descarta as marcas) e o que mais não for ASCII.
func toASCII(s string) string {
	var b strings.Builder
	for _, r := range norm.NFKD.String(s) {
		switch {
		case unicode.Is(unicode.Mn, r):
		case r < utf8.RuneSelf:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func strict(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			b.WriteRune(r)
		case unicode.IsSpace(r):
			b.WriteRune('_')
		}
	}
	out := b.String()
	for strings.Contains(out, "__") {
		out = strings.ReplaceAll(out, "__", "_")
	}
	return strings.Trim(out, "_")
}
