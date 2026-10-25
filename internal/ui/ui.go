// Package ui reúne os prompts (huh) e a saída formatada, no mesmo estilo do
// deploy-tools. A mesma saída serve ao modo texto e ao painel da TUI (que
// redireciona Out e mostra os formulários dentro do painel; ver Host).
package ui

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"golang.org/x/term"
)

type UI struct {
	In          io.Reader
	Out         io.Writer
	Interactive bool // pode perguntar (stdin e stdout são terminais)
	Accessible  bool // prompts simples por linha (testes, leitores de tela)
	ClearScreen bool // Clear() limpa de fato (só num terminal de verdade)
	// Host, quando definido, mostra os formulários em vez de rodá-los no
	// terminal (a TUI os embute no painel).
	Host Host
}

// Host roda um formulário dentro de outro programa; devolve ErrAborted
// quando o usuário cancela. SetStatus troca a linha de status ao vivo e
// SetTasks o quadro das tarefas em curso.
type Host interface {
	RunForm(*huh.Form) error
	SetStatus(string)
	SetTasks(title string, tasks []Task)
}

// Task é uma tarefa em curso no quadro da TUI (ex.: uma faixa baixando).
type Task struct {
	Name  string
	Stage string
	Frac  float64 // progresso de 0 a 1; < 0 não mostra barra
}

var ErrAborted = errors.New("cancelado")

func New() *UI {
	interactive := term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
	if os.Getenv("NO_COLOR") != "" || !term.IsTerminal(int(os.Stdout.Fd())) {
		lipgloss.SetColorProfile(termenv.Ascii)
	}
	accessible := os.Getenv("ACCESSIBLE") != ""
	return &UI{
		In: os.Stdin, Out: os.Stdout, Interactive: interactive, Accessible: accessible,
		// Leitores de tela perdem o contexto quando a tela é apagada.
		ClearScreen: interactive && !accessible,
	}
}

var (
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	okStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	warnStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	errStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("9"))
	faintStyle = lipgloss.NewStyle().Faint(true)
	keyStyle   = lipgloss.NewStyle().Bold(true)
)

func (u *UI) Title(s string) { fmt.Fprintln(u.Out, titleStyle.Render(s)) }

// Header abre uma tela: o título, cada linha não vazia apagada e uma linha em branco.
func (u *UI) Header(title string, lines ...string) {
	u.Title(title)
	for _, l := range lines {
		if l != "" {
			fmt.Fprintln(u.Out, faintStyle.Render(l))
		}
	}
	fmt.Fprintln(u.Out)
}

// Tone colore um texto curto de status (sem quebra de linha).
type Tone int

const (
	Plain Tone = iota
	Good
	Idle
	Bad
)

func (u *UI) Tone(t Tone, s string) string {
	switch t {
	case Good:
		return okStyle.Render(s)
	case Idle:
		return faintStyle.Render(s)
	case Bad:
		return warnStyle.Render(s)
	}
	return s
}

// Hint imprime uma linha recuada e apagada: contexto que dá para pular.
func (u *UI) Hint(format string, a ...any) {
	fmt.Fprintln(u.Out, renderLines(faintStyle, "  "+fmt.Sprintf(format, a...)))
}
func (u *UI) OK(format string, a ...any) {
	fmt.Fprintln(u.Out, renderLines(okStyle, "✓ "+fmt.Sprintf(format, a...)))
}
func (u *UI) Warn(format string, a ...any) {
	fmt.Fprintln(u.Out, renderLines(warnStyle, "! "+fmt.Sprintf(format, a...)))
}
func (u *UI) Fail(format string, a ...any) {
	fmt.Fprintln(u.Out, renderLines(errStyle, "✗ ")+fmt.Sprintf(format, a...))
}
func (u *UI) Println(a ...any)               { fmt.Fprintln(u.Out, a...) }
func (u *UI) Printf(format string, a ...any) { fmt.Fprintf(u.Out, format, a...) }
func (u *UI) Faint(s string) string          { return faintStyle.Render(s) }
func ErrorText(s string) string              { return errStyle.Render("erro: ") + s }

// Status troca a linha de status ao vivo do painel (progresso). Fora da TUI
// não faz nada: no modo texto só ficam as linhas definitivas.
func (u *UI) Status(format string, a ...any) {
	if u.Host != nil {
		u.Host.SetStatus(fmt.Sprintf(format, a...))
	}
}

