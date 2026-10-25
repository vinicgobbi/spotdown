package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadCreatesAndRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spotdown", "config.toml")
	cfg, created, err := Load(path)
	if err != nil || !created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("padrões inválidos: %v", err)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
		t.Errorf("permissão %v", st.Mode().Perm())
	}

	cfg.Output = `{artist}/"aspas" \ barra/{title}`
	cfg.M3U = true
	cfg.Threads = 8
	cfg.SpotifyClientID = "id"
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	got, created, err := Load(path)
	if err != nil || created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	if got != cfg {
		t.Errorf("got %+v\nwant %+v", got, cfg)
	}
}

func TestValidate(t *testing.T) {
	bad := []func(*Config){
		func(c *Config) { c.Format = "wav" },
		func(c *Config) { c.Overwrite = "sempre" },
		func(c *Config) { c.Lyrics = "sim" },
		func(c *Config) { c.Restrict = "x" },
		func(c *Config) { c.Threads = 0 },
		func(c *Config) { c.Output = "{artista}" },
		func(c *Config) { c.M3U, c.M3UName = true, "{lista}.m3u" },
	}
	for i, f := range bad {
		c := Default()
		f(&c)
		if c.Validate() == nil {
			t.Errorf("caso %d: esperava erro", i)
		}
	}
}
