package tui

import (
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/vinicgobbi/spotdown/internal/config"
	"github.com/vinicgobbi/spotdown/internal/download"
	"github.com/vinicgobbi/spotdown/internal/engine"
	"github.com/vinicgobbi/spotdown/internal/m3u"
	"github.com/vinicgobbi/spotdown/internal/pathtpl"
	"github.com/vinicgobbi/spotdown/internal/track"
)

// ---- Tela: opções ----

// field é uma linha da tela de opções: texto livre ou escolha (←/→).
type field struct {
	label   string
	hint    string
	choices []string // vazio = campo de texto
	get     func(config.Config) string
	set     func(*config.Config, string)
	input   textinput.Model
}

func (f *field) isText() bool { return len(f.choices) == 0 }

func (f *field) focus() {
	if f.isText() {
		f.input.Focus()
	}
}

func (f *field) blur() { f.input.Blur() }

func onOff(b bool) string {
	if b {
		return "sim"
	}
	return "não"
}

func newFields(c config.Config) []field {
	threads := make([]string, 16)
	for i := range threads {
		threads[i] = strconv.Itoa(i + 1)
	}
	fs := []field{
		{label: "Pasta base", hint: "onde ficam templates relativos",
			get: func(c config.Config) string { return c.OutputDir }, set: func(c *config.Config, v string) { c.OutputDir = v }},
		{label: "Template", hint: "tab mostra as variáveis",
			get: func(c config.Config) string { return c.Output }, set: func(c *config.Config, v string) { c.Output = v }},
		{label: "Formato", choices: download.Formats,
			get: func(c config.Config) string { return c.Format }, set: func(c *config.Config, v string) { c.Format = v }},
		{label: "Qualidade", choices: []string{"auto", "320k", "256k", "192k", "160k", "128k"},
			get: func(c config.Config) string { return c.Bitrate }, set: func(c *config.Config, v string) { c.Bitrate = v }},
		{label: "Letras", choices: config.LyricsMode, hint: "embed: no arquivo · lrc: arquivo ao lado",
			get: func(c config.Config) string { return c.Lyrics }, set: func(c *config.Config, v string) { c.Lyrics = v }},
		{label: "Se já existir", choices: config.Overwrites, hint: "metadata: só regrava as tags",
			get: func(c config.Config) string { return c.Overwrite }, set: func(c *config.Config, v string) { c.Overwrite = v }},
		{label: "Caracteres", choices: config.Restricts, hint: "ascii: sem acentos · strict: sem espaços",
			get: func(c config.Config) string { return c.Restrict }, set: func(c *config.Config, v string) { c.Restrict = v }},
		{label: "Gerar M3U", choices: []string{"não", "sim"}, hint: "um por álbum/playlist",
			get: func(c config.Config) string { return onOff(c.M3U) }, set: func(c *config.Config, v string) { c.M3U = v == "sim" }},
		{label: "Nome do M3U",
			get: func(c config.Config) string { return c.M3UName }, set: func(c *config.Config, v string) { c.M3UName = v }},
		{label: "Simultâneos", choices: threads,
			get: func(c config.Config) string { return strconv.Itoa(c.Threads) },
			set: func(c *config.Config, v string) { c.Threads, _ = strconv.Atoi(v) }},
	}
	for i := range fs {
		if fs[i].isText() {
			in := textinput.New()
			in.Prompt = ""
			in.CharLimit = 0
			in.SetValue(fs[i].get(c))
			fs[i].input = in
		}
	}
	return fs
}

func (m *Model) syncField(i int) {
	if f := &m.fields[i]; f.isText() {
		f.set(&m.c, f.input.Value())
	}
}

