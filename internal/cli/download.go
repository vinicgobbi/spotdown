package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/charmbracelet/huh"

	"github.com/vinicgobbi/spotdown/internal/config"
	"github.com/vinicgobbi/spotdown/internal/engine"
	"github.com/vinicgobbi/spotdown/internal/pathtpl"
	"github.com/vinicgobbi/spotdown/internal/spotify"
	"github.com/vinicgobbi/spotdown/internal/ui"
)

// DownloadOptions ajusta Download.
type DownloadOptions struct {
	DryRun bool // só lista os caminhos
	Choose bool // pergunta antes de baixar (todas, escolher ou cancelar)
}

// KindName devolve o nome do tipo de link em português.
func KindName(k spotify.Kind) string {
	switch k {
	case spotify.KindAlbum:
		return "Álbum"
	case spotify.KindPlaylist:
		return "Playlist"
	}
	return "Faixa"
}

// SpotifyOptions monta o cliente do Spotify conforme a fonte escolhida: o
// web player (padrão, sem credenciais) ou a API oficial com as do usuário.
func SpotifyOptions(cfg config.Config) spotify.Options {
	o := spotify.Options{CacheDir: config.CacheDir(), AlbumDetails: engine.NeedsAlbumDetails(cfg)}
	if cfg.Official() {
		o.ClientID, o.ClientSecret, o.TokenFile = cfg.SpotifyClientID, cfg.SpotifyClientSecret, config.TokenPath()
	} else {
		o.WebPlayer, o.GraphQLHash = true, cfg.SpotifyGraphQLHash
	}
	return o
}

// ValidateLinks confere um ou mais links separados por linha, espaço ou vírgula.
func ValidateLinks(s string) error {
	links := SplitLinks(s)
	if len(links) == 0 {
		return errors.New("cole ao menos um link")
	}
	for _, l := range links {
		if _, _, err := spotify.ParseURL(l); err != nil {
			return err
		}
	}
	return nil
}

// SplitLinks separa os links do texto colado.
func SplitLinks(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		return r == '\n' || r == ' ' || r == '\t' || r == ','
	})
}

// Download lê os links, monta os caminhos e baixa. Devolve o resultado da
// execução (nil no dry-run ou se nada foi baixado).
func Download(ctx context.Context, env *Env, urls []string, opt DownloadOptions) (*engine.FinishedEvent, error) {
	u, cfg := env.UI, env.Cfg
	if !opt.DryRun {
		env.CheckTools(ctx)
		if env.ToolsErr != nil {
			return nil, env.ToolsErr
		}
		if env.ToolsWarn != "" {
			u.Warn("%s", env.ToolsWarn)
		}
	}

	sp := spotify.New(SpotifyOptions(cfg))
	r := &reporter{u: u, base: pathtpl.ExpandPath(cfg.OutputDir)}
	lists := engine.Resolve(ctx, sp, urls, r.handle)
	u.Status("")
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if len(lists) == 0 {
		return nil, errors.New("nenhum link pôde ser lido")
	}
	for _, l := range lists {
		if len(l.Tracks) > 0 {
			t := l.Tracks[0]
			env.sample = &t
			break
		}
	}

	jobs, err := engine.Plan(cfg, lists)
	if err != nil {
		return nil, err
	}
	if opt.DryRun {
		u.Println()
		for _, j := range jobs {
			u.Println(r.rel(j.Path))
		}
		u.Println()
		u.Hint("%d arquivo(s) em %s", len(jobs), Tilde(r.base))
		return nil, nil
	}

	if opt.Choose && u.Interactive {
		jobs, err = choose(u, jobs, cfg)
		if err != nil {
			return nil, err
		}
	}

	u.Println()
	r.total = len(jobs)
	r.jobs = jobs
	fin := engine.NewRunner(cfg).Run(ctx, jobs, lists, r.handle)
	u.Status("")
	return &fin, ctx.Err()
}

