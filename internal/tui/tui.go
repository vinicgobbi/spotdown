// Package tui é a interface em tela cheia do spotdown, no estilo do
// deploy-tools: as ações numa barra lateral e um painel que descreve a
// selecionada e depois a executa. Cada ação roda na própria goroutine, a
// saída vai para o painel e os formulários huh aparecem nele (ver ui.Host).
//
// No modo acessível (ACCESSIBLE=1, e nos testes) os mesmos itens viram um
// menu por linhas.
package tui

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"

	"github.com/vinicgobbi/spotdown/internal/ui"
)

type Item struct {
	Label string
	// Desc é o texto do painel enquanto o item está selecionado. É calculado
	// quando a seleção muda, então pode mostrar contexto atual.
	Desc func() string
	CLI  string // o comando equivalente, mostrado abaixo da descrição
	// Run executa a ação; ctx é cancelado com ctrl+c duas vezes.
	Run func(ctx context.Context) error
}

type Section struct {
	Title string
	Items []Item
}

type App struct {
	Title    string
	Subtitle func() string // contexto ao lado do título
	// Notice é uma linha abaixo do cabeçalho (ex.: o yt-dlp está velho); ""
	// não mostra nada. Roda a cada redesenho, então deve ser barato.
	Notice   func() string
	Sections []Section
	// Rebuild, se definido, remonta as seções quando uma ação termina (ex.:
	// "Instalar no PATH" some depois de instalar).
	Rebuild func() []Section
}

// Run mostra app até o usuário sair.
func Run(u *ui.UI, app App) error {
	if u.Accessible || !u.Interactive {
		return plain(u, app)
	}
	_, err := run(u, app, nil, []tea.ProgramOption{tea.WithAltScreen()})
	return err
}

// run é a parte em tela cheia de Run; ready (testes) recebe o programa para
// mandar teclas depois de montado.
func run(u *ui.UI, app App, ready func(*tea.Program), opts []tea.ProgramOption) (*model, error) {
	m := newModel(u, app)
	p := tea.NewProgram(m, opts...)
	m.p = p

	oldOut := u.Out
	u.Out, u.Host = panelWriter{p}, (*host)(m)
	defer func() { u.Out, u.Host = oldOut, nil }()
	if ready != nil {
		go ready(p)
	}
	_, err := p.Run()
	if m.cancel != nil {
		m.cancel() // saiu com uma ação rodando (ctrl+c depois do fim): para ela
	}
	return m, err
}

// Capture roda f com u.Out indo para um buffer, para descrições montadas com
// as mesmas funções que imprimem a saída dos comandos.
func Capture(u *ui.UI, f func()) string {
	var b bytes.Buffer
	old := u.Out
	u.Out = &b
	defer func() { u.Out = old }()
	f()
	return strings.TrimRight(b.String(), "\n")
}

// plain é a alternativa por linhas: os mesmos itens num ui.Menu.
func plain(u *ui.UI, app App) error {
	header := func() error {
		sub := ""
		if app.Subtitle != nil {
			sub = app.Subtitle()
		}
		u.Header(app.Title, sub)
		if app.Notice != nil {
			if n := app.Notice(); n != "" {
				u.Hint("→ %s", n)
				u.Println()
			}
		}
		return nil
	}
	return u.Menu(header, "O que você quer fazer?", func() ([]ui.Entry, error) {
		if app.Rebuild != nil {
			app.Sections = app.Rebuild()
		}
		var es []ui.Entry
		for _, s := range app.Sections {
			for _, it := range s.Items {
				es = append(es, ui.Entry{Label: it.Label, Run: func() error { return it.Run(context.Background()) }})
			}
		}
		return append(es, ui.Entry{Label: "Sair"}), nil
	})
}

// ---- modelo bubbletea ----

type mode int

const (
	browsing mode = iota
	running
	done
)

