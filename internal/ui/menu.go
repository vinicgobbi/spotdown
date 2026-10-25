package ui

import (
	"errors"

	"github.com/charmbracelet/huh"
)

// Entry é uma opção do menu. Sem Run, a opção encerra o menu (ex.: "Sair").
type Entry struct {
	Label string
	Title string // título da saída da ação (padrão: Label)
	Run   func() error
}

// Menu repete uma tela até escolherem uma opção sem Run (ou cancelarem).
// Depois de uma ação, a saída fica visível e o menu volta logo abaixo, para
// dar para ler o resultado sem um passo extra de "voltar". entries é chamado
// a cada volta, para as opções acompanharem o que a última ação mudou.
func (u *UI) Menu(header func() error, title string, entries func() ([]Entry, error)) error {
	fresh := true
	for {
		if fresh {
			u.Clear()
			if err := header(); err != nil {
				return err
			}
		}
		es, err := entries()
		if err != nil {
			return err
		}
		opts := make([]huh.Option[int], len(es))
		for i, e := range es {
			opts[i] = huh.NewOption(e.Label, i)
		}
		choice := 0
		if err := u.Run(huh.NewGroup(
			huh.NewSelect[int]().Title(title).Options(opts...).Value(&choice),
		)); err != nil {
			if errors.Is(err, ErrAborted) {
				return nil
			}
			return err
		}

		e := es[choice]
		if e.Run == nil {
			return nil
		}
		u.Clear()
		t := e.Title
		if t == "" {
			t = e.Label
		}
		u.Title(t)
		u.Println()
		err = e.Run()
		fresh = false
		switch {
		case errors.Is(err, ErrAborted):
			u.Println("Cancelado.")
		case err != nil:
			u.Println(ErrorText(err.Error()))
		}
		u.Println()
	}
}
