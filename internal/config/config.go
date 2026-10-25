// Package config lê e grava ~/.config/spotdown/config.toml. A precedência é
// padrões < arquivo < variáveis de ambiente < flags (aplicadas pelo main).
package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"

	"github.com/BurntSushi/toml"

	"github.com/vinicgobbi/spotdown/internal/download"
	"github.com/vinicgobbi/spotdown/internal/pathtpl"
)

// Valores aceitos nos campos de escolha.
var (
	Overwrites = []string{"skip", "metadata", "force"}
	LyricsMode = []string{"embed", "lrc", "both", "off"}
	Restricts  = []string{string(pathtpl.RestrictNone), string(pathtpl.RestrictASCII), string(pathtpl.RestrictStrict)}
)

// DefaultOutput é o template padrão: organizado por artista e álbum.
const DefaultOutput = "{album-artist}/{album}/{track-number:02} - {title}.{output-ext}"

// Config são as opções persistentes.
type Config struct {
	OutputDir   string `toml:"output_dir"`
	Output      string `toml:"output"`
	Format      string `toml:"format"`
	Bitrate     string `toml:"bitrate"`
	Restrict    string `toml:"restrict"`
	Overwrite   string `toml:"overwrite"`
	M3U         bool   `toml:"m3u"`
	M3UName     string `toml:"m3u_name"`
	M3UAbsolute bool   `toml:"m3u_absolute"`
	Lyrics      string `toml:"lyrics"`
	Threads     int    `toml:"threads"`
	YtDlp       string `toml:"yt_dlp"`
	Cookies     string `toml:"cookies"`

	SpotifyClientID     string `toml:"spotify_client_id"`
	SpotifyClientSecret string `toml:"spotify_client_secret"`
}

// Default devolve a configuração padrão.
func Default() Config {
	return Config{
		OutputDir: defaultMusicDir(),
		Output:    DefaultOutput,
		Format:    "mp3",
		Bitrate:   "auto",
		Restrict:  string(pathtpl.RestrictNone),
		Overwrite: "skip",
		M3UName:   "{list-name}.m3u8",
		Lyrics:    "embed",
		Threads:   4,
	}
}

// defaultMusicDir usa a pasta de música do XDG (xdg-user-dir MUSIC), com
// ~/Music como reserva.
func defaultMusicDir() string {
	if out, err := exec.Command("xdg-user-dir", "MUSIC").Output(); err == nil {
		if d := strings.TrimSpace(string(out)); d != "" {
			if home, _ := os.UserHomeDir(); d != home {
				return d
			}
		}
	}
	return "~/Music"
}

// Path devolve o caminho padrão do arquivo de configuração.
func Path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "spotdown", "config.toml"), nil
}

// CacheDir devolve a pasta do cache de metadados.
func CacheDir() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "spotdown", "meta")
}

// Load lê path sobre os padrões. Se o arquivo não existir, grava um com os
// padrões comentados e devolve created=true.
func Load(path string) (cfg Config, created bool, err error) {
	cfg = Default()
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return cfg, false, fmt.Errorf("%s: %w", path, err)
		}
		if err := Save(path, cfg); err != nil {
			return cfg, false, err
		}
		created = true
	}
	return cfg, created, nil
}

// ApplyEnv sobrepõe as credenciais do Spotify com as variáveis de ambiente.
func (c *Config) ApplyEnv() {
	if v := os.Getenv("SPOTIFY_CLIENT_ID"); v != "" {
		c.SpotifyClientID = v
	}
	if v := os.Getenv("SPOTIFY_CLIENT_SECRET"); v != "" {
		c.SpotifyClientSecret = v
	}
}

func oneOf(field, v string, opts []string) error {
	for _, o := range opts {
		if v == o {
			return nil
		}
	}
	return fmt.Errorf("%s inválido %q (use %s)", field, v, strings.Join(opts, ", "))
}