type (
	outMsg    string
	statusMsg string
	tasksMsg  struct {
		title string
		tasks []ui.Task
	}
	doneMsg struct{ err error }
	formMsg struct {
		form *huh.Form
		done chan error
	}
)

type panelWriter struct{ p *tea.Program }

func (w panelWriter) Write(b []byte) (int, error) {
	w.p.Send(outMsg(string(b)))
	return len(b), nil
}

type host model

// RunForm é chamado da goroutine da ação e bloqueia até o usuário responder
// (ou cancelar) o formulário no painel.
func (h *host) RunForm(f *huh.Form) error {
	done := make(chan error, 1)
	h.p.Send(formMsg{f, done})
	return <-done
}

func (h *host) SetStatus(s string) { h.p.Send(statusMsg(s)) }

func (h *host) SetTasks(title string, tasks []ui.Task) {
	h.p.Send(tasksMsg{title, append([]ui.Task(nil), tasks...)})
}

type model struct {
	u    *ui.UI
	app  App
	p    *tea.Program
	refs [][2]int // índice plano -> seção, item

	cursor int
	mode   mode
	desc   string

	log       strings.Builder
	status    string
	tasks     tasksMsg // quadro das tarefas em curso; title "" esconde
	result    error
	form      *huh.Form
	formDone  chan error
	vp        viewport.Model
	cancel    context.CancelFunc
	cancelAsk bool

	w, h int
}

func newModel(u *ui.UI, app App) *model {
	m := &model{u: u, app: app}
	m.index()
	m.vp = viewport.New(0, 0)
	m.loadDesc()
	return m
}

// index refaz o mapa do índice plano para seção/item.
func (m *model) index() {
	m.refs = m.refs[:0]
	for si, s := range m.app.Sections {
		for ii := range s.Items {
			m.refs = append(m.refs, [2]int{si, ii})
		}
	}
}

// rebuild remonta as seções mantendo o cursor no mesmo item, se ele ainda existir.
func (m *model) rebuild() {
	if m.app.Rebuild == nil {
		return
	}
	label := m.item().Label
	m.app.Sections = m.app.Rebuild()
	m.index()
	m.cursor = min(m.cursor, len(m.refs)-1)
	for i, r := range m.refs {
		if m.app.Sections[r[0]].Items[r[1]].Label == label {
			m.cursor = i
			break
		}
	}
}

func (m *model) item() Item {
	r := m.refs[m.cursor]
	return m.app.Sections[r[0]].Items[r[1]]
}

func (m *model) loadDesc() {
	it := m.item()
	m.desc = ""
	if it.Desc != nil {
		m.desc = it.Desc()
	}
}

