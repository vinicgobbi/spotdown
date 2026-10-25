// Package tui é a interface interativa: colar links, ler os metadados,
// ajustar as opções com prévia do caminho, revisar as faixas e acompanhar os
// downloads.
package tui

import (
	"context"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/vinicgobbi/spotdown/internal/config"
	"github.com/vinicgobbi/spotdown/internal/engine"
	"github.com/vinicgobbi/spotdown/internal/spotify"
)

// Config é o que o main passa para a interface.
type Config struct {
	Cfg        config.Config
	ConfigPath string
	URLs       []string // links da linha de comando (pula a tela de entrada)
	Yes        bool     // com URLs: baixar direto, sem opções/revisão
	Warnings   []string
	Version    string
}

type state int

const (
	stateInput state = iota
	stateResolving
	stateOptions
	stateReview
	stateRunning
	stateDone
)

// Model é o modelo bubbletea.
type Model struct {
	cfg   Config
	c     config.Config // opções atuais (editáveis na tela de opções)
	state state
	w, h  int

	spin spinner.Model
	bar  progress.Model

	// entrada
	area     textarea.Model
	inputErr string

	// leitura dos links
	sp          *spotify.Client
	spAlbumInfo bool // AlbumDetails com que o cliente foi criado
	urls        []string
	resolved    []resolveLine
	resProgress [2]int
	lists       []*spotify.List

	events chan any
	cancel context.CancelFunc

	// opções
	fields   []field
	fcur     int
	showVars bool
	optMsg   string
	optErr   string

	// revisão
	jobs   []engine.Job
	sel    []bool
	exists []bool
	rcur   int
	revErr string

	// downloads
	runJobs   []engine.Job
	results   []engine.Result
	stage     map[int]string
	frac      map[int]float64
	active    []int
	log       []string
	notices   []string
	started   time.Time
	elapsed   time.Duration
	cancelAsk bool
	canceling bool
	finished  *engine.FinishedEvent
}

type resolveLine struct {
	url  string
	done bool
	list *spotify.List
	err  error
}

// New cria o modelo.
func New(cfg Config) Model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = accentStyle
	m := Model{
		cfg:  cfg,
		c:    cfg.Cfg,
		spin: sp,
		bar:  progress.New(progress.WithDefaultGradient(), progress.WithoutPercentage()),
		area: newArea(),
	}
	m.fields = newFields(m.c)
	if len(cfg.URLs) > 0 {
		m.urls = cfg.URLs
		m.state = stateResolving
	}
	return m
}

// Result devolve o fim da execução (nil se o usuário saiu antes).
func (m Model) Result() *engine.FinishedEvent { return m.finished }

func (m Model) Init() tea.Cmd {
	if m.state == stateResolving {
		return tea.Batch(m.spin.Tick, func() tea.Msg { return startResolveMsg{} })
	}
	return tea.Batch(textarea.Blink, m.spin.Tick)
}

type (
	eventsMsg       []any
	tickMsg         time.Time
	startResolveMsg struct{}
	// resolvedMsg vai pelo canal de eventos depois do último link.
	resolvedMsg struct{ lists []*spotify.List }
)

func tick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// listen espera o próximo evento e já drena os que estiverem na fila, para
// não redesenhar a tela a cada ponto de progresso.
func listen(ch chan any) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return nil
		}
		batch := []any{ev}
		for len(batch) < 500 {
			select {
			case ev, ok := <-ch:
				if !ok {
					return eventsMsg(batch)
				}
				batch = append(batch, ev)
			default:
				return eventsMsg(batch)
			}
		}
		return eventsMsg(batch)
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		// Descontando o padding aplicado em View.
		m.w, m.h = msg.Width-4, msg.Height-1
		m.resize()
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case tickMsg:
		if m.state == stateRunning {
			m.elapsed = time.Since(m.started)
			return m, tick()
		}
		return m, nil

	case startResolveMsg:
		return m.startResolve()

	case eventsMsg:
		return m.handleEvents(msg)

	case tea.KeyMsg:
		switch m.state {
		case stateInput:
			return m.updateInput(msg)
		case stateResolving:
			return m.updateResolving(msg)
		case stateOptions:
			return m.updateOptions(msg)
		case stateReview:
			return m.updateReview(msg)
		case stateRunning:
			return m.updateRunning(msg)
		case stateDone:
			return m.updateDone(msg)
		}
	}

	if m.state == stateInput {
		var cmd tea.Cmd
		m.area, cmd = m.area.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *Model) resize() {
	m.bar.Width = max(20, min(m.w-30, 60))
	m.area.SetWidth(max(20, m.w-2))
	m.area.SetHeight(max(3, min(12, m.h-12)))
	for i := range m.fields {
		m.fields[i].input.Width = max(20, m.w-28)
	}
}

