package tui

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vinicgobbi/spotdown/internal/ui"
)

func opts() []tea.ProgramOption {
	return []tea.ProgramOption{tea.WithInput(nil), tea.WithOutput(io.Discard), tea.WithoutSignalHandler()}
}

func keySender(p *tea.Program) func(tea.KeyType, ...rune) {
	return func(k tea.KeyType, runes ...rune) {
		p.Send(tea.KeyMsg{Type: k, Runes: runes})
		time.Sleep(50 * time.Millisecond)
	}
}

// TestFormInPanel dirige o programa bubbletea de verdade: esc cancela um
// formulário, o formulário de uma ação é respondido no painel e a saída vai
// para o painel, não para o terminal.
func TestFormInPanel(t *testing.T) {
	var out bytes.Buffer
	u := &ui.UI{Out: &out, Interactive: true}
	var name string
	var cancelErr error
	app := App{
		Title: "teste",
		Sections: []Section{{Title: "Ações", Items: []Item{
			{Label: "Cancelar", Desc: func() string { return "descrição viva" }, Run: func(context.Context) error {
				_, cancelErr = u.Confirm("Seguir?", "")
				return cancelErr
			}},
			{Label: "Perguntar", Run: func(context.Context) error {
				if err := u.Input("Nome", "", &name, nil); err != nil {
					return err
				}
				u.OK("olá %s", name)
				u.Status("quase")
				return nil
			}},
		}}},
	}
	ready := func(p *tea.Program) {
		key := keySender(p)
		p.Send(tea.WindowSizeMsg{Width: 100, Height: 30})
		key(tea.KeyEnter) // Cancelar: o confirm abre no painel
		key(tea.KeyEsc)   // e é cancelado
		key(tea.KeyEnter) // volta ao menu
		key(tea.KeyDown)
		key(tea.KeyEnter) // Perguntar
		key(tea.KeyRunes, []rune("Ana")...)
		key(tea.KeyEnter) // envia
		key(tea.KeyEnter) // volta ao menu
		key(tea.KeyRunes, 'q')
	}
	m, err := run(u, app, ready, opts())
	if err != nil {
		t.Fatal(err)
	}
	if !errors.Is(cancelErr, ui.ErrAborted) {
		t.Fatalf("esc: err = %v", cancelErr)
	}
	if name != "Ana" {
		t.Fatalf("name = %q", name)
	}
	if log := m.log.String(); !strings.Contains(log, "olá Ana") {
		t.Fatalf("painel:\n%s", log)
	}
	if m.status != "" {
		t.Fatalf("status não foi limpo ao terminar: %q", m.status)
	}
	if out.Len() != 0 {
		t.Fatalf("saída vazou para o terminal: %q", out.String())
	}
	if u.Out != &out || u.Host != nil {
		t.Fatal("saídas não restauradas")
	}
	if v := m.View(); !strings.Contains(v, "AÇÕES") || !strings.Contains(v, "Perguntar") {
		t.Fatalf("view:\n%s", v)
	}
}

// TestCancelRunning: ctrl+c uma vez só pede confirmação; a segunda cancela
// o contexto da ação, que termina com "Cancelado.".
func TestCancelRunning(t *testing.T) {
	u := &ui.UI{Out: io.Discard, Interactive: true}
	started := make(chan struct{})
	var ctxErr error
	app := App{Title: "teste", Sections: []Section{{Title: "Ações", Items: []Item{
		{Label: "Demorar", Run: func(ctx context.Context) error {
			u.Status("trabalhando")
			close(started)
			<-ctx.Done()
			ctxErr = ctx.Err()
			return ctx.Err()
		}},
	}}}}
	var afterFirst, afterCancel string
	ready := func(p *tea.Program) {
		key := keySender(p)
		p.Send(tea.WindowSizeMsg{Width: 100, Height: 30})
		key(tea.KeyEnter)
		<-started
		time.Sleep(50 * time.Millisecond)
		key(tea.KeyCtrlC)
		afterFirst = "ok"
		key(tea.KeyCtrlC)
		time.Sleep(100 * time.Millisecond)
		afterCancel = "ok"
		key(tea.KeyEnter)
		key(tea.KeyRunes, 'q')
	}
	m, err := run(u, app, ready, opts())
	if err != nil {
		t.Fatal(err)
	}
	if afterFirst == "" || afterCancel == "" || !errors.Is(ctxErr, context.Canceled) {
		t.Fatalf("ctx = %v", ctxErr)
	}
	if !errors.Is(m.result, context.Canceled) {
		t.Fatalf("result = %v", m.result)
	}
}

