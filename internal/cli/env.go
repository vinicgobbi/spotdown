// Package cli tem as ações do spotdown: o download (o mesmo fluxo no modo
// texto e no painel da TUI), as opções e as ferramentas de sistema, e a
// montagem da TUI com elas.
package cli

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/vinicgobbi/spotdown/internal/config"
	"github.com/vinicgobbi/spotdown/internal/download"
	"github.com/vinicgobbi/spotdown/internal/track"
	"github.com/vinicgobbi/spotdown/internal/ui"
)

// Env é o estado compartilhado pelas ações.
type Env struct {
	UI         *ui.UI
	Cfg        config.Config // efetiva: arquivo + flags + ambiente
	File       config.Config // só o arquivo (o que as opções da TUI gravam)
	ConfigPath string
	Version    string

	// Resultado da última checagem do yt-dlp/ffmpeg (ver CheckTools).
	ToolsErr     error
	ToolsWarn    string
	ToolsVersion string

	// sample é a primeira faixa do último download, usada nas prévias.
	sample *track.Track
}

// Save aplica change à configuração efetiva e ao arquivo, e grava o arquivo.
func (e *Env) Save(change func(*config.Config)) error {
	f := e.File
	change(&f)
	if err := f.Validate(); err != nil {
		return err
	}
	if err := config.Save(e.ConfigPath, f); err != nil {
		return err
	}
	e.File = f
	change(&e.Cfg)
	return nil
}

// CheckTools atualiza ToolsErr/ToolsWarn.
func (e *Env) CheckTools(ctx context.Context) {
	o := download.Options{YtDlp: e.Cfg.YtDlp}
	e.ToolsWarn, e.ToolsErr = download.CheckTools(ctx, o, time.Now())
	e.ToolsVersion = download.Version(ctx, o)
}

// Sample devolve a faixa das prévias: a do último download ou uma de exemplo.
func (e *Env) Sample() track.Track {
	if e.sample != nil {
		return *e.sample
	}
	return track.Track{
		ID: "4cOdK2wGLETKBW3PvgPWqT", Title: "Never Gonna Give You Up", Artists: []string{"Rick Astley"},
		Album: "Whenever You Need Somebody", AlbumArtist: "Rick Astley", TrackNumber: 1, TracksCount: 10,
		DiscNumber: 1, DiscCount: 1, Date: "1987-11-12", DurationMs: 213573, Publisher: "RCA Records Label",
		ListName: "Minha playlist", ListPosition: 1, ListLength: 10,
	}
}

// Tilde abrevia a pasta do usuário para "~" (só na exibição).
func Tilde(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == home {
		return "~"
	}
	if strings.HasPrefix(p, home+"/") {
		return "~" + p[len(home):]
	}
	return p
}
