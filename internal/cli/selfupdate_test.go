package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vinicgobbi/spotdown/internal/install"
	"github.com/vinicgobbi/spotdown/internal/update"
)

// fakeInstall aponta o instalador para uma home temporária com um binário falso.
func fakeInstall(t *testing.T, env *Env) *install.Installer {
	t.Helper()
	home := t.TempDir()
	self := filepath.Join(t.TempDir(), "spotdown-linux-amd64")
	if err := os.WriteFile(self, []byte("v0.1.0"), 0o755); err != nil {
		t.Fatal(err)
	}
	in := &install.Installer{GOOS: "linux", Home: home, Self: self, PathEnv: "/usr/bin", Shell: "/bin/bash"}
	env.NewInstaller = func() (*install.Installer, error) { return in, nil }
	return in
}

func TestInstallAddsToPathAndHidesItem(t *testing.T) {
	env, out := testEnv(t, "y\n") // confirma acrescentar ao .bashrc
	in := fakeInstall(t, env)
	if !hasItem(env, "Instalar no PATH") {
		t.Fatal("item de instalar deveria aparecer antes")
	}
	if err := Install(env, false); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if b, _ := os.ReadFile(in.Target()); string(b) != "v0.1.0" {
		t.Fatalf("binário instalado: %q", b)
	}
	rc, _ := os.ReadFile(filepath.Join(in.Home, ".bashrc"))
	if !strings.Contains(string(rc), `export PATH="$HOME/.local/bin:$PATH"`) {
		t.Fatalf(".bashrc:\n%s", rc)
	}
	// Agora rodando o instalado, com o PATH já valendo: o item some.
	in.Self, in.PathEnv = in.Target(), in.DefaultDir()+":/usr/bin"
	if hasItem(env, "Instalar no PATH") || !hasItem(env, "Procurar atualizações") {
		t.Error("depois de instalado, só a atualização deveria ficar")
	}
}

func hasItem(env *Env, label string) bool {
	for _, s := range sections(env) {
		for _, it := range s.Items {
			if it.Label == label {
				return true
			}
		}
	}
	return false
}

func fakeGitHub(t *testing.T, tag string, bin []byte) update.Client {
	t.Helper()
	asset := update.AssetName("spotdown", runtime.GOOS, runtime.GOARCH)
	sum := sha256.Sum256(bin)
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest":
			fmt.Fprintf(w, `{"tag_name":%q,"assets":[{"name":%q,"browser_download_url":"%s/bin"},{"name":%q,"browser_download_url":"%s/sum"}]}`,
				tag, asset, srv.URL, asset+".sha256", srv.URL)
		case "/bin":
			w.Write(bin)
		case "/sum":
			fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(sum[:]), asset)
		case "/" + tag + "/CHANGELOG.md":
			fmt.Fprintf(w, "## %s (2026-10-25)\n\n### Feat\n\n- **spotify**: web player como fonte padrão\n\n## v0.1.0 (2026-10-24)\n\n### Feat\n\n- antiga\n", tag)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return update.Client{URL: srv.URL + "/latest", ChangelogURL: srv.URL + "/%s/CHANGELOG.md"}
}

func TestUpdateReplacesInstalledBinary(t *testing.T) {
	env, out := testEnv(t, "")
	env.Version = "v0.1.0"
	in := fakeInstall(t, env)
	if _, _, err := in.Copy(); err != nil {
		t.Fatal(err)
	}
	env.Updates = fakeGitHub(t, "v0.4.0", []byte("v0.4.0"))
	if err := CheckUpdate(context.Background(), env, true); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if b, _ := os.ReadFile(in.Target()); string(b) != "v0.4.0" {
		t.Fatalf("binário depois da atualização: %q\n%s", b, out)
	}
	if b, _ := os.ReadFile(in.Self); string(b) != "v0.1.0" {
		t.Error("com instalação, o binário solto não deveria ser tocado")
	}
	s := out.String()
	if !strings.Contains(s, "spotify: web player como fonte padrão") || strings.Contains(s, "antiga") || !strings.Contains(s, "atualizado para v0.4.0") {
		t.Errorf("saída:\n%s", s)
	}
	if env.UpdateAvailable != "" {
		t.Error("UpdateAvailable deveria ser limpo")
	}
}

func TestUpdateUpToDateAndDev(t *testing.T) {
	env, out := testEnv(t, "")
	env.Version = "v0.4.0"
	fakeInstall(t, env)
	env.Updates = fakeGitHub(t, "v0.4.0", []byte("x"))
	if err := CheckUpdate(context.Background(), env, true); err != nil || !strings.Contains(out.String(), "versão mais recente") {
		t.Fatalf("%v\n%s", err, out)
	}
	env2, out2 := testEnv(t, "")
	env2.Version = "dev"
	fakeInstall(t, env2)
	env2.Updates = fakeGitHub(t, "v0.4.0", []byte("x"))
	if err := CheckUpdate(context.Background(), env2, true); err != nil || !strings.Contains(out2.String(), "desenvolvimento") {
		t.Fatalf("%v\n%s", err, out2)
	}
}