func (m *model) Init() tea.Cmd { return nil }

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.layout()
		if m.form != nil {
			m.form = m.form.WithWidth(m.panelW())
			fm, cmd := m.form.Update(msg)
			m.form = fm.(*huh.Form)
			m.refresh()
			return m, cmd
		}
		m.refresh()
		return m, nil
	case outMsg:
		m.log.WriteString(string(msg))
		m.refresh()
		return m, nil
	case statusMsg:
		m.status = string(msg)
		m.refresh()
		return m, nil
	case tasksMsg:
		m.tasks = msg
		m.layout()
		m.refresh()
		return m, nil
	case formMsg:
		m.form, m.formDone = msg.form.WithWidth(m.panelW()), msg.done
		cmd := m.form.Init()
		m.refresh()
		return m, cmd
	case doneMsg:
		m.mode, m.result, m.status, m.cancelAsk = done, msg.err, "", false
		m.tasks = tasksMsg{}
		m.layout()
		m.cancel()
		m.cancel = nil
		m.refresh()
		return m, nil
	}

	if m.form != nil {
		if k, ok := msg.(tea.KeyMsg); ok && k.String() == "esc" {
			m.finishForm(ui.ErrAborted)
			return m, nil
		}
		fm, cmd := m.form.Update(msg)
		m.form = fm.(*huh.Form)
		switch m.form.State {
		case huh.StateCompleted:
			m.finishForm(nil)
		case huh.StateAborted:
			m.finishForm(ui.ErrAborted)
		default:
			m.refresh()
		}
		return m, cmd
	}

	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch m.mode {
	case browsing:
		switch k.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "up", "k":
			m.move(-1)
		case "down", "j":
			m.move(1)
		case "home", "g":
			m.move(-len(m.refs))
		case "end", "G":
			m.move(len(m.refs))
		case "enter", "right", "l":
			m.start()
		}
	case running:
		// Sair no meio deixaria downloads pela metade: ctrl+c pede
		// confirmação e cancela a ação, que termina limpando o que fez.
		switch k.String() {
		case "ctrl+c":
			if m.cancelAsk {
				m.cancel()
				m.status = "cancelando…"
			}
			m.cancelAsk = !m.cancelAsk
		case "esc":
			m.cancelAsk = false
		default:
			var cmd tea.Cmd
			m.vp, cmd = m.vp.Update(msg)
			return m, cmd
		}
	case done:
		switch k.String() {
		case "enter", "esc", "left", "h", "q":
			m.mode = browsing
			m.rebuild()
			m.loadDesc() // a ação pode ter mudado o que o item mostra
		case "ctrl+c":
			return m, tea.Quit
		default:
			var cmd tea.Cmd
			m.vp, cmd = m.vp.Update(msg)
			return m, cmd
		}
	}
	return m, nil
}

func (m *model) move(d int) {
	m.cursor = max(0, min(len(m.refs)-1, m.cursor+d))
	m.loadDesc()
}

func (m *model) start() {
	it := m.item()
	m.mode, m.result, m.status, m.tasks = running, nil, "", tasksMsg{}
	m.log.Reset()
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.refresh()
	go func() {
		m.p.Send(doneMsg{safeRun(ctx, it.Run)})
	}()
}

func safeRun(ctx context.Context, f func(context.Context) error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("erro interno: %v", r)
		}
	}()
	return f(ctx)
}

func (m *model) finishForm(err error) {
	m.formDone <- err
	m.form, m.formDone = nil, nil
	m.refresh()
}

// ---- layout ----

var (
	accent    = lipgloss.Color("12")
	titleSt   = lipgloss.NewStyle().Bold(true).Foreground(accent)
	faintSt   = lipgloss.NewStyle().Faint(true)
	selSt     = lipgloss.NewStyle().Bold(true).Foreground(accent)
	okSt      = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	noticeSt  = lipgloss.NewStyle().Foreground(lipgloss.Color("14"))
	warnSt    = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	boxSt     = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("8")).Padding(0, 1)
	activeBox = boxSt.BorderForeground(accent)
)

func (m *model) sideW() int {
	w := 18
	for _, s := range m.app.Sections {
		for _, it := range s.Items {
			w = max(w, lipgloss.Width(it.Label)+4)
		}
	}
	return w
}

// panelW é a largura do texto dentro da caixa do painel.
func (m *model) panelW() int { return max(20, m.w-m.sideW()-4-4) }

// bodyH é a altura dentro das caixas (cabeçalho e rodapé ocupam uma linha cada).
func (m *model) bodyH() int {
	h := m.h - 2 - 2
	if m.notice() != "" {
		h--
	}
	return max(5, h)
}

// tasksH é a altura total do quadro de tarefas (com a borda); 0 sem quadro.
// Cabe no máximo metade do corpo; o resto vira "… e mais N".
func (m *model) tasksH() int {
	if m.tasks.title == "" || m.mode == browsing {
		return 0
	}
	rows := min(max(1, len(m.tasks.tasks)), max(1, m.bodyH()/2-3))
	return rows + 1 + 2 // título + bordas
}

