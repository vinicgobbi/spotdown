// Package engine executa o pipeline (Spotify → YouTube Music → yt-dlp →
// tags → caminho final) com vários downloads simultâneos e emite eventos
// para a interface (TUI ou texto).
package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/vinicgobbi/spotdown/internal/config"
	"github.com/vinicgobbi/spotdown/internal/download"
	"github.com/vinicgobbi/spotdown/internal/lyrics"
	"github.com/vinicgobbi/spotdown/internal/m3u"
	"github.com/vinicgobbi/spotdown/internal/pathtpl"
	"github.com/vinicgobbi/spotdown/internal/spotify"
	"github.com/vinicgobbi/spotdown/internal/tag"
	"github.com/vinicgobbi/spotdown/internal/track"
	"github.com/vinicgobbi/spotdown/internal/ytmusic"
)

// Status é a situação de uma faixa.
type Status int

const (
	Pending    Status = iota
	Running           // em andamento (ver Stage)
	Downloaded        // baixada agora
	Skipped           // já existia (overwrite=skip)
	Retagged          // já existia, tags regravadas (overwrite=metadata)
	NoMatch           // nada confiável no YouTube Music
	Failed
	Canceled
)

// OK informa se a faixa está no disco ao final.
func (s Status) OK() bool { return s == Downloaded || s == Skipped || s == Retagged }

// Job é uma faixa a processar, já com o caminho final.
type Job struct {
	Track track.Track
	Path  string
}

// Result é o desfecho de um Job.
type Result struct {
	Job    Job
	Status Status
	Err    error
	Match  *ytmusic.Match
	Lyrics bool // letra encontrada e gravada
}

// Eventos enviados para a interface.
type (
	ResolveStartEvent    struct{ URL string }
	ResolveProgressEvent struct {
		URL         string
		Done, Total int
	}
	ResolveDoneEvent struct {
		URL  string
		List *spotify.List
		Err  error
	}
	JobStartEvent struct{ Index int }
	JobStageEvent struct {
		Index int
		Stage string
	}
	JobProgressEvent struct {
		Index int
		Frac  float64
	}
	JobDoneEvent struct {
		Index  int
		Result Result
	}
	WarningEvent  struct{ Msg string }
	FinishedEvent struct {
		Results  []Result
		M3U      []string
		Failures string // arquivo com a lista de falhas ("" se não houve)
		Elapsed  time.Duration
	}
)

// Resolve busca os metadados de cada link, um de cada vez (cada link já
// paraleliza as próprias faixas). Links com erro são informados e pulados.
func Resolve(ctx context.Context, sp *spotify.Client, urls []string, emit func(any)) []*spotify.List {
	var lists []*spotify.List
	for _, u := range urls {
		if ctx.Err() != nil {
			break
		}
		emit(ResolveStartEvent{URL: u})
		l, err := sp.Resolve(ctx, u, func(done, total int) {
			emit(ResolveProgressEvent{URL: u, Done: done, Total: total})
		})
		emit(ResolveDoneEvent{URL: u, List: l, Err: err})
		if err == nil {
			lists = append(lists, l)
		}
	}
	return lists
}

// Ext devolve a extensão de saída do formato configurado.
func Ext(cfg config.Config) string { return cfg.Format }

// PathFor aplica o template da configuração a uma faixa.
func PathFor(cfg config.Config, t track.Track) (string, error) {
	return pathtpl.Render(cfg.Output, t, Ext(cfg), pathtpl.Options{BaseDir: cfg.OutputDir, Restrict: pathtpl.Restrict(cfg.Restrict)})
}

// Plan monta os jobs: uma faixa que aparece em mais de uma lista é baixada
// uma vez só (com o contexto da primeira lista), e caminhos repetidos entre
// faixas diferentes ganham um sufixo " (2)", " (3)"...
func Plan(cfg config.Config, lists []*spotify.List) ([]Job, error) {
	var jobs []Job
	seenID := map[string]bool{}
	usedPath := map[string]int{}
	for _, l := range lists {
		for _, t := range l.Tracks {
			if seenID[t.ID] {
				continue
			}
			seenID[t.ID] = true
			p, err := PathFor(cfg, t)
			if err != nil {
				return nil, err
			}
			key := strings.ToLower(p)
			if n := usedPath[key]; n > 0 {
				ext := filepath.Ext(p)
				p = fmt.Sprintf("%s (%d)%s", strings.TrimSuffix(p, ext), n+1, ext)
			}
			usedPath[key]++
			jobs = append(jobs, Job{Track: t, Path: p})
		}
	}
	return jobs, nil
}

