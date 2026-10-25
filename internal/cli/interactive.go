package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/charmbracelet/huh"

	"github.com/vinicgobbi/spotdown/internal/config"
	"github.com/vinicgobbi/spotdown/internal/engine"
	"github.com/vinicgobbi/spotdown/internal/m3u"
	"github.com/vinicgobbi/spotdown/internal/pathtpl"
	"github.com/vinicgobbi/spotdown/internal/spotify"
	"github.com/vinicgobbi/spotdown/internal/tui"
	"github.com/vinicgobbi/spotdown/internal/ui"
)

// Interactive é o spotdown sem links: a TUI em tela cheia (um menu por
// linhas no modo acessível).
func Interactive(env *Env) error {
	return tui.Run(env.UI, App(env))
}

// RedirectURI é a URL a cadastrar no app do Spotify. O spotdown não a usa (o
// fluxo client credentials não tem redirect), mas o formulário exige uma, e
// o Spotify recusa "localhost".
const RedirectURI = "http://127.0.0.1:8888/callback"

func onOff(b bool) string {
	if b {
		return "sim"
	}
	return "não"
}

// App monta a TUI.
func App(env *Env) tui.App {
	text := func(s string) func() string { return func() string { return s } }
	return tui.App{
		Title: "spotdown " + env.Version,
		Subtitle: func() string {
			c := env.Cfg
			return fmt.Sprintf("%s · %s · M3U %s", Tilde(pathtpl.ExpandPath(c.OutputDir)), c.Format, onOff(c.M3U))
		},
		Notice: func() string {
			switch {
			case env.ToolsErr != nil:
				return env.ToolsErr.Error() + " — veja Sistema › Dependências"
			case env.ToolsWarn != "":
				return env.ToolsWarn
			}
			return ""
		},
		Sections: []tui.Section{
			{Title: "Baixar", Items: []tui.Item{
				{
					Label: "Baixar links",
					Run:   func(ctx context.Context) error { return iDownload(ctx, env, false) },
					Desc: func() string {
						return "Baixa faixas, álbuns e playlists do Spotify: cole um ou mais links (um por linha).\n\n" +
							"Os metadados vêm do Spotify, o áudio do YouTube Music (a versão mais parecida em título, " +
							"artistas e duração) e cada arquivo sai com tags, capa e letra.\n\n" +
							tui.Capture(env.UI, func() { settingsKV(env) })
					},
					CLI: "spotdown [opções] LINK...",
				},
				{
					Label: "Simular caminhos",
					Run:   func(ctx context.Context) error { return iDownload(ctx, env, true) },
					Desc: text("Lê os links e mostra o caminho que cada faixa teria, sem baixar nada. " +
						"Bom para testar um template novo numa playlist de verdade."),
					CLI: "spotdown --dry-run LINK...",
				},
			}},
			{Title: "Opções", Items: []tui.Item{
				{
					Label: "Caminho dos arquivos",
					Run:   func(context.Context) error { return iPath(env) },
					Desc: func() string {
						c := env.Cfg
						return "Onde e com que nome cada faixa é salva, com as mesmas variáveis do spotdl.\n\n" +
							tui.Capture(env.UI, func() {
								env.UI.KV(
									[2]string{"Pasta base", Tilde(pathtpl.ExpandPath(c.OutputDir))},
									[2]string{"Template", c.Output},
									[2]string{"Caracteres", c.Restrict},
									[2]string{"Exemplo", Tilde(previewPath(env, c))},
								)
							}) + "\n\n" + varsText()
					},
					CLI: `spotdown --output-dir DIR -o "{artist}/{album}/{track-number:02} - {title}" --restrict none|ascii|strict LINK`,
				},
				{
					Label: "Formato e qualidade",
					Run:   func(context.Context) error { return iFormat(env) },
					Desc: func() string {
						return fmt.Sprintf("Formato atual: %s, qualidade %s.\n\n", env.Cfg.Format, env.Cfg.Bitrate) +
							"opus e m4a vêm direto do YouTube, sem reconverter (melhor qualidade por tamanho). " +
							"mp3 é o mais compatível. flac não ganha qualidade: o áudio de origem já é com perdas."
					},
					CLI: "spotdown --format mp3|m4a|opus|flac|ogg --bitrate auto|320k LINK",
				},
				{
					Label: "Playlists M3U",
					Run:   func(context.Context) error { return iM3U(env) },
					Desc: func() string {
						c := env.Cfg
						s := "Gera um .m3u8 para cada álbum e cada playlist baixados (faixas soltas não geram), " +
							"na ordem original. Faixas que não baixaram entram como comentário.\n\n"
						if !c.M3U {
							return s + "Desligado."
						}
						return s + tui.Capture(env.UI, func() {
							env.UI.KV(
								[2]string{"Nome", c.M3UName},
								[2]string{"Caminhos", map[bool]string{true: "absolutos", false: "relativos ao .m3u8"}[c.M3UAbsolute]},
								[2]string{"Exemplo", Tilde(previewM3U(env, c))},
							)
						})
					},
					CLI: `spotdown --m3u --m3u-name "{list-name}.m3u8" [--m3u-absolute] LINK`,
				},
				{
					Label: "Letras",
					Run:   func(context.Context) error { return iLyrics(env) },
					Desc: func() string {
						return "Letras do LRCLIB (lrclib.net), sincronizadas quando houver.\n\nAtual: " + lyricsName(env.Cfg.Lyrics) + "."
					},
					CLI: "spotdown --lyrics embed|lrc|both|off LINK",
				},
				{
					Label: "Arquivos existentes",
					Run:   func(context.Context) error { return iOverwrite(env) },
					Desc: func() string {
						return "O que fazer quando o arquivo de destino já existe.\n\nAtual: " + overwriteName(env.Cfg.Overwrite) + "."
					},
					CLI: "spotdown --overwrite skip|metadata|force LINK",
				},
				{
					Label: "Downloads simultâneos",
					Run:   func(context.Context) error { return iThreads(env) },
					Desc: func() string {
						return fmt.Sprintf("Quantas faixas baixar ao mesmo tempo. Atual: %d.\n\n"+
							"Mais que 4–6 raramente acelera e aumenta a chance de o YouTube limitar.", env.Cfg.Threads)
					},
					CLI: "spotdown --threads N LINK",
				},
				{
					Label: "API do Spotify",
					Run:   func(ctx context.Context) error { return iAPI(ctx, env) },
					Desc:  func() string { return apiDesc(env) },
					CLI:   "SPOTIFY_CLIENT_ID=... SPOTIFY_CLIENT_SECRET=... spotdown LINK",
				},
			}},
			{Title: "Sistema", Items: []tui.Item{
				{
					Label: "Dependências",
					Run: func(ctx context.Context) error {
						env.CheckTools(ctx)
						depsReport(env)
						return nil
					},
					Desc: func() string {
						return "Confere o yt-dlp e o ffmpeg (enter checa de novo).\n\n" + tui.Capture(env.UI, func() { depsReport(env) })
					},
					CLI: "yt-dlp --version; ffmpeg -version",
				},
				{
					Label: "Limpar cache",
					Run:   func(context.Context) error { return iClearCache(env) },
					Desc: func() string {
						return "Os metadados de faixas e álbuns ficam em cache por 30 dias, para não consultar " +
							"o Spotify de novo. Apagar é seguro: só deixa a próxima leitura mais lenta.\n\n" +
							tui.Capture(env.UI, func() {
								env.UI.KV([2]string{"Pasta", Tilde(config.CacheDir())}, [2]string{"Tamanho", cacheSize()})
							})
					},
					CLI: "rm -r " + config.CacheDir(),
				},
				{
					Label: "Arquivo de configuração",
					Run: func(context.Context) error {
						b, err := os.ReadFile(env.ConfigPath)
						if err != nil {
							return err
						}
						env.UI.Hint("%s", env.ConfigPath)
						env.UI.Println()
						env.UI.Println(strings.TrimRight(string(b), "\n"))
						return nil
					},
					Desc: text("As opções desta tela ficam em " + env.ConfigPath + " (enter mostra o conteúdo).\n\n" +
						"Flags da linha de comando valem só para aquela execução; --save-config grava as flags como padrão."),
					CLI: "spotdown --save-config [opções]",
				},
			}},
		},
	}
}