// Tasks troca o quadro das tarefas em curso; title "" o esconde. Como
// Status, fora da TUI não faz nada.
func (u *UI) Tasks(title string, tasks []Task) {
	if u.Host != nil {
		u.Host.SetTasks(title, tasks)
	}
}

// renderLines estiliza cada linha separadamente: renderizar um texto de
// várias linhas de uma vez completa todas com espaços até a mais larga.
func renderLines(st lipgloss.Style, s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = st.Render(l)
	}
	return strings.Join(lines, "\n")
}

// clearSeq vai para o início, limpa a tela e o histórico (como o clear(1)).
const clearSeq = "\033[H\033[2J\033[3J"

// Clear limpa o terminal para cada tela interativa começar do zero.
func (u *UI) Clear() {
	if u.ClearScreen {
		fmt.Fprint(u.Out, clearSeq)
	}
}

// KV imprime linhas "chave  valor" alinhadas.
func (u *UI) KV(pairs ...[2]string) {
	w := 0
	for _, p := range pairs {
		w = max(w, len([]rune(p[0])))
	}
	for _, p := range pairs {
		pad := strings.Repeat(" ", w-len([]rune(p[0])))
		fmt.Fprintf(u.Out, "  %s%s  %s\n", keyStyle.Render(p[0]), pad, p[1])
	}
}

// Steps imprime uma lista numerada.
func (u *UI) Steps(title string, steps ...string) {
	if len(steps) == 0 {
		return
	}
	fmt.Fprintln(u.Out)
	u.Title(title)
	for i, s := range steps {
		fmt.Fprintf(u.Out, "  %d. %s\n", i+1, s)
	}
}

// Run mostra um formulário; ctrl+c/esc viram ErrAborted.
func (u *UI) Run(groups ...*huh.Group) error {
	if !u.Interactive {
		return errors.New("é preciso um terminal interativo para perguntar (ou passe as flags)")
	}
	if u.Host != nil {
		// O host mostra os próprios atalhos.
		return u.Host.RunForm(huh.NewForm(groups...).WithTheme(huh.ThemeCharm()).WithShowHelp(false))
	}
	in := u.In
	if u.Accessible {
		// Cada formulário acessível lê a entrada com o próprio buffer;
		// entregar uma linha por Read evita que um formulário engula as
		// respostas dos seguintes.
		if _, ok := in.(*lineReader); !ok {
			u.In = &lineReader{r: bufio.NewReader(in)}
		}
		in = u.In
	}
	err := huh.NewForm(groups...).
		WithInput(in).WithOutput(u.Out).
		WithAccessible(u.Accessible).
		WithTheme(huh.ThemeCharm()).
		Run()
	if errors.Is(err, huh.ErrUserAborted) {
		return ErrAborted
	}
	return err
}

func (u *UI) Confirm(title, description string) (bool, error) {
	return u.ConfirmDefault(title, description, false)
}

// ConfirmDefault é o Confirm com def pré-selecionado (Enter aceita).
func (u *UI) ConfirmDefault(title, description string, def bool) (bool, error) {
	ok := def
	err := u.Run(huh.NewGroup(huh.NewConfirm().Title(title).Description(description).
		Affirmative("Sim").Negative("Não").Value(&ok)))
	return ok, err
}

func (u *UI) Select(title string, val *string, opts ...huh.Option[string]) error {
	return u.Run(huh.NewGroup(huh.NewSelect[string]().Title(title).Options(opts...).Value(val)))
}

func (u *UI) Input(title, placeholder string, val *string, validate func(string) error) error {
	in := huh.NewInput().Title(title).Placeholder(placeholder).Value(val)
	if validate != nil {
		in = in.Validate(validate)
	}
	return u.Run(huh.NewGroup(in))
}

type lineReader struct{ r *bufio.Reader }

func (l *lineReader) Read(p []byte) (int, error) {
	line, err := l.r.ReadSlice('\n')
	n := copy(p, line)
	if n < len(line) {
		// p pequeno demais: devolve o resto para o próximo Read.
		l.r = bufio.NewReader(io.MultiReader(bytes.NewReader(line[n:]), l.r))
		return n, nil
	}
	if err == bufio.ErrBufferFull {
		err = nil
	}
	return n, err
}
