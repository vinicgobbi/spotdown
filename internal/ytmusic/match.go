package ytmusic

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"github.com/vinicgobbi/spotdown/internal/track"
)

// MinScore é a nota mínima para aceitar um resultado.
const MinScore = 0.65

// goodEnough dispensa a segunda busca (vídeos) quando a melhor música já é boa.
const goodEnough = 0.85

// Match é o resultado escolhido e a nota (0–1) que ele recebeu.
type Match struct {
	Result
	Score float64
}

// ErrNoMatch indica que nenhum resultado passou de MinScore.
type ErrNoMatch struct {
	Query string
	Best  *Match
}

func (e *ErrNoMatch) Error() string {
	if e.Best == nil {
		return fmt.Sprintf("nenhum resultado no YouTube Music para %q", e.Query)
	}
	return fmt.Sprintf("sem correspondência confiável para %q (melhor: %q de %s, nota %.2f)",
		e.Query, e.Best.Title, strings.Join(e.Best.Artists, ", "), e.Best.Score)
}

// Find busca a faixa entre as músicas e, se nada for bom o bastante, entre os vídeos.
func (c *Client) Find(ctx context.Context, t track.Track) (Match, error) {
	query := strings.TrimSpace(t.ArtistsString() + " - " + t.Title)
	var best *Match
	for _, songs := range []bool{true, false} {
		results, err := c.Search(ctx, query, songs)
		if err != nil {
			if best != nil {
				break
			}
			return Match{}, err
		}
		for _, r := range results {
			s := Score(t, r)
			if best == nil || s > best.Score {
				best = &Match{Result: r, Score: s}
			}
		}
		if best != nil && best.Score >= goodEnough {
			break
		}
	}
	if best == nil || best.Score < MinScore {
		return Match{}, &ErrNoMatch{Query: query, Best: best}
	}
	return *best, nil
}

// Palavras que indicam outra versão da música; penalizam o resultado quando
// não aparecem no título do Spotify.
var versionWords = []string{
	"live", "ao vivo", "en vivo", "remix", "cover", "karaoke", "instrumental", "acoustic", "acustico",
	"sped up", "slowed", "nightcore", "8d", "reverb", "edit", "version", "demo", "mashup", "extended",
}

// Score avalia o quanto r corresponde a t (0–1): título, artistas e duração,
// com bônus para músicas (não vídeos) e álbum igual.
func Score(t track.Track, r Result) float64 {
	title := titleScore(t.Title, r.Title)
	artist := artistScore(t.Artists, r)
	dur := 0.7 // duração desconhecida: neutro
	if t.DurationMs > 0 && r.Duration > 0 {
		d := abs(t.Seconds() - r.Duration)
		switch {
		case d > 30:
			return 0 // outra versão (ou outra música)
		case d <= 2:
			dur = 1
		default:
			dur = max(0, 1-float64(d-2)/20)
		}
	}
	s := 0.45*title + 0.35*artist + 0.20*dur
	if r.IsSong {
		s += 0.05
	}
	if t.Album != "" && r.Album != "" && normalize(t.Album) == normalize(r.Album) {
		s += 0.05
	}
	st, rt := normalize(t.Title), normalize(r.Title)
	for _, w := range versionWords {
		if containsWord(rt, w) && !containsWord(st, w) {
			s -= 0.4
			break
		}
	}
	return min(1, max(0, s))
}

var (
	featRe   = regexp.MustCompile(`(?i)[(\[]\s*(feat|ft|with|com|part)\.?\s[^)\]]*[)\]]`)
	parensRe = regexp.MustCompile(`[(\[][^)\]]*[)\]]`)
	dashRe   = regexp.MustCompile(`\s+-\s+.*$`)
)

// core tira participações, parênteses e sufixos " - Remastered 2009".
func core(s string) string {
	s = featRe.ReplaceAllString(s, "")
	s = parensRe.ReplaceAllString(s, "")
	return dashRe.ReplaceAllString(s, "")
}

func titleScore(spot, yt string) float64 {
	best := similarity(normalize(spot), normalize(yt))
	best = max(best, similarity(normalize(core(spot)), normalize(core(yt))))
	// Vídeos costumam vir como "Artista - Título (Official Video)".
	if i := strings.Index(yt, " - "); i > 0 {
		best = max(best, similarity(normalize(core(spot)), normalize(core(yt[i+3:]))))
	}
	return best
}

func artistScore(artists []string, r Result) float64 {
	if len(artists) == 0 {
		return 0.5
	}
	ytArtists := compact(strings.Join(r.Artists, " "))
	ytTitle := compact(r.Title)
	found := 0
	mainFound := false
	for i, a := range artists {
		ca := compact(a)
		if ca == "" {
			continue
		}
		if strings.Contains(ytArtists, ca) || strings.Contains(ytTitle, ca) {
			found++
			if i == 0 {
				mainFound = true
			}
		}
	}
	s := float64(found) / float64(len(artists))
	if mainFound {
		s = max(s, 0.8)
	} else {
		// Canal com nome parecido ("RickAstleyVEVO", "Rick Astley - Topic").
		s = max(s, 0.8*similarity(compact(artists[0]), ytArtists))
	}
	return s
}

// normalize: minúsculas, sem acentos, "&" → "and", só letras/dígitos/espaços.
func normalize(s string) string {
	s = strings.ToLower(norm.NFKD.String(s))
	s = strings.ReplaceAll(s, "&", " and ")
	var b strings.Builder
	for _, r := range s {
		switch {
		case unicode.Is(unicode.Mn, r):
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		default:
			b.WriteRune(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

var channelNoise = regexp.MustCompile(`(vevo|topic|official|oficial)`)

// compact normaliza e tira espaços e ruído de nomes de canal.
func compact(s string) string {
	return channelNoise.ReplaceAllString(strings.ReplaceAll(normalize(s), " ", ""), "")
}

func containsWord(s, w string) bool {
	return strings.Contains(" "+s+" ", " "+w+" ")
}

// similarity é o maior entre a razão de Levenshtein e a sobreposição de
// palavras (Dice), para tolerar tanto erros de digitação quanto ordem trocada.
func similarity(a, b string) float64 {
	if a == "" || b == "" {
		return 0
	}
	if a == b {
		return 1
	}
	ra, rb := []rune(a), []rune(b)
	lev := 1 - float64(levenshtein(ra, rb))/float64(max(len(ra), len(rb)))

	wa, wb := strings.Fields(a), strings.Fields(b)
	set := map[string]int{}
	for _, w := range wa {
		set[w]++
	}
	common := 0
	for _, w := range wb {
		if set[w] > 0 {
			set[w]--
			common++
		}
	}
	dice := 2 * float64(common) / float64(len(wa)+len(wb))
	return max(lev, dice)
}

func levenshtein(a, b []rune) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
