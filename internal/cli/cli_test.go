package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vinicgobbi/spotdown/internal/config"
	"github.com/vinicgobbi/spotdown/internal/ui"
)

// testEnv monta um Env com config em pasta temporária e respostas em
// modo acessível (uma por linha).
func testEnv(t *testing.T, answers string) (*Env, *bytes.Buffer) {
	t.Helper()
	var out bytes.Buffer
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg, _, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.OutputDir = "/musica"
	u := &ui.UI{In: strings.NewReader(answers), Out: &out, Interactive: true, Accessible: true}
	return &Env{UI: u, Cfg: cfg, File: cfg, ConfigPath: path, Version: "test",
		UpdateStamp: filepath.Join(t.TempDir(), "update-check")}, &out
}

func reload(t *testing.T, env *Env) config.Config {
	t.Helper()
	c, _, err := config.Load(env.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestFormatSaves(t *testing.T) {
	env, out := testEnv(t, "3\n2\n") // opus, 320k
	if err := iFormat(env); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if c := reload(t, env); c.Format != "opus" || c.Bitrate != "320k" {
		t.Fatalf("format=%s bitrate=%s", c.Format, c.Bitrate)
	}
	if env.Cfg.Format != "opus" {
		t.Fatal("config efetiva não atualizada")
	}
}

func TestPathSavesAndValidates(t *testing.T) {
	// Pasta, template inválido (repete a pergunta), template válido, restrict ascii.
	env, out := testEnv(t, "/m\n{artista}\n{artist}/{track-number:02} {title}\n2\n")
	if err := iPath(env); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	c := reload(t, env)
	if c.OutputDir != "/m" || c.Output != "{artist}/{track-number:02} {title}" || c.Restrict != "ascii" {
		t.Fatalf("%+v", c)
	}
	if !strings.Contains(out.String(), "variável desconhecida {artista}") {
		t.Fatalf("sem erro de validação:\n%s", out)
	}
	if !strings.Contains(out.String(), "/m/Rick Astley/01 Never Gonna Give You Up.mp3") {
		t.Fatalf("sem prévia do caminho:\n%s", out)
	}
}

func TestM3UOnAndOff(t *testing.T) {
	env, out := testEnv(t, "y\nPlaylists/{list-name}\nn\n")
	if err := iM3U(env); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if c := reload(t, env); !c.M3U || c.M3UName != "Playlists/{list-name}" || c.M3UAbsolute {
		t.Fatalf("%+v", c)
	}
	if !strings.Contains(out.String(), "/musica/Playlists/Minha playlist.m3u8") {
		t.Fatalf("saída:\n%s", out)
	}

	env2, _ := testEnv(t, "n\n")
	if err := iM3U(env2); err != nil {
		t.Fatal(err)
	}
	if reload(t, env2).M3U {
		t.Fatal("M3U deveria estar desligado")
	}
}

func TestEnvCredentialsNotSaved(t *testing.T) {
	env, _ := testEnv(t, "")
	env.Cfg.SpotifyClientID, env.Cfg.SpotifyClientSecret = "do-ambiente", "segredo"
	if err := env.Save(func(c *config.Config) { c.Threads = 2 }); err != nil {
		t.Fatal(err)
	}
	if c := reload(t, env); c.Threads != 2 || c.SpotifyClientID != "" {
		t.Fatalf("%+v", c)
	}
}

func TestEveryItemHasDescription(t *testing.T) {
	env, _ := testEnv(t, "")
	app := App(env)
	for _, s := range app.Sections {
		for _, it := range s.Items {
			if it.Desc == nil || strings.TrimSpace(it.Desc()) == "" || it.Run == nil {
				t.Errorf("%s: sem descrição ou ação", it.Label)
			}
		}
	}
	if d := apiDesc(env); !strings.Contains(d, "web player (padrão)") || !strings.Contains(d, "downtify") {
		t.Errorf("descrição do web player:\n%s", d)
	}
	env.Cfg.SpotifySource = "official"
	if d := apiDesc(env); !strings.Contains(d, RedirectURI) || !strings.Contains(d, "Web API") {
		t.Errorf("descrição da API oficial sem a Redirect URI:\n%s", d)
	}
}

func TestValidateLinks(t *testing.T) {
	if ValidateLinks("") == nil || ValidateLinks("https://example.com/x") == nil {
		t.Error("aceitou entrada inválida")
	}
	ok := "https://open.spotify.com/track/4cOdK2wGLETKBW3PvgPWqT\nspotify:album:5Z9iiGl2FcIfa3BMiv6OIw"
	if err := ValidateLinks(ok); err != nil {
		t.Error(err)
	}
	if n := len(SplitLinks(ok + ", https://open.spotify.com/playlist/37i9dQZF1DXcBWIGoYBM5M")); n != 3 {
		t.Errorf("links=%d", n)
	}
}

func TestSpotifyOptionsBySource(t *testing.T) {
	c := config.Default()
	c.SpotifyClientID, c.SpotifyClientSecret = "id", "secret"
	if o := SpotifyOptions(c); !o.WebPlayer || o.ClientID != "" || o.TokenFile != "" {
		t.Errorf("web player não pode usar as credenciais: %+v", o)
	}
	c.SpotifySource = "official"
	if o := SpotifyOptions(c); o.WebPlayer || o.ClientID != "id" || o.ClientSecret != "secret" || o.TokenFile == "" {
		t.Errorf("API oficial: %+v", o)
	}
}

func TestSwitchSource(t *testing.T) {
	env, out := testEnv(t, "1\n") // primeira opção: trocar de fonte
	if err := iAPI(context.Background(), env); err == nil {
		// Sem Client ID, trocar para a oficial segue para o cadastro do app
		// (que fica sem resposta aqui) — o que importa é a fonte gravada.
		t.Log(out.String())
	}
	if c := reload(t, env); c.SpotifySource != "official" {
		t.Fatalf("source=%q\n%s", c.SpotifySource, out)
	}
	env2, _ := testEnv(t, "1\n")
	env2.Cfg.SpotifySource, env2.File.SpotifySource = "official", "official"
	if err := iAPI(context.Background(), env2); err != nil {
		t.Fatal(err)
	}
	if c := reload(t, env2); c.SpotifySource != "web" {
		t.Fatalf("source=%q", c.SpotifySource)
	}
}
