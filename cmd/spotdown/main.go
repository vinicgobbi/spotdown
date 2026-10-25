// spotdown baixa faixas, álbuns e playlists do Spotify: metadados das
// páginas públicas do Spotify, áudio do YouTube Music via yt-dlp, tags/capa/
// letra no arquivo e caminho de saída por template (como o spotdl) — com uma
// TUI para colar links, ajustar as opções e acompanhar os downloads.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/term"

	"github.com/vinicgobbi/spotdown/internal/config"
	"github.com/vinicgobbi/spotdown/internal/download"
	"github.com/vinicgobbi/spotdown/internal/engine"
	"github.com/vinicgobbi/spotdown/internal/lyrics"
	"github.com/vinicgobbi/spotdown/internal/pathtpl"
	"github.com/vinicgobbi/spotdown/internal/plain"
	"github.com/vinicgobbi/spotdown/internal/spotify"
	"github.com/vinicgobbi/spotdown/internal/tui"
	"github.com/vinicgobbi/spotdown/internal/ytmusic"
)

// version é definido na compilação (-ldflags "-X main.version=...").
var version = "dev"

type options struct {
	cfg        config.Config
	configPath string
	yes        bool
	noTUI      bool
	dryRun     bool
	save       bool
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
	fs.StringVar(&c.YtDlp, "yt-dlp", c.YtDlp, "caminho do yt-dlp")
	fs.StringVar(&c.Cookies, "cookies", c.Cookies, "cookies.txt para o yt-dlp (opcional)")
	fs.BoolVar(&o.yes, "yes", false, "com links na linha de comando, baixar direto sem revisar")
	fs.BoolVar(&o.yes, "y", false, "atalho para --yes")
	fs.BoolVar(&o.dryRun, "dry-run", false, "só listar as faixas e os caminhos que seriam gerados, sem baixar")
	fs.BoolVar(&o.noTUI, "no-tui", false, "saída em texto simples (padrão quando não há terminal)")
	fs.BoolVar(&o.save, "save-config", false, "gravar as opções passadas por flag no arquivo de configuração")
	fs.BoolVar(&o.vars, "vars", false, "listar as variáveis do template e sair")
	fs.BoolVar(&o.version, "version", false, "mostrar a versão e sair")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Uso: spotdown [opções] [LINK...]

Baixa faixas, álbuns e playlists do Spotify (links open.spotify.com ou
spotify:...). Sem links, abre a interface para colá-los.

Exemplos:
  spotdown
  spotdown -o "{artist}/{album}/{track-number:02} - {title}" --format opus LINK
  spotdown --m3u --m3u-name "Playlists/{list-name}.m3u8" -y LINK_DA_PLAYLIST

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
	cfg, created, err := config.Load(o.configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "erro:", err)
		return 2
	}
	o.cfg = cfg
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
		fmt.Fprintln(os.Stderr, "erro:", err)
		return 2
	}
	if o.save {
		if err := config.Save(o.configPath, o.cfg); err != nil {
			fmt.Fprintln(os.Stderr, "erro:", err)
			return 2
		}
		fmt.Fprintln(os.Stderr, "configuração gravada em", o.configPath)
	}
	// Depois de gravar: credenciais do ambiente não vão parar no arquivo.
	o.cfg.ApplyEnv()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var warnings []string
	if created {
		warnings = append(warnings, "configuração criada em "+o.configPath)
	}
	w, err := download.CheckTools(ctx, download.Options{YtDlp: o.cfg.YtDlp}, time.Now())
	if err != nil {
		fmt.Fprintln(os.Stderr, "erro:", err)
		return 2
	}
	if w != "" {
		warnings = append(warnings, w)
	}

	interactive := term.IsTerminal(int(os.Stdout.Fd())) && term.IsTerminal(int(os.Stdin.Fd()))
	if o.noTUI || o.dryRun || !interactive {
		if len(urls) == 0 {
			fmt.Fprintln(os.Stderr, "erro: sem terminal interativo, passe os links na linha de comando")
			return 2
		}
		return runPlain(ctx, o.cfg, urls, warnings, o.dryRun)
	}

	// A TUI trata o Ctrl+C sozinha (pede confirmação durante os downloads).
	stop()
	m := tui.New(tui.Config{
		Cfg: o.cfg, ConfigPath: o.configPath, URLs: urls, Yes: o.yes,
		Warnings: warnings, Version: version,
	})
	final, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	if err != nil {
		fmt.Fprintln(os.Stderr, "erro:", err)
		return 1
	}
	if fin := final.(tui.Model).Result(); fin != nil {
		fmt.Println(plain.Summary(*fin))
		return exitCode(*fin)
	}
	return 0
}

func setUserAgents() {
	ua := "spotdown/" + version + " (+https://github.com/vinicgobbi/spotdown)"
	spotify.UserAgent, ytmusic.UserAgent, lyrics.UserAgent = ua, ua, ua
}

func runPlain(ctx context.Context, cfg config.Config, urls []string, warnings []string, dryRun bool) int {
	p := &plain.Printer{W: os.Stdout, Color: term.IsTerminal(int(os.Stdout.Fd()))}
	for _, w := range warnings {
		p.Handle(engine.WarningEvent{Msg: w})
	}
	sp := spotify.New(spotify.Options{
		ClientID: cfg.SpotifyClientID, ClientSecret: cfg.SpotifyClientSecret,
		CacheDir: config.CacheDir(), AlbumDetails: engine.NeedsAlbumDetails(cfg),
	})
	lists := engine.Resolve(ctx, sp, urls, p.Handle)
	if len(lists) == 0 {
		return 1
	}
	jobs, err := engine.Plan(cfg, lists)
	if err != nil {
		fmt.Fprintln(os.Stderr, "erro:", err)
		return 2
	}
	if dryRun {
		for _, j := range jobs {
			fmt.Println(j.Path)
		}
		return 0
	}
	p.Total = len(jobs)
	fin := engine.NewRunner(cfg).Run(ctx, jobs, lists, p.Handle)
	return exitCode(fin)
}

// exitCode: 0 tudo certo, 1 se alguma faixa falhou ou ficou sem correspondência.
func exitCode(f engine.FinishedEvent) int {
	for _, r := range f.Results {
		if !r.Status.OK() {
			return 1
		}
	}
	return 0
}