func (m Model) updateOptions(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	f := &m.fields[m.fcur]
	m.optMsg = ""
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.state = stateInput
		m.area.SetValue(strings.Join(m.urls, "\n"))
		return m, nil
	case "up", "shift+tab":
		f.blur()
		m.fcur = (m.fcur - 1 + len(m.fields)) % len(m.fields)
		m.fields[m.fcur].focus()
		return m, nil
	case "down":
		f.blur()
		m.fcur = (m.fcur + 1) % len(m.fields)
		m.fields[m.fcur].focus()
		return m, nil
	case "tab":
		m.showVars = !m.showVars
		return m, nil
	case "ctrl+s":
		if err := m.c.Validate(); err != nil {
			m.optErr = err.Error()
			return m, nil
		}
		if err := config.Save(m.cfg.ConfigPath, m.fileConfig()); err != nil {
			m.optErr = err.Error()
			return m, nil
		}
		m.optErr, m.optMsg = "", "configuração gravada em "+m.cfg.ConfigPath
		return m, nil
	case "enter":
		if err := m.c.Validate(); err != nil {
			m.optErr = err.Error()
			return m, nil
		}
		m.optErr = ""
		// Template novo pede dados de álbum que a primeira leitura pulou: lê de novo.
		if m.sp != nil && m.sp.HasAPI() && engine.NeedsAlbumDetails(m.c) && !m.spAlbumInfo {
			return m.startResolve()
		}
		if err := m.plan(); err != nil {
			m.optErr = err.Error()
			return m, nil
		}
		m.state = stateReview
		return m, nil
	}
	if !f.isText() {
		cur := slices.Index(f.choices, f.get(m.c))
		switch msg.String() {
		case "left", "h":
			f.set(&m.c, f.choices[(cur-1+len(f.choices))%len(f.choices)])
		case "right", "l", " ":
			f.set(&m.c, f.choices[(cur+1)%len(f.choices)])
		}
		return m, nil
	}
	var cmd tea.Cmd
	f.input, cmd = f.input.Update(msg)
	m.syncField(m.fcur)
	return m, cmd
}

// fileConfig devolve as opções atuais sem as credenciais que vieram do
// ambiente (para não gravá-las no arquivo).
func (m Model) fileConfig() config.Config {
	out := m.c
	out.SpotifyClientID, out.SpotifyClientSecret = "", ""
	if fileCfg, _, err := config.Load(m.cfg.ConfigPath); err == nil {
		out.SpotifyClientID, out.SpotifyClientSecret = fileCfg.SpotifyClientID, fileCfg.SpotifyClientSecret
	}
	return out
}

// sample é a faixa usada na prévia: a primeira lida, ou uma de exemplo.
func (m Model) sample() track.Track {
	for _, l := range m.lists {
		if len(l.Tracks) > 0 {
			return l.Tracks[0]
		}
	}
	return track.Track{
		ID: "4cOdK2wGLETKBW3PvgPWqT", Title: "Never Gonna Give You Up", Artists: []string{"Rick Astley"},
		Album: "Whenever You Need Somebody", AlbumArtist: "Rick Astley", TrackNumber: 1, TracksCount: 10,
		DiscNumber: 1, DiscCount: 1, Date: "1987-11-12", DurationMs: 213573,
		ListName: "Exemplo", ListPosition: 1, ListLength: 10,
	}
}