// settingsKV resume as opções que valem para o próximo download.
func settingsKV(env *Env) {
	c := env.Cfg
	m3uText := "não"
	if c.M3U {
		m3uText = c.M3UName
	}
	api := "não configurada (playlists até 100 faixas)"
	if c.SpotifyClientID != "" && c.SpotifyClientSecret != "" {
		api = "configurada"
	}
	env.UI.KV(
		[2]string{"Destino", Tilde(filepath.Join(pathtpl.ExpandPath(c.OutputDir), c.Output))},
		[2]string{"Formato", c.Format + " · " + c.Bitrate},
		[2]string{"M3U", m3uText},
		[2]string{"Letras", c.Lyrics},
		[2]string{"Existentes", c.Overwrite},
		[2]string{"API Spotify", api},
	)
}

func iDownload(ctx context.Context, env *Env, dry bool) error {
	var links string
	if err := env.UI.Run(huh.NewGroup(huh.NewText().
		Title("Links do Spotify").
		Description("Faixas, álbuns ou playlists, um por linha (dá para colar vários de uma vez; ctrl+j nova linha).").
		Lines(5).Validate(ValidateLinks).Value(&links))); err != nil {
		return err
	}
	fin, err := Download(ctx, env, SplitLinks(links), DownloadOptions{DryRun: dry, Choose: true})
	if err != nil {
		return err
	}
	if Failed(fin) {
		return errors.New("algumas faixas não foram baixadas (veja acima)")
	}
	return nil
}