func TestStatusShownWhileRunning(t *testing.T) {
	u := &ui.UI{Out: io.Discard, Interactive: true}
	m := newModel(u, App{Title: "t", Sections: []Section{{Title: "A", Items: []Item{{Label: "x"}}}}})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.mode = running
	m.Update(outMsg("linha 1\n"))
	m.Update(statusMsg("Baixando 3/10"))
	if v := m.View(); !strings.Contains(v, "linha 1") || !strings.Contains(v, "Baixando 3/10") || !strings.Contains(v, "ctrl+c cancelar") {
		t.Fatalf("view:\n%s", v)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if !strings.Contains(m.View(), "ctrl+c de novo cancela") {
		t.Fatal("sem pedido de confirmação")
	}
}

func TestPlainMenuAccessible(t *testing.T) {
	var out bytes.Buffer
	ran := false
	u := &ui.UI{In: strings.NewReader("1\n2\n"), Out: &out, Interactive: true, Accessible: true}
	app := App{Title: "spotdown", Sections: []Section{{Title: "A", Items: []Item{
		{Label: "Fazer", Run: func(context.Context) error { ran = true; u.OK("feito"); return nil }},
	}}}}
	if err := Run(u, app); err != nil {
		t.Fatal(err)
	}
	if !ran || !strings.Contains(out.String(), "feito") {
		t.Fatalf("saída:\n%s", out.String())
	}
}

func TestRebuildAfterAction(t *testing.T) {
	u := &ui.UI{Out: io.Discard, Interactive: true}
	installed := false
	items := func() []Section {
		its := []Item{{Label: "Outra", Run: func(context.Context) error { return nil }}}
		if !installed {
			its = append(its, Item{Label: "Instalar", Run: func(context.Context) error { installed = true; return nil }})
		}
		return []Section{{Title: "Sistema", Items: its}}
	}
	app := App{Title: "t", Sections: items(), Rebuild: items}
	ready := func(p *tea.Program) {
		key := keySender(p)
		p.Send(tea.WindowSizeMsg{Width: 100, Height: 30})
		key(tea.KeyDown)
		key(tea.KeyEnter) // Instalar
		time.Sleep(50 * time.Millisecond)
		key(tea.KeyEnter) // volta ao menu: o item some
		key(tea.KeyRunes, 'q')
	}
	m, err := run(u, app, ready, opts())
	if err != nil {
		t.Fatal(err)
	}
	if !installed || len(m.refs) != 1 || m.item().Label != "Outra" || strings.Contains(m.View(), "Instalar") {
		t.Fatalf("installed=%v refs=%d item=%q\n%s", installed, len(m.refs), m.item().Label, m.View())
	}
}

// TestTasksBox: as faixas em curso ficam num quadro abaixo do log, com
// todas as que couberem e o resto resumido.
func TestTasksBox(t *testing.T) {
	u := &ui.UI{Out: io.Discard, Interactive: true}
	m := newModel(u, App{Title: "t", Sections: []Section{{Title: "A", Items: []Item{{Label: "x"}}}}})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.mode = running
	m.Update(outMsg("linha do log\n"))
	m.Update(tasksMsg{"Baixando 2/10", []ui.Task{
		{Name: "Artista - Faixa A", Stage: "buscando", Frac: -1},
		{Name: "Artista - Faixa B", Stage: "baixando", Frac: 0.5},
	}})
	v := m.View()
	for _, s := range []string{"linha do log", "Baixando 2/10", "Faixa A", "buscando", "Faixa B", "50%"} {
		if !strings.Contains(v, s) {
			t.Fatalf("falta %q:\n%s", s, v)
		}
	}
	if strings.Index(v, "linha do log") > strings.Index(v, "Baixando 2/10") {
		t.Fatalf("quadro acima do log:\n%s", v)
	}
	if n := len(strings.Split(v, "\n")); n != 30 {
		t.Fatalf("altura %d, esperado 30:\n%s", n, v)
	}

	many := make([]ui.Task, 40)
	for i := range many {
		many[i] = ui.Task{Name: "faixa", Stage: "tags", Frac: -1}
	}
	m.Update(tasksMsg{"Baixando 0/40", many})
	if v := m.View(); !strings.Contains(v, "… e mais") || len(strings.Split(v, "\n")) != 30 {
		t.Fatalf("view:\n%s", v)
	}

	m.cancel = func() {}
	m.Update(doneMsg{})
	if strings.Contains(m.View(), "Baixando") {
		t.Fatal("quadro ficou depois do fim")
	}
}
