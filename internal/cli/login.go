package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"runtime"

	"github.com/vinicgobbi/spotdown/internal/config"
	"github.com/vinicgobbi/spotdown/internal/spotify"
)

// openBrowser abre o link no navegador padrão, sem deixar a saída do
// xdg-open sujar o terminal (ou o painel da TUI).
func openBrowser(link string) error {
	bin := "xdg-open"
	if runtime.GOOS == "darwin" {
		bin = "open"
	}
	cmd := exec.Command(bin, link)
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// LoggedUser devolve o nome da conta logada ("" sem login válido para o
// Client ID atual).
func LoggedUser(env *Env) string {
	tok, err := spotify.LoadToken(config.TokenPath())
	if err != nil || tok == nil || tok.ClientID != env.Cfg.SpotifyClientID {
		return ""
	}
	if tok.User == "" {
		return "conta sem nome"
	}
	return tok.User
}

// Login entra com a conta do Spotify (Authorization Code + PKCE) e grava o token.
func Login(ctx context.Context, env *Env) error {
	u := env.UI
	id := env.Cfg.SpotifyClientID
	if id == "" {
		return errors.New("falta o Client ID do app do Spotify (spotify_client_id no config.toml, ou Opções › Fonte do Spotify)")
	}
	u.Println("Abrindo o navegador para você autorizar o spotdown na sua conta do Spotify…")
	u.Status("Aguardando a autorização no navegador… (ctrl+c duas vezes cancela)")
	tok, err := spotify.Login(ctx, id, openBrowser, func(link string) {
		u.Hint("Se o navegador não abrir, acesse:")
		u.Hint("%s", link)
		u.Println()
	})
	u.Status("")
	if err != nil {
		return err
	}
	if err := tok.Save(config.TokenPath()); err != nil {
		return err
	}
	who := tok.User
	if who == "" {
		who = "sua conta"
	}
	u.OK("Conectado como %s.", who)
	if env.Cfg.Official() {
		u.Hint("Playlists suas ou colaborativas agora vêm completas pela API oficial.")
	} else {
		u.Hint("A fonte atual é o web player; o login só é usado com spotify_source = \"official\".")
	}
	return nil
}

// Logout apaga o token gravado.
func Logout(env *Env) error {
	err := os.Remove(config.TokenPath())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	env.UI.OK("Login do Spotify removido.")
	return nil
}