func previewPath(env *Env, c config.Config) string {
	p, err := engine.PathFor(c, env.Sample())
	if err != nil {
		return "✗ " + err.Error()
	}
	return p
}

func previewM3U(env *Env, c config.Config) string {
	name := c.M3UName
	if name == "" {
		name = m3u.DefaultName
	}
	p, err := pathtpl.RenderList(name, env.Sample(), "m3u8", pathtpl.Options{BaseDir: c.OutputDir, Restrict: pathtpl.Restrict(c.Restrict)})
	if err != nil {
		return "✗ " + err.Error()
	}
	if !strings.HasSuffix(p, ".m3u8") && !strings.HasSuffix(p, ".m3u") {
		p += ".m3u8"
	}
	return p
}

func varsText() string {
	names := make([]string, len(pathtpl.Vars))
	for i, v := range pathtpl.Vars {
		names[i] = "{" + v.Name + "}"
	}
	return "Variáveis: " + strings.Join(names, " ") + "\n\n" +
		"Números aceitam zeros à esquerda: {track-number:02}. Terminar em / acrescenta \"{artists} - {title}\"; " +
		"sem {output-ext}, a extensão é acrescentada. spotdown --vars descreve cada uma."
}

func iPath(env *Env) error {
	c := env.Cfg
	dir, tpl, restrict := c.OutputDir, c.Output, c.Restrict
	preview := func() string {
		cc := env.Cfg
		cc.OutputDir, cc.Output, cc.Restrict = dir, tpl, restrict
		return "Exemplo: " + Tilde(previewPath(env, cc))
	}
	err := env.UI.Run(huh.NewGroup(
		huh.NewInput().Title("Pasta base").Description("Onde ficam os templates relativos (~ e $VARIAVEIS valem).").
			Value(&dir).Validate(func(s string) error {
			if strings.TrimSpace(s) == "" {
				return errors.New("informe uma pasta")
			}
			return nil
		}),
		huh.NewInput().Title("Template").DescriptionFunc(preview, []any{&dir, &tpl}).
			Value(&tpl).Validate(func(s string) error { return pathtpl.Validate(pathtpl.Normalize(s)) }),
		huh.NewSelect[string]().Title("Caracteres nos nomes").Options(
			huh.NewOption("none — só remove os inválidos (/ : * ? ...)", "none"),
			huh.NewOption("ascii — também tira os acentos", "ascii"),
			huh.NewOption("strict — ASCII, sem espaços, só A-Z 0-9 . _ -", "strict"),
		).Value(&restrict),
	))
	if err != nil {
		return err
	}
	if err := env.Save(func(c *config.Config) { c.OutputDir, c.Output, c.Restrict = dir, tpl, restrict }); err != nil {
		return err
	}
	env.UI.OK("Caminho gravado.")
	env.UI.Hint("%s", Tilde(previewPath(env, env.Cfg)))
	return nil
}

func iFormat(env *Env) error {
	format, bitrate := env.Cfg.Format, env.Cfg.Bitrate
	err := env.UI.Run(huh.NewGroup(
		huh.NewSelect[string]().Title("Formato").Options(
			huh.NewOption("mp3 — o mais compatível (reconverte)", "mp3"),
			huh.NewOption("m4a — AAC original do YouTube", "m4a"),
			huh.NewOption("opus — Opus original do YouTube (menor arquivo)", "opus"),
			huh.NewOption("ogg — Opus em .ogg", "ogg"),
			huh.NewOption("flac — sem perdas a partir da origem (arquivo grande)", "flac"),
		).Value(&format),
		huh.NewSelect[string]().Title("Qualidade").Options(
			huh.NewOption("auto — a melhor disponível", "auto"),
			huh.NewOption("320k", "320k"), huh.NewOption("256k", "256k"),
			huh.NewOption("192k", "192k"), huh.NewOption("128k", "128k"),
		).Value(&bitrate),
	))
	if err != nil {
		return err
	}
	if err := env.Save(func(c *config.Config) { c.Format, c.Bitrate = format, bitrate }); err != nil {
		return err
	}
	env.UI.OK("Formato: %s, qualidade %s.", format, bitrate)
	return nil
}

