// Package plain é a saída em texto simples, usada quando não há terminal
// interativo (pipe, cron) ou com --no-tui.
package plain

import (
	"fmt"
	"io"
	"sync"

	"github.com/vinicgobbi/spotdown/internal/engine"
	"github.com/vinicgobbi/spotdown/internal/spotify"
)

const (
	verde    = "\033[0;32m"
	amarelo  = "\033[1;33m"
	vermelho = "\033[0;31m"
	azul     = "\033[0;34m"
	cinza    = "\033[0;90m"
	negrito  = "\033[1m"
	nc       = "\033[0m"
)

// Printer imprime os eventos do engine, uma linha por acontecimento.
type Printer struct {
	mu    sync.Mutex
	W     io.Writer
	Color bool
	Total int // quantidade de jobs (para o "[n/total]")
	done  int
}

func (p *Printer) c(code, s string) string {
	if !p.Color {
		return s
	}
	return code + s + nc
}

// KindName devolve o nome do tipo de link em português.
func KindName(k spotify.Kind) string {
	switch k {
	case spotify.KindAlbum:
		return "Álbum"
	case spotify.KindPlaylist:
		return "Playlist"
	}
	return "Faixa"
}

// Handle imprime um evento; os de progresso fino são ignorados.
func (p *Printer) Handle(ev any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch ev := ev.(type) {
	case engine.ResolveStartEvent:
		fmt.Fprintf(p.W, "%s %s\n", p.c(azul, "→"), ev.URL)
	case engine.ResolveDoneEvent:
		if ev.Err != nil {
			fmt.Fprintf(p.W, "  %s %v\n", p.c(vermelho, "✗"), ev.Err)
			return
		}
		l := ev.List
		fmt.Fprintf(p.W, "  %s %s %s: %d faixa(s)\n", p.c(verde, "✓"), KindName(l.Kind), p.c(negrito, l.Name), len(l.Tracks))
		for _, w := range l.Warnings {
			fmt.Fprintf(p.W, "  %s %s\n", p.c(amarelo, "!"), w)
		}
	case engine.WarningEvent:
		fmt.Fprintf(p.W, "%s %s\n", p.c(amarelo, "!"), ev.Msg)
	case engine.JobDoneEvent:
		p.done++
		r := ev.Result
		t := r.Job.Track
		name := t.ArtistsString() + " - " + t.Title
		prefix := p.c(cinza, fmt.Sprintf("[%d/%d]", p.done, p.Total))
		switch r.Status {
		case engine.Downloaded:
			fmt.Fprintf(p.W, "%s %s %s %s\n", prefix, p.c(verde, "✓"), name, p.c(cinza, "→ "+r.Job.Path))
		case engine.Skipped:
			fmt.Fprintf(p.W, "%s %s %s %s\n", prefix, p.c(cinza, "·"), name, p.c(cinza, "(já existe)"))
		case engine.Retagged:
			fmt.Fprintf(p.W, "%s %s %s %s\n", prefix, p.c(azul, "↻"), name, p.c(cinza, "(tags regravadas)"))
		case engine.NoMatch:
			fmt.Fprintf(p.W, "%s %s %s: %v\n", prefix, p.c(amarelo, "?"), name, r.Err)
		case engine.Canceled:
			fmt.Fprintf(p.W, "%s %s %s (cancelada)\n", prefix, p.c(cinza, "-"), name)
		default:
			fmt.Fprintf(p.W, "%s %s %s: %v\n", prefix, p.c(vermelho, "✗"), name, r.Err)
		}
	case engine.FinishedEvent:
		fmt.Fprintln(p.W)
		fmt.Fprintln(p.W, Summary(ev))
	}
}

// Summary resume a execução em uma linha, mais M3U e arquivo de falhas.
func Summary(f engine.FinishedEvent) string {
	counts := map[engine.Status]int{}
	for _, r := range f.Results {
		counts[r.Status]++
	}
	s := fmt.Sprintf("%d baixada(s), %d já existia(m), %d com tags regravadas, %d sem correspondência, %d com erro",
		counts[engine.Downloaded], counts[engine.Skipped], counts[engine.Retagged], counts[engine.NoMatch], counts[engine.Failed])
	if n := counts[engine.Canceled]; n > 0 {
		s += fmt.Sprintf(", %d cancelada(s)", n)
	}
	s += fmt.Sprintf(" em %s", f.Elapsed.Round(1e9))
	for _, m := range f.M3U {
		s += "\nM3U: " + m
	}
	if f.Failures != "" {
		s += "\nFalhas: " + f.Failures
	}
	return s
}
