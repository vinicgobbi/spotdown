package tui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vinicgobbi/spotdown/internal/engine"
	"github.com/vinicgobbi/spotdown/internal/plain"
)

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// ---- Tela: revisão ----

func (m Model) countSelected() int {
	n := 0
	for _, s := range m.sel {
		if s {
			n++
		}
	}
	return n
}

func (m Model) updateReview(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.revErr = ""
	switch msg.String() {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "esc":
		m.state = stateOptions
		return m, nil
	case "up", "k":
		m.rcur = max(0, m.rcur-1)
	case "down", "j":
		m.rcur = min(len(m.jobs)-1, m.rcur+1)
	case "pgup":
		m.rcur = max(0, m.rcur-m.reviewRows())
	case "pgdown":
		m.rcur = min(len(m.jobs)-1, m.rcur+m.reviewRows())
	case "home", "g":
		m.rcur = 0
	case "end", "G":
		m.rcur = len(m.jobs) - 1
	case " ", "x":
		if len(m.sel) > 0 {
			m.sel[m.rcur] = !m.sel[m.rcur]
		}
	case "a":
		all := m.countSelected() == len(m.sel)
		for i := range m.sel {
			m.sel[i] = !all
		}
	case "e":
		// Desmarca as que já existem (útil com overwrite=force).
		for i := range m.sel {
			if m.exists[i] {
				m.sel[i] = false
			}
		}
	case "enter":
		if m.countSelected() == 0 {
			m.revErr = "nenhuma faixa marcada"
			return m, nil
		}
		return m.startRun()
	}
	return m, nil
}

func (m Model) reviewRows() int { return max(5, m.h-14-len(m.cfg.Warnings)) }

func (m Model) viewReview() string {
	var b strings.Builder
	b.WriteString(m.header() + "\n")
	nExist := 0
	for _, e := range m.exists {
		if e {
			nExist++
		}
	}
	title := fmt.Sprintf("Revisão — %d de %d faixa(s) marcada(s)", m.countSelected(), len(m.jobs))
	b.WriteString(boldStyle.Render(title))
	if nExist > 0 {
		b.WriteString(subtleStyle.Render(fmt.Sprintf("  · %d já existe(m) (%s)", nExist, m.c.Overwrite)))
	}
	b.WriteString("\n\n")

	start, end := window(m.rcur, len(m.jobs), m.reviewRows())
	for i := start; i < end; i++ {
		j := m.jobs[i]
		box := subtleStyle.Render("[ ]")
		if m.sel[i] {
			box = okStyle.Render("[x]")
		}
		cursor := "  "
		if i == m.rcur {
			cursor = cursorStyle.Render("› ")
		}
		name := j.Track.ArtistsString() + " - " + j.Track.Title
		mark := ""
		if m.exists[i] {
			mark = warnStyle.Render(" ●")
		}
		line := cursor + box + " " + name + mark + subtleStyle.Render("  → "+m.rel(j.Path))
		b.WriteString(m.trunc(line, 0) + "\n")
	}
	if len(m.jobs) > end-start {
		b.WriteString(subtleStyle.Render(fmt.Sprintf("  … %d/%d", m.rcur+1, len(m.jobs))) + "\n")
	}
	if m.revErr != "" {
		b.WriteString("\n" + errStyle.Render("✗ "+m.revErr) + "\n")
	}
	b.WriteString("\n" + help("espaço", "marcar", "a", "todas", "e", "desmarcar existentes", "enter", "baixar", "esc", "opções"))
	return b.String()
}

// ---- Tela: downloads ----

func (m Model) startRun() (tea.Model, tea.Cmd) {
	m.runJobs = nil
	for i, j := range m.jobs {
		if m.sel[i] {
			m.runJobs = append(m.runJobs, j)
		}
	}
	m.results = make([]engine.Result, len(m.runJobs))
	for i, j := range m.runJobs {
		m.results[i] = engine.Result{Job: j, Status: engine.Pending}
	}
	m.stage, m.frac = map[int]string{}, map[int]float64{}
	m.active, m.log, m.notices = nil, nil, nil
	m.cancelAsk, m.canceling = false, false
	m.state = stateRunning
	m.started = time.Now()
	m.events = make(chan any, 2048)

	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	ch, jobs, lists, runner := m.events, m.runJobs, m.lists, engine.NewRunner(m.c)
	go func() {
		runner.Run(ctx, jobs, lists, func(ev any) { ch <- ev })
		close(ch)
	}()
	return m, tea.Batch(listen(m.events), tick(), m.spin.Tick)
}

func (m Model) updateRunning(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		if m.canceling {
			return m, nil
		}
		if !m.cancelAsk {
			m.cancelAsk = true
			return m, nil
		}
		m.canceling = true
		m.cancel()
		return m, nil
	case "esc", "n":
		m.cancelAsk = false
	}
	return m, nil
}