func (m Model) viewOptions() string {
	var b strings.Builder
	b.WriteString(m.header() + "\n")
	b.WriteString(boldStyle.Render("Opções") + "\n\n")
	for i, f := range m.fields {
		cursor := "  "
		label := fmt.Sprintf("%-14s", f.label)
		if i == m.fcur {
			cursor = cursorStyle.Render("› ")
			label = accentStyle.Render(label)
		}
		var val string
		if f.isText() {
			if i == m.fcur {
				val = f.input.View()
			} else {
				val = f.input.Value()
			}
		} else {
			v := f.get(m.c)
			if i == m.fcur {
				val = subtleStyle.Render("‹ ") + boldStyle.Render(v) + subtleStyle.Render(" ›")
			} else {
				val = v
			}
		}
		b.WriteString(m.trunc(cursor+label+" "+val, 0) + "\n")
	}
	if h := m.fields[m.fcur].hint; h != "" {
		b.WriteString(subtleStyle.Render("  "+h) + "\n")
	}

	b.WriteString("\n" + m.preview())

	if m.showVars {
		b.WriteString("\n" + sectionStyle.Render(varsHelp()) + "\n")
	}
	if m.optErr != "" {
		b.WriteString("\n" + errStyle.Render("✗ "+m.optErr) + "\n")
	}
	if m.optMsg != "" {
		b.WriteString("\n" + okStyle.Render("✓ "+m.optMsg) + "\n")
	}
	b.WriteString("\n" + help("↑/↓", "campo", "←/→", "mudar", "tab", "variáveis", "ctrl+s", "salvar padrão", "enter", "revisar", "esc", "voltar"))
	return b.String()
}

// preview mostra o caminho que a faixa de exemplo teria (e o do M3U).
func (m Model) preview() string {
	t := m.sample()
	var b strings.Builder
	b.WriteString(subtleStyle.Render("Prévia — "+t.ArtistsString()+" - "+t.Title) + "\n")
	p, err := engine.PathFor(m.c, t)
	if err != nil {
		b.WriteString(errStyle.Render("  ✗ "+err.Error()) + "\n")
	} else {
		b.WriteString(m.trunc("  "+infoStyle.Render(p), 0) + "\n")
	}
	if m.c.M3U {
		name := m.c.M3UName
		if name == "" {
			name = m3u.DefaultName
		}
		if mp, err := pathtpl.RenderList(name, t, "m3u8", pathtpl.Options{BaseDir: m.c.OutputDir, Restrict: pathtpl.Restrict(m.c.Restrict)}); err == nil {
			if !strings.HasSuffix(mp, ".m3u8") && !strings.HasSuffix(mp, ".m3u") {
				mp += ".m3u8"
			}
			b.WriteString(m.trunc("  "+subtleStyle.Render("M3U: ")+infoStyle.Render(mp), 0) + "\n")
		} else {
			b.WriteString(errStyle.Render("  ✗ M3U: "+err.Error()) + "\n")
		}
	}
	return b.String()
}

func varsHelp() string {
	var lines []string
	for _, v := range pathtpl.Vars {
		lines = append(lines, keyStyle.Render(fmt.Sprintf("{%s}", v.Name))+" "+subtleStyle.Render(v.Desc))
	}
	lines = append(lines, "", subtleStyle.Render("Zeros à esquerda: {track-number:02}. Terminar em / usa \"{artists} - {title}\"."))
	// Duas colunas quando couber.
	half := (len(pathtpl.Vars) + 1) / 2
	var out []string
	for i := 0; i < half; i++ {
		left := lines[i]
		right := ""
		if i+half < len(pathtpl.Vars) {
			right = lines[i+half]
		}
		out = append(out, lipgloss.NewStyle().Width(50).Render(left)+" "+right)
	}
	out = append(out, lines[len(pathtpl.Vars):]...)
	return strings.Join(out, "\n")
}

// plan gera os jobs e a seleção inicial da tela de revisão.
func (m *Model) plan() error {
	jobs, err := engine.Plan(m.c, m.lists)
	if err != nil {
		return err
	}
	m.jobs = jobs
	m.sel = make([]bool, len(jobs))
	m.exists = make([]bool, len(jobs))
	for i, j := range jobs {
		m.sel[i] = true
		m.exists[i] = fileExists(j.Path)
	}
	m.rcur = 0
	return nil
}

// rel encurta o caminho para exibição (relativo à pasta base).
func (m Model) rel(p string) string {
	base := pathtpl.ExpandPath(m.c.OutputDir)
	if r, err := filepath.Rel(base, p); err == nil && !strings.HasPrefix(r, "..") {
		return r
	}
	return p
}