// choose pergunta se baixa tudo, se escolhe as faixas ou se cancela.
func choose(u *ui.UI, jobs []engine.Job, cfg config.Config) ([]engine.Job, error) {
	exists := 0
	for _, j := range jobs {
		if fileExists(j.Path) {
			exists++
		}
	}
	desc := fmt.Sprintf("Destino: %s", Tilde(pathtpl.ExpandPath(cfg.OutputDir)))
	if exists > 0 {
		desc += fmt.Sprintf("\n%d já existe(m) no disco e vão ser tratadas como %q.", exists, cfg.Overwrite)
	}
	what := "all"
	if err := u.Run(huh.NewGroup(huh.NewSelect[string]().
		Title(fmt.Sprintf("%d faixa(s) para baixar", len(jobs))).
		Description(desc).
		Options(
			huh.NewOption(fmt.Sprintf("Baixar todas (%d)", len(jobs)), "all"),
			huh.NewOption("Escolher as faixas", "pick"),
			huh.NewOption("Cancelar", "cancel"),
		).Value(&what))); err != nil {
		return nil, err
	}
	switch what {
	case "cancel":
		return nil, ui.ErrAborted
	case "all":
		return jobs, nil
	}

	opts := make([]huh.Option[int], len(jobs))
	for i, j := range jobs {
		label := j.Track.ArtistsString() + " - " + j.Track.Title
		if fileExists(j.Path) {
			label += "  ●"
		}
		opts[i] = huh.NewOption(label, i).Selected(true)
	}
	var picked []int
	if err := u.Run(huh.NewGroup(huh.NewMultiSelect[int]().
		Title("Faixas").
		Description("espaço marca/desmarca · / filtra · ● já existe").
		Options(opts...).Filterable(true).Height(min(len(jobs)+3, 16)).
		Validate(func(s []int) error {
			if len(s) == 0 {
				return errors.New("marque ao menos uma faixa")
			}
			return nil
		}).
		Value(&picked))); err != nil {
		return nil, err
	}
	sort.Ints(picked)
	out := make([]engine.Job, 0, len(picked))
	for _, i := range picked {
		out = append(out, jobs[i])
	}
	return out, nil
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// reporter imprime os eventos do engine e mantém a linha de status.
type reporter struct {
	mu    sync.Mutex
	u     *ui.UI
	base  string
	total int
	done  int
	jobs  []engine.Job

	active []int
	stage  map[int]string
	frac   map[int]float64
}

func (r *reporter) rel(p string) string {
	if rel, err := filepath.Rel(r.base, p); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return p
}

func (r *reporter) handle(ev any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	u := r.u
	switch ev := ev.(type) {
	case engine.ResolveStartEvent:
		u.Println(u.Faint("→ " + ev.URL))
	case engine.ResolveProgressEvent:
		u.Status("Lendo os detalhes das faixas no Spotify: %d/%d", ev.Done, ev.Total)
	case engine.ResolveDoneEvent:
		if ev.Err != nil {
			u.Fail("%v", ev.Err)
			return
		}
		l := ev.List
		u.OK("%s %s: %d faixa(s)", KindName(l.Kind), l.Name, len(l.Tracks))
		for _, w := range l.Warnings {
			u.Warn("%s", w)
		}
	case engine.WarningEvent:
		u.Warn("%s", ev.Msg)
	case engine.JobStartEvent:
		if r.stage == nil {
			r.stage, r.frac = map[int]string{}, map[int]float64{}
		}
		r.active = append(r.active, ev.Index)
		r.status()
	case engine.JobStageEvent:
		r.stage[ev.Index] = ev.Stage
		r.status()
	case engine.JobProgressEvent:
		r.frac[ev.Index] = ev.Frac
		r.status()
	case engine.JobDoneEvent:
		r.done++
		for k, v := range r.active {
			if v == ev.Index {
				r.active = append(r.active[:k], r.active[k+1:]...)
				break
			}
		}
		r.line(ev.Result)
		r.status()
	case engine.FinishedEvent:
		u.Status("")
		u.Println()
		summary(u, ev)
	}
}

func (r *reporter) line(res engine.Result) {
	u := r.u
	t := res.Job.Track
	name := t.ArtistsString() + " - " + t.Title
	n := u.Faint(fmt.Sprintf("[%d/%d]", r.done, r.total))
	switch res.Status {
	case engine.Downloaded:
		extra := ""
		if res.Lyrics {
			extra = " ♪"
		}
		u.Printf("%s %s %s%s\n", n, u.Tone(ui.Good, "✓"), name, u.Faint(extra+"  → "+r.rel(res.Job.Path)))
	case engine.Skipped:
		u.Printf("%s %s\n", n, u.Faint("· "+name+" (já existe)"))
	case engine.Retagged:
		u.Printf("%s %s %s%s\n", n, u.Tone(ui.Good, "↻"), name, u.Faint(" (tags regravadas)"))
	case engine.NoMatch:
		u.Printf("%s %s %s%s\n", n, u.Tone(ui.Bad, "?"), name, u.Faint(" — sem correspondência no YouTube Music"))
	case engine.Canceled:
		u.Printf("%s %s\n", n, u.Faint("- "+name+" (cancelada)"))
	default:
		msg := ""
		if res.Err != nil {
			msg = " — " + res.Err.Error()
		}
		u.Printf("%s %s %s%s\n", n, u.Tone(ui.Bad, "✗"), name, u.Faint(msg))
	}
}

// status mostra o andamento: total e as faixas em curso (até 3).
func (r *reporter) status() {
	if r.u.Host == nil || r.total == 0 {
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Baixando %d/%d", r.done, r.total)
	for k, i := range r.active {
		if k == 3 {
			fmt.Fprintf(&b, "\n  … e mais %d", len(r.active)-3)
			break
		}
		st := r.stage[i]
		if st == "" {
			st = "iniciando"
		}
		if st == "baixando" {
			st = fmt.Sprintf("baixando %.0f%%", r.frac[i]*100)
		}
		t := r.jobs[i].Track
		fmt.Fprintf(&b, "\n  %s — %s", t.ArtistsString()+" - "+t.Title, st)
	}
	r.u.Status("%s", b.String())
}

// summary resume a execução.
func summary(u *ui.UI, f engine.FinishedEvent) {
	counts := map[engine.Status]int{}
	for _, r := range f.Results {
		counts[r.Status]++
	}
	s := fmt.Sprintf("%d baixada(s), %d já existia(m), %d com tags regravadas, %d sem correspondência, %d com erro",
		counts[engine.Downloaded], counts[engine.Skipped], counts[engine.Retagged], counts[engine.NoMatch], counts[engine.Failed])
	if n := counts[engine.Canceled]; n > 0 {
		s += fmt.Sprintf(", %d cancelada(s)", n)
	}
	s += fmt.Sprintf(" em %s", f.Elapsed.Round(1e9))
	if counts[engine.NoMatch]+counts[engine.Failed]+counts[engine.Canceled] == 0 {
		u.OK("%s", s)
	} else {
		u.Warn("%s", s)
	}
	for _, m := range f.M3U {
		u.Hint("M3U: %s", Tilde(m))
	}
	if f.Failures != "" {
		u.Hint("Faixas que faltaram: %s", Tilde(f.Failures))
	}
}

// Failed informa se alguma faixa não está no disco ao final.
func Failed(f *engine.FinishedEvent) bool {
	if f == nil {
		return false
	}
	for _, r := range f.Results {
		if !r.Status.OK() {
			return true
		}
	}
	return false
}