// Validate confere todos os campos.
func (c Config) Validate() error {
	if err := (download.Options{Format: c.Format, Bitrate: c.Bitrate}).Valid(); err != nil {
		return err
	}
	if err := oneOf("restrict", c.Restrict, Restricts); err != nil {
		return err
	}
	if err := oneOf("overwrite", c.Overwrite, Overwrites); err != nil {
		return err
	}
	if err := oneOf("lyrics", c.Lyrics, LyricsMode); err != nil {
		return err
	}
	if c.Threads < 1 || c.Threads > 16 {
		return fmt.Errorf("threads deve estar entre 1 e 16 (recebido %d)", c.Threads)
	}
	if err := pathtpl.Validate(pathtpl.Normalize(c.Output)); err != nil {
		return fmt.Errorf("output: %w", err)
	}
	if c.M3U {
		if err := pathtpl.Validate(c.M3UName); err != nil {
			return fmt.Errorf("m3u_name: %w", err)
		}
	}
	return nil
}

// Save grava a configuração com comentários explicando cada campo.
func Save(path string, c Config) error {
	var b bytes.Buffer
	if err := fileTmpl.Execute(&b, c); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// 0600: o arquivo pode guardar o Client Secret do Spotify.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b.Bytes(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// quote gera uma string TOML básica (aspas duplas com escapes).
func quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, "\\u%04X", r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

var fileTmpl = template.Must(template.New("config").Funcs(template.FuncMap{
	"q":    quote,
	"bool": strconv.FormatBool,
	"vars": func() string {
		var b strings.Builder
		for _, v := range pathtpl.Vars {
			fmt.Fprintf(&b, "#   {%s} — %s\n", v.Name, v.Desc)
		}
		return b.String()
	},
}).Parse(`# Configuração do spotdown. As flags da linha de comando têm prioridade.

# Pasta base para templates relativos.
output_dir = {{q .OutputDir}}

# Template do caminho de cada faixa (como o --output do spotdl).
# Terminando em "/", acrescenta "{artists} - {title}.{output-ext}";
# sem {output-ext}, a extensão é acrescentada. Números aceitam zeros à
# esquerda: {track-number:02}. Variáveis:
{{vars}}output = {{q .Output}}

# Formato: mp3, m4a, opus, flac ou ogg. opus/m4a evitam reconversão.
format = {{q .Format}}

# Qualidade: "auto" (a melhor) ou um bitrate como "320k".
bitrate = {{q .Bitrate}}

# Caracteres nos nomes: "none" (só remove os inválidos), "ascii" (sem
# acentos) ou "strict" (ASCII, sem espaços).
restrict = {{q .Restrict}}

# Arquivo já existe: "skip" (pula), "metadata" (só regrava as tags) ou
# "force" (baixa de novo).
overwrite = {{q .Overwrite}}

# Gerar um .m3u8 para cada álbum/playlist (faixas avulsas não geram).
m3u = {{bool .M3U}}
# Nome do .m3u8 (mesmas variáveis; relativo a output_dir).
m3u_name = {{q .M3UName}}
# Caminhos absolutos no .m3u8 (padrão: relativos à pasta do .m3u8).
m3u_absolute = {{bool .M3UAbsolute}}

# Letras (LRCLIB): "embed" (no arquivo), "lrc" (arquivo .lrc ao lado),
# "both" ou "off".
lyrics = {{q .Lyrics}}

# Downloads simultâneos (1 a 16).
threads = {{.Threads}}

# Caminho do yt-dlp (vazio = o do PATH) e um cookies.txt opcional.
yt_dlp = {{q .YtDlp}}
cookies = {{q .Cookies}}

# Opcional: credenciais de um app em https://developer.spotify.com/dashboard
# (fluxo client credentials, sem login). Liberam playlists com mais de 100
# faixas e trazem ISRC. Também lidas de SPOTIFY_CLIENT_ID/SPOTIFY_CLIENT_SECRET.
# Ao criar o app: Redirect URI "http://127.0.0.1:8888/callback" (obrigatória
# no formulário, mas nunca usada; "localhost" é recusado) e API "Web API".
spotify_client_id = {{q .SpotifyClientID}}
spotify_client_secret = {{q .SpotifyClientSecret}}
`))