// Runner guarda os clientes usados durante a execução.
type Runner struct {
	Cfg    config.Config
	YT     *ytmusic.Client
	Lyrics *lyrics.Client
	HTTP   *http.Client

	covers sync.Map // URL → *coverEntry (faixas do mesmo álbum dividem a capa)
}

type coverEntry struct {
	once sync.Once
	data []byte
}

// NewRunner cria o runner com os clientes padrão.
func NewRunner(cfg config.Config) *Runner {
	return &Runner{Cfg: cfg, YT: ytmusic.New(), Lyrics: lyrics.New(), HTTP: &http.Client{Timeout: 30 * time.Second}}
}

func (r *Runner) dl() download.Options {
	return download.Options{YtDlp: r.Cfg.YtDlp, Format: r.Cfg.Format, Bitrate: r.Cfg.Bitrate, Cookies: r.Cfg.Cookies}
}

// Run processa os jobs com Cfg.Threads workers, grava os M3U das listas que
// não são faixas avulsas e emite FinishedEvent no fim.
func (r *Runner) Run(ctx context.Context, jobs []Job, lists []*spotify.List, emit func(any)) FinishedEvent {
	start := time.Now()
	results := make([]Result, len(jobs))
	for i, j := range jobs {
		results[i] = Result{Job: j, Status: Pending}
	}

	tmp, err := download.TempDir()
	if err != nil {
		emit(WarningEvent{Msg: "pasta temporária: " + err.Error()})
		fin := FinishedEvent{Results: results, Elapsed: time.Since(start)}
		emit(fin)
		return fin
	}
	defer os.RemoveAll(tmp)

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(r.Cfg.Threads)
	for i := range jobs {
		if gctx.Err() != nil {
			break
		}
		g.Go(func() error {
			emit(JobStartEvent{Index: i})
			res := r.process(gctx, i, jobs[i], tmp, emit)
			results[i] = res
			emit(JobDoneEvent{Index: i, Result: res})
			return nil
		})
	}
	_ = g.Wait()
	for i := range results {
		if results[i].Status == Pending {
			results[i].Status = Canceled
		}
	}

	fin := FinishedEvent{Results: results}
	if r.Cfg.M3U {
		paths, err := r.writeM3U(results, lists)
		if err != nil {
			emit(WarningEvent{Msg: "M3U: " + err.Error()})
		}
		fin.M3U = paths
	}
	if f, err := r.writeFailures(results); err != nil {
		emit(WarningEvent{Msg: "lista de falhas: " + err.Error()})
	} else {
		fin.Failures = f
	}
	fin.Elapsed = time.Since(start)
	emit(fin)
	return fin
}

func (r *Runner) process(ctx context.Context, i int, j Job, tmp string, emit func(any)) Result {
	res := Result{Job: j}
	stage := func(s string) { emit(JobStageEvent{Index: i, Stage: s}) }
	fail := func(err error) Result {
		res.Err = err
		res.Status = Failed
		if ctx.Err() != nil {
			res.Status = Canceled
		}
		return res
	}

	_, statErr := os.Stat(j.Path)
	exists := statErr == nil
	if exists && r.Cfg.Overwrite == "skip" {
		res.Status = Skipped
		return res
	}

	t := j.Track
	if exists && r.Cfg.Overwrite == "metadata" {
		stage("tags")
		lyr := r.extras(ctx, t, stage)
		res.Lyrics = lyr != ""
		if err := r.tagAndLRC(ctx, j.Path, j.Path, t, lyr, ""); err != nil {
			return fail(err)
		}
		res.Status = Retagged
		return res
	}

	stage("buscando")
	m, err := r.YT.Find(ctx, t)
	if err != nil {
		var nm *ytmusic.ErrNoMatch
		if errors.As(err, &nm) {
			res.Status, res.Err = NoMatch, err
			return res
		}
		return fail(err)
	}
	res.Match = &m
	if t.Album == "" {
		t.Album = m.Album // faixa sem detalhes do Spotify: usa o álbum do YouTube Music
	}

	stage("baixando")
	dir, err := os.MkdirTemp(tmp, "job-")
	if err != nil {
		return fail(err)
	}
	last := -1.0
	file, err := download.Fetch(ctx, r.dl(), m.URL(), dir, func(f float64) {
		if f-last >= 0.01 || f == 1 {
			last = f
			emit(JobProgressEvent{Index: i, Frac: f})
		}
	})
	if err != nil {
		return fail(err)
	}

	stage("tags")
	lyr := r.extras(ctx, t, stage)
	res.Lyrics = lyr != ""
	if err := r.tagAndLRC(ctx, file, j.Path, t, lyr, m.URL()); err != nil {
		return fail(err)
	}
	res.Status = Downloaded
	return res
}