func (m Model) handleEvents(batch eventsMsg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	for _, ev := range batch {
		switch ev := ev.(type) {
		case engine.ResolveProgressEvent:
			m.resProgress = [2]int{ev.Done, ev.Total}
		case engine.ResolveDoneEvent:
			for i := range m.resolved {
				if m.resolved[i].url == ev.URL && !m.resolved[i].done {
					m.resolved[i] = resolveLine{url: ev.URL, done: true, list: ev.List, err: ev.Err}
					break
				}
			}
			m.resProgress = [2]int{}
		case resolvedMsg:
			return m.afterResolve(ev.lists)
		case engine.JobStartEvent:
			m.results[ev.Index].Status = engine.Running
			m.active = append(m.active, ev.Index)
		case engine.JobStageEvent:
			m.stage[ev.Index] = ev.Stage
		case engine.JobProgressEvent:
			m.frac[ev.Index] = ev.Frac
		case engine.JobDoneEvent:
			m.results[ev.Index] = ev.Result
			m.removeActive(ev.Index)
			m.log = append(m.log, renderResult(ev.Result))
			if len(m.log) > 500 {
				m.log = m.log[len(m.log)-500:]
			}
		case engine.WarningEvent:
			m.notices = append(m.notices, ev.Msg)
		case engine.FinishedEvent:
			m.finished = &ev
			m.elapsed = ev.Elapsed
			m.state = stateDone
		}
	}
	if m.state == stateRunning || m.state == stateResolving {
		cmds = append(cmds, listen(m.events))
	}
	return m, tea.Batch(cmds...)
}

func (m *Model) removeActive(i int) {
	for k, v := range m.active {
		if v == i {
			m.active = append(m.active[:k], m.active[k+1:]...)
			return
		}
	}
}

// ---- Comum ----

func (m Model) header() string {
	title := titleStyle.Render("SPOTDOWN")
	if m.cfg.Version != "" {
		title += " " + subtleStyle.Render(m.cfg.Version)
	}
	steps := []string{"Links", "Leitura", "Opções", "Revisão", "Download", "Fim"}
	var crumbs string
	for i, s := range steps {
		if i > 0 {
			crumbs += subtleStyle.Render(" › ")
		}
		if state(i) == m.state {
			crumbs += accentStyle.Render(s)
		} else {
			crumbs += subtleStyle.Render(s)
		}
	}
	out := title + "  " + crumbs
	for _, w := range m.cfg.Warnings {
		out += "\n" + warnStyle.Width(max(20, m.w)).Render("! "+w)
	}
	return out + "\n"
}

func (m Model) trunc(s string, extra int) string {
	if m.w <= 0 {
		return s
	}
	return ansi.Truncate(s, max(10, m.w-extra), "…")
}

func (m Model) View() string {
	if m.w <= 0 {
		return ""
	}
	var v string
	switch m.state {
	case stateInput:
		v = m.viewInput()
	case stateResolving:
		v = m.viewResolving()
	case stateOptions:
		v = m.viewOptions()
	case stateReview:
		v = m.viewReview()
	case stateRunning:
		v = m.viewRunning()
	case stateDone:
		v = m.viewDone()
	}
	return lipgloss.NewStyle().Padding(1, 2, 0, 2).Render(v)
}

// window devolve o intervalo [start, end) de itens visíveis para manter o
// cursor na tela.
func window(cursor, total, rows int) (int, int) {
	if total <= rows {
		return 0, total
	}
	start := max(0, cursor-rows/2)
	end := start + rows
	if end > total {
		end = total
		start = end - rows
	}
	return start, end
}