func iM3U(env *Env) error {
	on, name, abs := env.Cfg.M3U, env.Cfg.M3UName, env.Cfg.M3UAbsolute
	err := env.UI.Run(
		huh.NewGroup(huh.NewConfirm().Title("Gerar M3U para álbuns e playlists?").
			Affirmative("Sim").Negative("Não").Value(&on)),
		huh.NewGroup(
			huh.NewInput().Title("Nome do arquivo").
				DescriptionFunc(func() string {
					c := env.Cfg
					c.M3UName = name
					return "Relativo à pasta base. Exemplo: " + Tilde(previewM3U(env, c))
				}, &name).
				Value(&name).Validate(pathtpl.Validate),
			huh.NewConfirm().Title("Caminhos no M3U").
				Affirmative("Absolutos").Negative("Relativos ao .m3u8").Value(&abs),
		).WithHideFunc(func() bool { return !on }),
	)
	if err != nil {
		return err
	}
	if err := env.Save(func(c *config.Config) { c.M3U, c.M3UName, c.M3UAbsolute = on, name, abs }); err != nil {
		return err
	}
	if on {
		env.UI.OK("M3U ligado: %s", Tilde(previewM3U(env, env.Cfg)))
	} else {
		env.UI.OK("M3U desligado.")
	}
	return nil
}

func lyricsName(mode string) string {
	switch mode {
	case "embed":
		return "embutir no arquivo"
	case "lrc":
		return "arquivo .lrc ao lado"
	case "both":
		return "embutir e .lrc"
	}
	return "desligadas"
}

func iLyrics(env *Env) error {
	mode := env.Cfg.Lyrics
	var opts []huh.Option[string]
	for _, m := range config.LyricsMode {
		opts = append(opts, huh.NewOption(lyricsName(m), m))
	}
	if err := env.UI.Select("Letras", &mode, opts...); err != nil {
		return err
	}
	if err := env.Save(func(c *config.Config) { c.Lyrics = mode }); err != nil {
		return err
	}
	env.UI.OK("Letras: %s.", lyricsName(mode))
	return nil
}

func overwriteName(mode string) string {
	switch mode {
	case "skip":
		return "pular (não baixa de novo)"
	case "metadata":
		return "só regravar as tags"
	}
	return "baixar de novo"
}

func iOverwrite(env *Env) error {
	mode := env.Cfg.Overwrite
	var opts []huh.Option[string]
	for _, m := range config.Overwrites {
		opts = append(opts, huh.NewOption(overwriteName(m), m))
	}
	if err := env.UI.Select("Se o arquivo já existir", &mode, opts...); err != nil {
		return err
	}
	if err := env.Save(func(c *config.Config) { c.Overwrite = mode }); err != nil {
		return err
	}
	env.UI.OK("Arquivos existentes: %s.", overwriteName(mode))
	return nil
}

func iThreads(env *Env) error {
	n := strconv.Itoa(env.Cfg.Threads)
	var opts []huh.Option[string]
	for i := 1; i <= 16; i++ {
		opts = append(opts, huh.NewOption(strconv.Itoa(i), strconv.Itoa(i)))
	}
	if err := env.UI.Select("Downloads simultâneos", &n, opts...); err != nil {
		return err
	}
	v, _ := strconv.Atoi(n)
	if err := env.Save(func(c *config.Config) { c.Threads = v }); err != nil {
		return err
	}
	env.UI.OK("Downloads simultâneos: %d.", v)
	return nil
}

func apiConfigured(c config.Config) bool {
	return c.SpotifyClientID != "" && c.SpotifyClientSecret != ""
}

func apiDesc(env *Env) string {
	var b strings.Builder
	if apiConfigured(env.Cfg) {
		id := env.Cfg.SpotifyClientID
		fmt.Fprintf(&b, "Configurada (Client ID %s…).", id[:min(6, len(id))])
		if os.Getenv("SPOTIFY_CLIENT_ID") != "" {
			b.WriteString(" As credenciais vêm das variáveis de ambiente, que têm prioridade sobre o arquivo.")
		}
	} else {
		b.WriteString("Não configurada: sem login, o Spotify só libera as primeiras 100 faixas de uma playlist.")
	}
	b.WriteString("\n\nCom as credenciais de um app (grátis, sem login de usuário) vêm a playlist inteira, o ISRC e o gênero. Para criar:\n\n")
	b.WriteString(tui.Capture(env.UI, func() { apiSteps(env.UI) }))
	return b.String()
}

