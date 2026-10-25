package update

import (
	"bufio"
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/vinicgobbi/spotdown/internal/ui"
)

// ChangelogURL é o CHANGELOG.md (escrito pelo `cz bump`) numa tag (%s). Ele
// acumula todas as versões, então um download cobre qualquer salto.
const ChangelogURL = "https://raw.githubusercontent.com/vinicgobbi/spotdown/%s/CHANGELOG.md"

// ReleasesURL é onde ficam as notas completas, para quando o resumo é cortado.
const ReleasesURL = "https://github.com/vinicgobbi/spotdown/releases"

// Note é a seção de uma versão no changelog.
type Note struct {
	Version, Date string
	Sections      []NoteSection
}

type NoteSection struct {
	Title string // como o Commitizen escreve: Feat, Fix, BREAKING CHANGE...
	Items []string
}

var versionHeadRe = regexp.MustCompile(`^## (v\d+\.\d+\.\d+)\s*(?:\(([^)]*)\))?`)

// ParseChangelog devolve as notas das versões depois de current até latest
// (as duas "vX.Y.Z"), da mais nova para a mais velha. Um current que não é
// versão (build dev) recebe só as notas de latest.
func ParseChangelog(text, current, latest string) []Note {
	var notes []Note
	var note *Note
	var sec *NoteSection
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), " \t\r")
		if m := versionHeadRe.FindStringSubmatch(line); m != nil {
			note, sec = nil, nil
			if inRange(m[1], current, latest) {
				notes = append(notes, Note{Version: m[1], Date: m[2]})
				note = &notes[len(notes)-1]
			}
			continue
		}
		if note == nil {
			continue
		}
		switch {
		case strings.HasPrefix(line, "### "):
			note.Sections = append(note.Sections, NoteSection{Title: strings.TrimSpace(line[4:])})
			sec = &note.Sections[len(note.Sections)-1]
		case strings.HasPrefix(line, "- ") && sec != nil:
			sec.Items = append(sec.Items, strings.TrimSpace(line[2:]))
		case strings.HasPrefix(line, "  ") && sec != nil && len(sec.Items) > 0 && strings.TrimSpace(line) != "":
			// Continuação de um item de várias linhas (ex.: corpo de um BREAKING CHANGE).
			sec.Items[len(sec.Items)-1] += " " + strings.TrimSpace(line)
		}
	}
	return notes
}

func inRange(v, current, latest string) bool {
	if c, ok := Compare(v, latest); !ok || c > 0 {
		return false
	}
	if c, ok := Compare(v, current); ok {
		return c > 0
	}
	return v == latest
}

// Changes baixa o changelog de latest e devolve o que mudou desde current.
// Com URL própria (testes, espelhos) e sem ChangelogURL, não há o que
// baixar: devolve nil.
func (c Client) Changes(ctx context.Context, current, latest string) ([]Note, error) {
	tmpl := c.ChangelogURL
	if tmpl == "" {
		if c.URL != "" {
			return nil, nil
		}
		tmpl = ChangelogURL
	}
	b, err := c.get(ctx, fmt.Sprintf(tmpl, latest), 4<<20)
	if err != nil {
		return nil, err
	}
	return ParseChangelog(string(b), current, latest), nil
}

var sectionNames = map[string]string{
	"Feat":            "Novidades",
	"Fix":             "Correções",
	"Refactor":        "Refatoração",
	"Perf":            "Desempenho",
	"BREAKING CHANGE": "Mudanças incompatíveis",
}

var boldScopeRe = regexp.MustCompile(`^\*\*([^*]+)\*\*:\s*`)

// Show imprime as notas, da mais nova para a mais velha, em no máximo
// maxLines itens (0 = sem limite); o resto aponta para a página de releases.
func Show(u *ui.UI, notes []Note, maxLines int) {
	shown, total := 0, 0
	for _, n := range notes {
		for _, s := range n.Sections {
			total += len(s.Items)
		}
	}
	for _, n := range notes {
		if maxLines > 0 && shown >= maxLines {
			break
		}
		head := n.Version
		if n.Date != "" {
			head += "  " + u.Faint(n.Date)
		}
		u.Println(head)
		for _, s := range n.Sections {
			if maxLines > 0 && shown >= maxLines {
				break
			}
			title, ok := sectionNames[s.Title]
			if !ok {
				title = s.Title
			}
			if s.Title == "BREAKING CHANGE" {
				u.Println("  " + u.Tone(ui.Bad, title))
			} else {
				u.Println("  " + title)
			}
			for _, it := range s.Items {
				if maxLines > 0 && shown >= maxLines {
					break
				}
				u.Println("    • " + boldScopeRe.ReplaceAllString(it, "$1: "))
				shown++
			}
		}
	}
	if shown < total {
		u.Println(u.Faint(fmt.Sprintf("  … e mais %d mudança(s): %s", total-shown, ReleasesURL)))
	}
}

// ShowChanges baixa e imprime o que mudou de current para latest, com um
// título. Nunca falha: sem notas não imprime nada, e erro de download vira dica.
func (c Client) ShowChanges(ctx context.Context, u *ui.UI, current, latest string, maxLines int) {
	notes, err := c.Changes(ctx, current, latest)
	if err != nil {
		u.Hint("Não consegui baixar as notas da versão (%v); veja em %s", err, ReleasesURL)
		return
	}
	if len(notes) == 0 {
		return
	}
	title := "O que muda em " + latest
	if len(notes) > 1 {
		title = "O que muda de " + current + " para " + latest
	}
	u.Title(title)
	Show(u, notes, maxLines)
	u.Println()
}