func (m *model) layout() {
	m.vp.Width = m.panelW()
	m.vp.Height = max(1, m.bodyH()-m.tasksH()-2) // título do painel + linha em branco
}

// refresh remonta o conteúdo do painel de uma ação em andamento/concluída.
func (m *model) refresh() {
	if m.mode == browsing {
		return
	}
	var parts []string
	if out := strings.TrimRight(m.log.String(), "\n"); out != "" {
		parts = append(parts, wrap(out, m.panelW()))
	}
	if m.status != "" && m.form == nil {
		parts = append(parts, noticeSt.Render(wrap(m.status, m.panelW())))
	}
	if m.form != nil {
		parts = append(parts, m.form.View())
	}
	if m.mode == done {
		switch {
		case m.result == nil && len(parts) > 0:
			parts = append(parts, faintSt.Render("Concluído."))
		case m.result == nil:
			parts = append(parts, okSt.Render("✓ Concluído."))
		case m.result == ui.ErrAborted || m.result == context.Canceled:
			parts = append(parts, warnSt.Render("Cancelado."))
		default:
			parts = append(parts, wrap(ui.ErrorText(m.result.Error()), m.panelW()))
		}
	}
	m.vp.SetContent(strings.Join(parts, "\n\n"))
	m.vp.GotoBottom()
}

// listPrefix casa o recuo de uma linha mais um marcador de lista ("  1. ",
// "  · ", "✓ ", "[3/10] ✓ "), para as linhas quebradas manterem o recuo.
var listPrefix = regexp.MustCompile(`^ *(\d+\. |\[\d+/\d+\] [·✓!✗?↻\-] |[·•\-✓!✗?↻] )?`)

func wrap(s string, w int) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if lipgloss.Width(l) <= w {
			continue
		}
		prefix := listPrefix.FindString(l)
		if len(prefix) > w/2 {
			prefix = ""
		}
		pad := strings.Repeat(" ", lipgloss.Width(prefix))
		body := lipgloss.NewStyle().Width(w - len(pad)).Render(l[len(prefix):])
		parts := strings.Split(body, "\n")
		for j := range parts {
			parts[j] = strings.TrimRight(parts[j], " ")
			if j == 0 {
				parts[j] = prefix + parts[j]
			} else {
				parts[j] = pad + parts[j]
			}
		}
		lines[i] = strings.Join(parts, "\n")
	}
	return strings.Join(lines, "\n")
}

func (m *model) View() string {
	if m.w == 0 {
		return ""
	}
	if m.w < 60 || m.h < 16 {
		return "Terminal pequeno demais: aumente a janela (mínimo 60×16).\n\nq sai."
	}
	m.layout() // a linha de aviso pode aparecer e sumir depois de uma ação
	return lipgloss.JoinVertical(lipgloss.Left, m.header(), m.body(), m.footer())
}

func (m *model) header() string {
	left := titleSt.Render(m.app.Title)
	if m.app.Subtitle != nil {
		if sub := m.app.Subtitle(); sub != "" {
			left += faintSt.Render("  ·  " + sub)
		}
	}
	line := " " + truncate(left, m.w-2)
	if n := m.notice(); n != "" {
		line += "\n " + noticeSt.Render(truncate("→ "+n, m.w-2))
	}
	return line
}

func (m *model) notice() string {
	if m.app.Notice == nil {
		return ""
	}
	return m.app.Notice()
}

// truncate corta s em w colunas, com reticências.
func truncate(s string, w int) string {
	if lipgloss.Width(s) <= w || w < 2 {
		return s
	}
	r := []rune(s)
	for len(r) > 0 && lipgloss.Width(string(r))+1 > w {
		r = r[:len(r)-1]
	}
	return string(r) + "…"
}