func (m Model) progress() (done int, frac float64) {
	if len(m.results) == 0 {
		return 0, 0
	}
	partial := 0.0
	for i, r := range m.results {
		switch r.Status {
		case engine.Pending:
		case engine.Running:
			partial += m.frac[i] * 0.9 // a fração do download; tags/letra ficam nos 10% finais
		default:
			done++
		}
	}
	return done, (float64(done) + partial) / float64(len(m.results))
}

func (m Model) viewRunning() string {
	var b strings.Builder
	b.WriteString(m.header() + "\n")
	done, frac := m.progress()
	b.WriteString(fmt.Sprintf("%s %s %s\n\n",
		boldStyle.Render(fmt.Sprintf("Baixando %d/%d", done, len(m.results))),
		m.bar.ViewAs(frac),
		subtleStyle.Render(fmtDuration(m.elapsed))))

	for _, i := range m.active {
		t := m.runJobs[i].Track
		st := m.stage[i]
		if st == "" {
			st = "iniciando"
		}
		if st == "baixando" {
			st = fmt.Sprintf("baixando %3.0f%%", m.frac[i]*100)
		}
		line := m.spin.View() + " " + subtleStyle.Render(fmt.Sprintf("%-14s", st)) + " " + t.ArtistsString() + " - " + t.Title
		b.WriteString(m.trunc(line, 0) + "\n")
	}
	if len(m.active) > 0 {
		b.WriteString("\n")
	}

	rows := max(3, m.h-10-len(m.active)-len(m.cfg.Warnings)-len(m.notices))
	start := max(0, len(m.log)-rows)
	for _, l := range m.log[start:] {
		b.WriteString(m.trunc(l, 0) + "\n")
	}
	for _, n := range m.notices {
		b.WriteString(warnStyle.Render("! "+n) + "\n")
	}

	b.WriteString("\n")
	switch {
	case m.canceling:
		b.WriteString(warnStyle.Render("Cancelando… aguardando os downloads em andamento pararem."))
	case m.cancelAsk:
		b.WriteString(warnStyle.Render("Cancelar os downloads? ") + help("ctrl+c", "sim", "esc", "não"))
	default:
		b.WriteString(help("ctrl+c", "cancelar"))
	}
	return b.String()
}

func renderResult(r engine.Result) string {
	t := r.Job.Track
	name := t.ArtistsString() + " - " + t.Title
	switch r.Status {
	case engine.Downloaded:
		extra := ""
		if r.Lyrics {
			extra = " ♪"
		}
		return okStyle.Render("✓ ") + name + subtleStyle.Render(extra)
	case engine.Skipped:
		return subtleStyle.Render("· " + name + " (já existe)")
	case engine.Retagged:
		return infoStyle.Render("↻ ") + name + subtleStyle.Render(" (tags regravadas)")
	case engine.NoMatch:
		return warnStyle.Render("? ") + name + subtleStyle.Render(" — sem correspondência no YouTube Music")
	case engine.Canceled:
		return subtleStyle.Render("- " + name + " (cancelada)")
	}
	msg := ""
	if r.Err != nil {
		msg = " — " + r.Err.Error()
	}
	return errStyle.Render("✗ ") + name + subtleStyle.Render(msg)
}

// ---- Tela: fim ----

func (m Model) updateDone(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q", "enter", "esc":
		return m, tea.Quit
	case "n":
		// Novo download: volta à tela de links mantendo as opções.
		next := New(Config{Cfg: m.c, ConfigPath: m.cfg.ConfigPath, Version: m.cfg.Version})
		next.w, next.h = m.w, m.h
		next.resize()
		return next, next.Init()
	}
	return m, nil
}

func (m Model) viewDone() string {
	var b strings.Builder
	b.WriteString(m.header() + "\n")
	b.WriteString(boldStyle.Render("Concluído") + "\n\n")
	if m.finished != nil {
		b.WriteString(plain.Summary(*m.finished) + "\n")
		var bad []string
		for _, r := range m.finished.Results {
			if r.Status == engine.NoMatch || r.Status == engine.Failed {
				bad = append(bad, renderResult(r))
			}
		}
		if len(bad) > 0 {
			b.WriteString("\n" + boldStyle.Render("Não baixadas") + "\n")
			rows := max(3, m.h-14)
			for i, l := range bad {
				if i == rows {
					b.WriteString(subtleStyle.Render(fmt.Sprintf("… e mais %d (veja o arquivo de falhas)", len(bad)-rows)) + "\n")
					break
				}
				b.WriteString(m.trunc(l, 0) + "\n")
			}
		}
	}
	for _, n := range m.notices {
		b.WriteString(warnStyle.Render("! "+n) + "\n")
	}
	b.WriteString("\n" + help("n", "novo download", "q", "sair"))
	return b.String()
}

func fmtDuration(d time.Duration) string {
	d = d.Round(time.Second)
	h, mnt, s := int(d.Hours()), int(d.Minutes())%60, int(d.Seconds())%60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, mnt, s)
	}
	return fmt.Sprintf("%02d:%02d", mnt, s)
}