// extras busca a letra conforme a configuração ("" se desligada ou não achada).
func (r *Runner) extras(ctx context.Context, t track.Track, stage func(string)) string {
	if r.Cfg.Lyrics == "off" {
		return ""
	}
	stage("letra")
	l, err := r.Lyrics.Find(ctx, t)
	if err != nil || l.Instrumental {
		return ""
	}
	return l.Best()
}

// tagAndLRC grava as tags em src, move para dst e cria o .lrc se pedido.
func (r *Runner) tagAndLRC(ctx context.Context, src, dst string, t track.Track, lyr, source string) error {
	embed := r.Cfg.Lyrics == "embed" || r.Cfg.Lyrics == "both"
	meta := tag.Meta{Track: t, Cover: r.cover(ctx, t.CoverURL), Source: source}
	if embed {
		meta.Lyrics = lyr
	}
	if err := tag.Write(ctx, src, meta); err != nil {
		return err
	}
	if src != dst {
		if err := move(src, dst); err != nil {
			return err
		}
	}
	if lyr != "" && (r.Cfg.Lyrics == "lrc" || r.Cfg.Lyrics == "both") {
		lrc := strings.TrimSuffix(dst, filepath.Ext(dst)) + ".lrc"
		if err := os.WriteFile(lrc, []byte(lyrics.LRC(t, lyrics.Lyrics{Synced: lyr})), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// cover baixa a capa uma vez por URL. Falha = sem capa (não impede o download).
func (r *Runner) cover(ctx context.Context, url string) []byte {
	if url == "" {
		return nil
	}
	v, _ := r.covers.LoadOrStore(url, &coverEntry{})
	e := v.(*coverEntry)
	e.once.Do(func() {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return
		}
		resp, err := r.HTTP.Do(req)
		if err != nil {
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return
		}
		e.data, _ = io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	})
	return e.data
}

// move renomeia src para dst criando as pastas; entre sistemas de arquivos
// diferentes (o /tmp costuma ser tmpfs) copia para dst.part e renomeia, para
// nunca deixar um arquivo final pela metade.
func move(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	err := os.Rename(src, dst)
	if err == nil || !errors.Is(err, syscall.EXDEV) {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	part := dst + ".part"
	out, err := os.Create(part)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(part)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(part)
		return err
	}
	if err := os.Rename(part, dst); err != nil {
		os.Remove(part)
		return err
	}
	return os.Remove(src)
}

func (r *Runner) writeM3U(results []Result, lists []*spotify.List) ([]string, error) {
	byID := map[string]Result{}
	for _, res := range results {
		byID[res.Job.Track.ID] = res
	}
	var ml []m3u.List
	for _, l := range lists {
		if l.Kind == spotify.KindTrack {
			continue
		}
		entry := m3u.List{Name: l.Name}
		for _, t := range l.Tracks {
			e := m3u.Entry{Track: t}
			if res, ok := byID[t.ID]; ok {
				if res.Status.OK() {
					e.Path = res.Job.Path
				} else {
					e.Reason = reason(res)
				}
			} else {
				e.Reason = "desmarcada"
			}
			entry.Entries = append(entry.Entries, e)
		}
		ml = append(ml, entry)
	}
	if len(ml) == 0 {
		return nil, nil
	}
	return m3u.Write(ml, m3u.Options{
		Name: r.Cfg.M3UName, BaseDir: r.Cfg.OutputDir,
		Restrict: pathtpl.Restrict(r.Cfg.Restrict), Absolute: r.Cfg.M3UAbsolute,
	})
}

func reason(res Result) string {
	switch res.Status {
	case NoMatch:
		return "sem correspondência no YouTube Music"
	case Canceled:
		return "cancelada"
	}
	if res.Err != nil {
		return res.Err.Error()
	}
	return "não baixada"
}

// writeFailures grava em output_dir a lista das faixas que não baixaram, com
// o link do Spotify, para conferir ou buscar à mão.
func (r *Runner) writeFailures(results []Result) (string, error) {
	var b strings.Builder
	for _, res := range results {
		if res.Status != NoMatch && res.Status != Failed {
			continue
		}
		t := res.Job.Track
		fmt.Fprintf(&b, "%s - %s\t%s\t%s\n", t.ArtistsString(), t.Title, t.URL(), reason(res))
	}
	if b.Len() == 0 {
		return "", nil
	}
	dir := pathtpl.ExpandPath(r.Cfg.OutputDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "spotdown-falhas.txt")
	head := fmt.Sprintf("# spotdown — %s\n", time.Now().Format("2006-01-02 15:04"))
	return path, os.WriteFile(path, []byte(head+b.String()), 0o644)
}