func (m *model) body() string {
	h := m.bodyH()
	side := m.sidebar()
	sideBox, panelBox := activeBox, boxSt
	if m.mode != browsing {
		sideBox, panelBox = boxSt, activeBox
	}
	l := sideBox.Width(m.sideW()).Height(h).Render(side)
	th := m.tasksH()
	r := panelBox.Width(m.panelW() + 2).Height(h - th).Render(m.panel())
	if th > 0 {
		r = lipgloss.JoinVertical(lipgloss.Left, r,
			boxSt.Width(m.panelW()+2).Height(th-2).Render(m.tasksView(th-3)))
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, l, r)
}

// tasksView desenha o quadro de tarefas com até rows linhas de faixas.
func (m *model) tasksView(rows int) string {
	ts := m.tasks.tasks
	lines := []string{titleSt.Render(m.tasks.title)}
	if len(ts) == 0 {
		lines = append(lines, faintSt.Render("aguardando…"))
	}
	shown := ts
	if len(ts) > rows {
		shown = ts[:rows-1]
	}
	w := m.panelW()
	for _, t := range shown {
		right := t.Stage
		if t.Frac >= 0 {
			right = bar(t.Frac, 10) + fmt.Sprintf(" %3.0f%%", t.Frac*100)
		}
		right = faintSt.Render(right)
		nameW := max(8, w-lipgloss.Width(right)-2)
		name := truncate(t.Name, nameW)
		pad := strings.Repeat(" ", max(1, w-lipgloss.Width(name)-lipgloss.Width(right)))
		lines = append(lines, name+pad+right)
	}
	if len(shown) < len(ts) {
		lines = append(lines, faintSt.Render(fmt.Sprintf("… e mais %d", len(ts)-len(shown))))
	}
	return strings.Join(lines, "\n")
}

// bar desenha uma barra de progresso de w colunas.
func bar(f float64, w int) string {
	n := int(max(0, min(1, f))*float64(w) + 0.5)
	return okSt.Render(strings.Repeat("━", n)) + faintSt.Render(strings.Repeat("━", w-n))
}

func (m *model) sidebar() string {
	var b strings.Builder
	i := 0
	for si, s := range m.app.Sections {
		if si > 0 {
			b.WriteString("\n")
		}
		b.WriteString(faintSt.Render(strings.ToUpper(s.Title)) + "\n")
		for _, it := range s.Items {
			label := it.Label
			switch {
			case i == m.cursor && m.mode == browsing:
				label = selSt.Render("▸ " + label)
			case i == m.cursor:
				label = selSt.Render("● " + label)
			default:
				label = "  " + label
			}
			b.WriteString(label + "\n")
			i++
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m *model) panel() string {
	it := m.item()
	title := titleSt.Render(it.Label)
	if m.mode != browsing {
		return title + "\n\n" + m.vp.View()
	}
	var parts []string
	if m.desc != "" {
		parts = append(parts, wrap(m.desc, m.panelW()))
	}
	if it.CLI != "" {
		parts = append(parts, faintSt.Render("Pelo terminal:")+"\n"+wrap(faintSt.Render(it.CLI), m.panelW()))
	}
	body := strings.Join(parts, "\n\n")
	// Mantém a descrição dentro da caixa: corta o que não couber.
	if lines := strings.Split(body, "\n"); len(lines) > m.bodyH()-2 {
		body = strings.Join(lines[:m.bodyH()-3], "\n") + "\n" + faintSt.Render("…")
	}
	return title + "\n\n" + body
}

func (m *model) footer() string {
	var keys string
	switch {
	case m.form != nil:
		keys = "↑↓←→ escolher · enter confirmar · esc cancelar"
	case m.mode == running && m.cancelAsk:
		keys = "ctrl+c de novo cancela · esc continua"
	case m.mode == running:
		keys = "executando… · ↑↓ rolar · ctrl+c cancelar"
	case m.mode == done:
		keys = "enter/esc voltar ao menu · ↑↓ rolar"
	default:
		keys = "↑↓ navegar · enter abrir · q sair"
	}
	return faintSt.Render(" " + keys)
}
