package tui

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/vinicgobbi/spotdown/internal/config"
	"github.com/vinicgobbi/spotdown/internal/engine"
	"github.com/vinicgobbi/spotdown/internal/spotify"
	"github.com/vinicgobbi/spotdown/internal/track"
)

func key(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "space":
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
	case "ctrl+u":
		return tea.KeyMsg{Type: tea.KeyCtrlU}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func send(t *testing.T, m Model, msgs ...tea.Msg) Model {
	t.Helper()
	for _, msg := range msgs {
		nm, _ := m.Update(msg)
		m = nm.(Model)
	}
	return m
}

func screen(m Model) string { return ansi.Strip(m.View()) }

func testModel(t *testing.T) Model {
	cfg := config.Default()
	cfg.OutputDir = t.TempDir()
	cfg.Output = "{artist}/{title}"
	m := New(Config{Cfg: cfg, ConfigPath: filepath.Join(t.TempDir(), "config.toml"), Version: "test"})
	m = send(t, m, tea.WindowSizeMsg{Width: 140, Height: 40})
	return m
}

func album() *spotify.List {
	l := &spotify.List{Kind: spotify.KindAlbum, Name: "Álbum X"}
	for i, title := range []string{"Primeira", "Segunda", "Terceira"} {
		l.Tracks = append(l.Tracks, track.Track{
			ID: string(rune('a' + i)), Title: title, Artists: []string{"Banda"}, Album: "Álbum X",
			TrackNumber: i + 1, TracksCount: 3, ListName: "Álbum X", ListPosition: i + 1, ListLength: 3,
		})
	}
	return l
}

func TestInputValidatesLinks(t *testing.T) {
	m := testModel(t)
	m = send(t, m, key("enter"))
	if !strings.Contains(screen(m), "cole ao menos um link") {
		t.Errorf("sem erro de entrada vazia:\n%s", screen(m))
	}
	m = send(t, m, key("https://example.com/x"), key("enter"))
	if m.state != stateInput || !strings.Contains(screen(m), "não é um link do Spotify") {
		t.Errorf("aceitou link inválido:\n%s", screen(m))
	}
}

func TestOptionsPreviewAndReview(t *testing.T) {
	m := testModel(t)
	m = send(t, m, resolvedEvents(album())...)
	if m.state != stateOptions {
		t.Fatalf("estado %v", m.state)
	}
	s := screen(m)
	if !strings.Contains(s, filepath.Join("Banda", "Primeira.mp3")) {
		t.Errorf("prévia sem o caminho da primeira faixa:\n%s", s)
	}

	// Template (campo 1): troca por outro e vê a prévia mudar.
	m = send(t, m, key("down"), key("ctrl+u"), key("{album}/{track-number:02} {title}"))
	if s := screen(m); !strings.Contains(s, filepath.Join("Álbum X", "01 Primeira.mp3")) {
		t.Errorf("prévia não acompanhou o template:\n%s", s)
	}
	// Formato (campo 2): → muda de mp3 para m4a.
	m = send(t, m, key("down"), key("right"))
	if m.c.Format != "m4a" || !strings.Contains(screen(m), "01 Primeira.m4a") {
		t.Errorf("formato=%s\n%s", m.c.Format, screen(m))
	}
	// Variável inválida bloqueia o avanço.
	m = send(t, m, key("up"), key("{xyz}"), key("enter"))
	if m.state != stateOptions || !strings.Contains(screen(m), "variável desconhecida {xyz}") {
		t.Errorf("aceitou template inválido:\n%s", screen(m))
	}
	m = send(t, m, key("ctrl+u"), key("{album}/{title}"), key("enter"))
	if m.state != stateReview {
		t.Fatalf("estado %v: %s", m.state, m.optErr)
	}
	if s := screen(m); !strings.Contains(s, "3 de 3") || !strings.Contains(s, filepath.Join("Álbum X", "Segunda.m4a")) {
		t.Errorf("revisão:\n%s", s)
	}
	m = send(t, m, key("space"))
	if m.countSelected() != 2 || !strings.Contains(screen(m), "2 de 3") {
		t.Errorf("espaço não desmarcou:\n%s", screen(m))
	}
	m = send(t, m, key("a"))
	if m.countSelected() != 3 {
		t.Errorf("a não marcou todas: %d", m.countSelected())
	}
}

func TestOptionsSaveConfig(t *testing.T) {
	m := testModel(t)
	m = send(t, m, resolvedEvents(album())...)
	m = send(t, m, key("down"), key("down"), key("right"), tea.KeyMsg{Type: tea.KeyCtrlS})
	if !strings.Contains(screen(m), "configuração gravada") {
		t.Fatalf("não gravou:\n%s", screen(m))
	}
	got, _, err := config.Load(m.cfg.ConfigPath)
	if err != nil || got.Format != "m4a" {
		t.Errorf("format=%q err=%v", got.Format, err)
	}
}

func TestRunningAndDoneScreens(t *testing.T) {
	m := testModel(t)
	l := album()
	m.lists = []*spotify.List{l}
	if err := m.plan(); err != nil {
		t.Fatal(err)
	}
	// Simula a execução sem rede: estado de downloads com eventos falsos.
	m.runJobs = m.jobs
	m.results = make([]engine.Result, len(m.jobs))
	m.stage, m.frac = map[int]string{}, map[int]float64{}
	m.state = stateRunning
	m.events = make(chan any)
	m = send(t, m, eventsMsg{
		engine.JobStartEvent{Index: 0}, engine.JobStageEvent{Index: 0, Stage: "baixando"}, engine.JobProgressEvent{Index: 0, Frac: 0.5},
		engine.JobStartEvent{Index: 1}, engine.JobDoneEvent{Index: 1, Result: engine.Result{Job: m.jobs[1], Status: engine.NoMatch, Err: errors.New("x")}},
	})
	s := screen(m)
	if !strings.Contains(s, "baixando  50%") || !strings.Contains(s, "Banda - Primeira") || !strings.Contains(s, "sem correspondência") {
		t.Errorf("tela de downloads:\n%s", s)
	}
	m = send(t, m, key("q"))
	if !m.cancelAsk || !strings.Contains(screen(m), "Cancelar os downloads?") {
		t.Errorf("sem confirmação de cancelamento")
	}
	m = send(t, m, key("esc"))

	res := []engine.Result{
		{Job: m.jobs[0], Status: engine.Downloaded},
		{Job: m.jobs[1], Status: engine.NoMatch, Err: errors.New("x")},
		{Job: m.jobs[2], Status: engine.Skipped},
	}
	m = send(t, m, eventsMsg{engine.FinishedEvent{Results: res, M3U: []string{"/tmp/x.m3u8"}}})
	s = screen(m)
	if m.state != stateDone || !strings.Contains(s, "1 baixada(s), 1 já existia(m)") || !strings.Contains(s, "M3U: /tmp/x.m3u8") || !strings.Contains(s, "Não baixadas") {
		t.Errorf("tela final:\n%s", s)
	}
}

// resolvedEvents simula o fim da leitura dos links.
func resolvedEvents(lists ...*spotify.List) []tea.Msg {
	return []tea.Msg{eventsMsg{resolvedMsg{lists: lists}}}
}

func TestVarsHelpAligned(t *testing.T) {
	lines := strings.Split(ansi.Strip(varsHelp()), "\n")
	col := -1
	for _, l := range lines[:5] {
		i := strings.Index(l[1:], "{")
		if i < 0 {
			continue
		}
		w := ansi.StringWidth(l[:i+1])
		if col >= 0 && w != col {
			t.Errorf("colunas desalinhadas:\n%s", strings.Join(lines, "\n"))
			return
		}
		col = w
	}
}