func apiSteps(u *ui.UI) {
	steps := []string{
		"Abra https://developer.spotify.com/dashboard e clique em Create app.",
		"Nome e descrição: qualquer texto.",
		"Redirect URIs: " + RedirectURI + " (clique em Add). O spotdown não usa, mas o formulário exige; \"localhost\" é recusado.",
		"Em \"Which API/SDKs\", marque Web API, aceite os termos e salve.",
		"Em Settings, copie o Client ID e, em View client secret, o Client secret.",
	}
	for i, s := range steps {
		u.Printf("  %d. %s\n", i+1, s)
	}
}

func iAPI(ctx context.Context, env *Env) error {
	u := env.UI
	if apiConfigured(env.File) {
		what := "change"
		if err := u.Select("API do Spotify", &what,
			huh.NewOption("Trocar as credenciais", "change"),
			huh.NewOption("Remover as credenciais", "remove"),
		); err != nil {
			return err
		}
		if what == "remove" {
			if err := env.Save(func(c *config.Config) { c.SpotifyClientID, c.SpotifyClientSecret = "", "" }); err != nil {
				return err
			}
			u.OK("Credenciais removidas.")
			return nil
		}
	} else {
		u.Println("Crie o app antes de continuar:")
		apiSteps(u)
		u.Println()
	}
	var id, secret string
	nonEmpty := func(s string) error {
		if strings.TrimSpace(s) == "" {
			return errors.New("obrigatório")
		}
		return nil
	}
	if err := u.Run(huh.NewGroup(
		huh.NewInput().Title("Client ID").Value(&id).Validate(nonEmpty),
		huh.NewInput().Title("Client secret").EchoMode(huh.EchoModePassword).Value(&secret).Validate(nonEmpty),
	)); err != nil {
		return err
	}
	id, secret = strings.TrimSpace(id), strings.TrimSpace(secret)
	u.Status("Testando as credenciais…")
	err := spotify.CheckCredentials(ctx, id, secret)
	u.Status("")
	if err != nil {
		return err
	}
	if err := env.Save(func(c *config.Config) { c.SpotifyClientID, c.SpotifyClientSecret = id, secret }); err != nil {
		return err
	}
	u.OK("Credenciais válidas e gravadas em %s.", env.ConfigPath)
	return nil
}

// depsReport mostra o estado do yt-dlp e do ffmpeg (da última checagem).
func depsReport(env *Env) {
	u := env.UI
	yt := env.Cfg.YtDlp
	if yt == "" {
		yt = "yt-dlp"
	}
	ver := env.ToolsVersion
	switch {
	case env.ToolsErr != nil:
		u.Warn("%s", env.ToolsErr)
	case env.ToolsWarn != "":
		u.Warn("%s", env.ToolsWarn)
	default:
		u.OK("yt-dlp %s e ffmpeg encontrados.", ver)
	}
	ff, _ := lookPath("ffmpeg")
	u.Println()
	u.KV(
		[2]string{"yt-dlp", orDash(firstPath(yt)) + "  " + u.Faint(ver)},
		[2]string{"ffmpeg", orDash(ff)},
	)
	u.Println()
	u.Hint("O YouTube muda com frequência: mantenha o yt-dlp atualizado (yt-dlp -U, ou pipx upgrade yt-dlp).")
	u.Hint("O do apt costuma estar velho demais; prefira o binário de https://github.com/yt-dlp/yt-dlp/releases.")
}

var lookPath = exec.LookPath

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func firstPath(bin string) string {
	p, _ := lookPath(bin)
	return p
}

func cacheSize() string {
	var total int64
	_ = filepath.WalkDir(config.CacheDir(), func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if info, err := d.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	switch {
	case total >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(total)/(1<<20))
	case total >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(total)/(1<<10))
	}
	return fmt.Sprintf("%d bytes", total)
}

func iClearCache(env *Env) error {
	ok, err := env.UI.Confirm("Apagar o cache de metadados?", config.CacheDir()+" ("+cacheSize()+")")
	if err != nil || !ok {
		if err == nil {
			err = ui.ErrAborted
		}
		return err
	}
	if err := os.RemoveAll(config.CacheDir()); err != nil {
		return err
	}
	env.UI.OK("Cache apagado.")
	return nil
}
