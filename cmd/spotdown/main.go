// spotdown baixa faixas, álbuns e playlists do Spotify: metadados das
// páginas públicas do Spotify, áudio do YouTube Music via yt-dlp, tags/capa/
// letra no arquivo e caminho de saída por template (como o spotdl). Sem
// links, abre a TUI; com links, baixa direto.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/vinicgobbi/spotdown/internal/cli"
	"github.com/vinicgobbi/spotdown/internal/config"
	"github.com/vinicgobbi/spotdown/internal/lyrics"
	"github.com/vinicgobbi/spotdown/internal/pathtpl"
	"github.com/vinicgobbi/spotdown/internal/spotify"
	"github.com/vinicgobbi/spotdown/internal/ui"
	"github.com/vinicgobbi/spotdown/internal/ytmusic"
)

// version é definido na compilação (-ldflags "-X main.version=...").
var version = "dev"

type options struct {
	cfg        config.Config
	configPath string
	dryRun     bool
	save       bool
	login      bool
	logout     bool
	version    bool
	vars       bool
}

func parseFlags(o *options) (urls []string) {
	fs := flag.CommandLine
	c := &o.cfg
	fs.StringVar(&o.configPath, "config", o.configPath, "arquivo de configuração")
	fs.StringVar(&c.Output, "output", c.Output, "template do caminho de cada faixa (veja --vars)")
	fs.StringVar(&c.Output, "o", c.Output, "atalho para --output")
	fs.StringVar(&c.OutputDir, "output-dir", c.OutputDir, "pasta base para templates relativos")
	fs.StringVar(&c.Format, "format", c.Format, "formato: mp3, m4a, opus, flac ou ogg")
	fs.StringVar(&c.Bitrate, "bitrate", c.Bitrate, "\"auto\" (melhor qualidade) ou um bitrate como 320k")
	fs.StringVar(&c.Restrict, "restrict", c.Restrict, "caracteres nos nomes: none, ascii ou strict")
	fs.StringVar(&c.Overwrite, "overwrite", c.Overwrite, "arquivo existente: skip, metadata ou force")
	fs.BoolVar(&c.M3U, "m3u", c.M3U, "gerar .m3u8 para cada álbum/playlist")
	fs.StringVar(&c.M3UName, "m3u-name", c.M3UName, "template do nome do .m3u8")
	fs.BoolVar(&c.M3UAbsolute, "m3u-absolute", c.M3UAbsolute, "caminhos absolutos no .m3u8")
	fs.StringVar(&c.Lyrics, "lyrics", c.Lyrics, "letras: embed, lrc, both ou off")
	fs.IntVar(&c.Threads, "threads", c.Threads, "downloads simultâneos (1 a 16)")
	fs.StringVar(&c.SpotifySource, "spotify-source", c.SpotifySource, "de onde vêm as playlists: web (padrão, sem conta) ou official (API oficial com suas credenciais)")
	fs.StringVar(&c.YtDlp, "yt-dlp", c.YtDlp, "caminho do yt-dlp")
	fs.StringVar(&c.Cookies, "cookies", c.Cookies, "cookies.txt para o yt-dlp (opcional)")
	fs.BoolVar(&o.dryRun, "dry-run", false, "só listar as faixas e os caminhos que seriam gerados, sem baixar")
	fs.BoolVar(&o.login, "login", false, "entrar com a conta do Spotify (playlists suas ou colaborativas sem o limite de 100 faixas) e sair")
	fs.BoolVar(&o.logout, "logout", false, "apagar o login do Spotify e sair")
	fs.BoolVar(&o.save, "save-config", false, "gravar as opções passadas por flag no arquivo de configuração")
	fs.BoolVar(&o.vars, "vars", false, "listar as variáveis do template e sair")
	fs.BoolVar(&o.version, "version", false, "mostrar a versão e sair")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Uso: spotdown [opções] [LINK...]

Baixa faixas, álbuns e playlists do Spotify (links open.spotify.com ou
spotify:...). Sem links, abre a interface interativa.

Exemplos:
  spotdown
  spotdown -o "{artist}/{album}/{track-number:02} - {title}" --format opus LINK
  spotdown --m3u --m3u-name "Playlists/{list-name}.m3u8" LINK_DA_PLAYLIST

Opções:
`)
		fs.PrintDefaults()
	}
	return parseInterspersed(fs, os.Args[1:])
}

// parseInterspersed aceita flags depois dos links ("spotdown LINK --m3u"):
// o pacote flag para no primeiro argumento que não é flag, então o resto é
// relido a cada link encontrado.
func parseInterspersed(fs *flag.FlagSet, args []string) []string {
	var pos []string
	for {
		_ = fs.Parse(args) // ExitOnError: erro já encerra o programa
		rest := fs.Args()
		if len(rest) == 0 {
			return pos
		}
		pos = append(pos, rest[0])
		args = rest[1:]
	}
}

func main() { os.Exit(run()) }

func run() int {
	setUserAgents()

	// Lê a config antes das flags, para que os padrões das flags sejam os valores dela.
	o := options{}
	o.configPath, _ = config.Path()
	for i, a := range os.Args[1:] {
		if (a == "--config" || a == "-config") && i+2 < len(os.Args) {
			o.configPath = os.Args[i+2]
		}
	}
	file, created, err := config.Load(o.configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, ui.ErrorText(err.Error()))
		return 2
	}
	o.cfg = file
	urls := parseFlags(&o)

	if o.version {
		fmt.Println("spotdown", version)
		return 0
	}
	if o.vars {
		for _, v := range pathtpl.Vars {
			fmt.Printf("  {%s}\t%s\n", v.Name, v.Desc)
		}
		return 0
	}
	if err := o.cfg.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, ui.ErrorText(err.Error()))
		return 2
	}
	if o.save {
		if err := config.Save(o.configPath, o.cfg); err != nil {
			fmt.Fprintln(os.Stderr, ui.ErrorText(err.Error()))
			return 2
		}
		file = o.cfg
		fmt.Fprintln(os.Stderr, "configuração gravada em", o.configPath)
	}
	// Depois de gravar: credenciais do ambiente não vão parar no arquivo.
	o.cfg.ApplyEnv()

	env := &cli.Env{UI: ui.New(), Cfg: o.cfg, File: file, ConfigPath: o.configPath, Version: version}

	if o.login || o.logout {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		var err error
		if o.logout {
			err = cli.Logout(env)
		} else {
			err = cli.Login(ctx, env)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, ui.ErrorText(err.Error()))
			return 1
		}
		return 0
	}

	if len(urls) == 0 {
		if !env.UI.Interactive {
			fmt.Fprintln(os.Stderr, ui.ErrorText("sem terminal interativo, passe os links na linha de comando"))
			return 2
		}
		env.CheckTools(context.Background())
		if err := cli.Interactive(env); err != nil {
			fmt.Fprintln(os.Stderr, ui.ErrorText(err.Error()))
			return 1
		}
		return 0
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if created {
		env.UI.Hint("configuração criada em %s", o.configPath)
	}
	fin, err := cli.Download(ctx, env, urls, cli.DownloadOptions{DryRun: o.dryRun})
	switch {
	case errors.Is(err, context.Canceled):
		return 130
	case err != nil:
		fmt.Fprintln(os.Stderr, ui.ErrorText(err.Error()))
		return 2
	case cli.Failed(fin):
		return 1
	}
	return 0
}

func setUserAgents() {
	ua := "spotdown/" + version + " (+https://github.com/vinicgobbi/spotdown)"
	spotify.UserAgent, ytmusic.UserAgent, lyrics.UserAgent = ua, ua, ua
}
