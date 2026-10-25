package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/vinicgobbi/spotdown/internal/config"
	"github.com/vinicgobbi/spotdown/internal/engine"
	"github.com/vinicgobbi/spotdown/internal/plain"
	"github.com/vinicgobbi/spotdown/internal/spotify"
)

// ---- Tela: links ----

func newArea() textarea.Model {
	a := textarea.New()
	a.Placeholder = "https://open.spotify.com/playlist/...\nhttps://open.spotify.com/album/...\nhttps://open.spotify.com/track/..."
	a.ShowLineNumbers = false
	a.CharLimit = 0
	a.Prompt = "│ "
	// Enter avança; nova linha com ctrl+j/alt+enter (colar várias linhas funciona direto).
	a.KeyMap.InsertNewline.SetKeys("ctrl+j", "alt+enter")
	a.Focus()
	return a
}

// splitLinks separa os links do texto colado (linhas, espaços ou vírgulas).
func splitLinks(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		return r == '\n' || r == ' ' || r == '\t' || r == ','
	})
}

func (m Model) updateInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "esc":
		return m, tea.Quit
	case "enter":
		links := splitLinks(m.area.Value())
		if len(links) == 0 {
			m.inputErr = "cole ao menos um link"
			return m, nil
		}
		for _, l := range links {
			if _, _, err := spotify.ParseURL(l); err != nil {
				m.inputErr = err.Error()
				return m, nil
			}
		}
		m.inputErr = ""
		m.urls = links
		return m.startResolve()
	}
	var cmd tea.Cmd
	m.area, cmd = m.area.Update(msg)
	return m, cmd
}

func (m Model) viewInput() string {
	var b strings.Builder
	b.WriteString(m.header() + "\n")
	b.WriteString(boldStyle.Render("Cole os links do Spotify") + subtleStyle.Render(" — faixas, álbuns ou playlists, um por linha") + "\n\n")
	b.WriteString(m.area.View() + "\n")
	if m.inputErr != "" {
		b.WriteString("\n" + errStyle.Render("✗ "+m.inputErr) + "\n")
	}
	b.WriteString("\n" + help("enter", "continuar", "ctrl+j", "nova linha", "esc", "sair"))
	return b.String()
}

// ---- Tela: lendo os links ----

// client cria (ou reaproveita) o cliente do Spotify. Ele é recriado só
// quando a necessidade de detalhes extras do álbum muda (ver NeedsAlbumDetails).
func (m *Model) client() *spotify.Client {
	need := engine.NeedsAlbumDetails(m.c)
	if m.sp == nil || need != m.spAlbumInfo {
		m.sp = spotify.New(spotify.Options{
			ClientID: m.c.SpotifyClientID, ClientSecret: m.c.SpotifyClientSecret,
			CacheDir: config.CacheDir(), AlbumDetails: need,
		})
		m.spAlbumInfo = need
	}
	return m.sp
}

func (m Model) startResolve() (tea.Model, tea.Cmd) {
	m.state = stateResolving
	m.resolved = nil
	for _, u := range m.urls {
		m.resolved = append(m.resolved, resolveLine{url: u})
	}
	m.resProgress = [2]int{}
	m.events = make(chan any, 1024)
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	ch, sp, urls := m.events, m.client(), m.urls
	go func() {
		lists := engine.Resolve(ctx, sp, urls, func(ev any) { ch <- ev })
		if ctx.Err() == nil {
			ch <- resolvedMsg{lists: lists}
		}
		close(ch)
	}()
	return m, tea.Batch(listen(m.events), m.spin.Tick)
}

func (m Model) updateResolving(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "esc":
		if m.cancel != nil {
			m.cancel()
		}
		m.state = stateInput
		m.area.SetValue(strings.Join(m.urls, "\n"))
		return m, textarea.Blink
	}
	return m, nil
}

func (m Model) afterResolve(lists []*spotify.List) (tea.Model, tea.Cmd) {
	m.lists = lists
	if len(lists) == 0 {
		m.state = stateInput
		m.area.SetValue(strings.Join(m.urls, "\n"))
		m.inputErr = "nenhum link pôde ser lido"
		for _, r := range m.resolved {
			if r.err != nil {
				m.inputErr = r.err.Error()
				break
			}
		}
		return m, textarea.Blink
	}
	if m.cfg.Yes && len(m.cfg.URLs) > 0 {
		if err := m.plan(); err != nil {
			m.state = stateOptions
			m.optErr = err.Error()
			return m, nil
		}
		return m.startRun()
	}
	m.state = stateOptions
	m.fields[m.fcur].focus()
	return m, nil
}

func (m Model) viewResolving() string {
	var b strings.Builder
	b.WriteString(m.header() + "\n")
	b.WriteString(boldStyle.Render("Lendo os links no Spotify") + "\n\n")
	for _, r := range m.resolved {
		switch {
		case !r.done:
			line := m.spin.View() + " " + r.url
			if t := m.resProgress[1]; t > 0 {
				line += subtleStyle.Render(fmt.Sprintf("  detalhes %d/%d", m.resProgress[0], t))
			}
			b.WriteString(m.trunc(line, 0) + "\n")
		case r.err != nil:
			b.WriteString(m.trunc(errStyle.Render("✗ ")+r.url+subtleStyle.Render("  "+r.err.Error()), 0) + "\n")
		default:
			l := r.list
			b.WriteString(m.trunc(okStyle.Render("✓ ")+plain.KindName(l.Kind)+" "+boldStyle.Render(l.Name)+
				subtleStyle.Render(fmt.Sprintf("  %d faixa(s)", len(l.Tracks))), 0) + "\n")
			for _, w := range l.Warnings {
				b.WriteString(warnStyle.Width(max(20, m.w-2)).Render("  ! "+w) + "\n")
			}
		}
	}
	if m.sp != nil && !m.sp.HasAPI() {
		b.WriteString("\n" + subtleStyle.Render("Sem credenciais da API do Spotify: playlists ficam limitadas a 100 faixas."))
	}
	b.WriteString("\n\n" + help("esc", "cancelar"))
	return b.String()
}
