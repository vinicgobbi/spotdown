package update

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vinicgobbi/spotdown/internal/ui"
)

const changelog = `## v0.13.0 (2026-10-16)

### Feat

- **dev**: cadastra instâncias e escolhe a forja no init
- **dev**: gera o workflow de deploy para Gitea e Forgejo

## v0.12.1 (2026-10-16)

### Fix

- deploy tenta de novo quando a conexão SSH falha

## v0.12.0 (2026-10-16)

### BREAKING CHANGE

- o deploy-ci exige o release id com timestamp
  no começo do nome

### Feat

- **tui**: menu separado por escopo

## v0.11.1 (2026-10-15)

### Fix

- **deploy-dev**: check sem o prefixo .:0:
`

func TestParseChangelog(t *testing.T) {
	notes := ParseChangelog(changelog, "v0.11.1", "v0.13.0")
	var vs []string
	for _, n := range notes {
		vs = append(vs, n.Version)
	}
	if strings.Join(vs, ",") != "v0.13.0,v0.12.1,v0.12.0" {
		t.Fatalf("versões = %v", vs)
	}
	if n := notes[0]; n.Date != "2026-10-16" || len(n.Sections) != 1 || n.Sections[0].Title != "Feat" || len(n.Sections[0].Items) != 2 {
		t.Errorf("v0.13.0 = %+v", n)
	}
	if it := notes[2].Sections[0].Items[0]; it != "o deploy-ci exige o release id com timestamp no começo do nome" {
		t.Errorf("item de várias linhas = %q", it)
	}
	// Only up to latest, and a dev build gets just latest's notes.
	if got := ParseChangelog(changelog, "v0.11.1", "v0.12.0"); len(got) != 1 || got[0].Version != "v0.12.0" {
		t.Errorf("até v0.12.0 = %+v", got)
	}
	if got := ParseChangelog(changelog, "dev", "v0.13.0"); len(got) != 1 || got[0].Version != "v0.13.0" {
		t.Errorf("dev = %+v", got)
	}
}

func TestShowTranslatesAndCaps(t *testing.T) {
	var out bytes.Buffer
	u := &ui.UI{Out: &out}
	notes := ParseChangelog(changelog, "v0.11.1", "v0.13.0")
	Show(u, notes, 0)
	all := out.String()
	for _, want := range []string{"v0.13.0", "Novidades", "• dev: cadastra instâncias", "Correções", "Mudanças incompatíveis"} {
		if !strings.Contains(all, want) {
			t.Errorf("faltou %q:\n%s", want, all)
		}
	}
	if strings.Contains(all, "**") {
		t.Errorf("markdown cru:\n%s", all)
	}
	out.Reset()
	Show(u, notes, 2)
	if got := out.String(); strings.Contains(got, "v0.12.0") || !strings.Contains(got, "e mais 3 mudança(s)") {
		t.Errorf("limite de 2:\n%s", got)
	}
}

func TestChangesFetchesTheLatestTag(t *testing.T) {
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		fmt.Fprint(w, changelog)
	}))
	defer srv.Close()
	c := Client{URL: srv.URL + "/latest", ChangelogURL: srv.URL + "/%s/CHANGELOG.md"}
	notes, err := c.Changes(context.Background(), "v0.12.1", "v0.13.0")
	if err != nil || len(notes) != 1 || path != "/v0.13.0/CHANGELOG.md" {
		t.Fatalf("notes = %+v, err = %v, path = %s", notes, err, path)
	}
	// A custom release URL without a changelog URL fetches nothing.
	if notes, err := (Client{URL: srv.URL}).Changes(context.Background(), "v0.12.1", "v0.13.0"); notes != nil || err != nil {
		t.Fatalf("sem ChangelogURL: %+v %v", notes, err)
	}
	// A failed download is a hint, never an error for the caller.
	var out bytes.Buffer
	c.ChangelogURL = srv.URL + "/missing/%s"
	srv.Config.Handler = http.NotFoundHandler()
	c.ShowChanges(context.Background(), &ui.UI{Out: &out}, "v0.12.1", "v0.13.0", 0)
	if !strings.Contains(out.String(), "Não consegui baixar as notas") {
		t.Fatalf("saída: %s", out.String())
	}
}
