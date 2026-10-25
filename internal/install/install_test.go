package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testInstaller(t *testing.T) *Installer {
	t.Helper()
	home := t.TempDir()
	self := filepath.Join(t.TempDir(), "spotdown-linux-amd64")
	if err := os.WriteFile(self, []byte("binario v1"), 0o755); err != nil {
		t.Fatal(err)
	}
	return &Installer{GOOS: "linux", Home: home, Self: cleanReal(self), PathEnv: "/usr/bin:/bin", Shell: "/usr/bin/zsh"}
}

func TestCopyAndInstalled(t *testing.T) {
	in := testInstaller(t)
	if in.Installed() {
		t.Fatal("não deveria estar instalado")
	}
	dst, already, err := in.Copy()
	if err != nil || already || dst != filepath.Join(in.Home, ".local", "bin", "spotdown") {
		t.Fatalf("dst=%s already=%v err=%v", dst, already, err)
	}
	b, _ := os.ReadFile(dst)
	st, _ := os.Stat(dst)
	if string(b) != "binario v1" || st.Mode().Perm() != 0o755 {
		t.Fatalf("conteúdo %q modo %v", b, st.Mode())
	}

	// Rodando o instalado, com a pasta no PATH ("~" também vale).
	in.Self, in.PathEnv = cleanReal(dst), "~/.local/bin:/usr/bin"
	if !in.Installed() {
		t.Error("deveria constar como instalado")
	}
	if _, already, _ := in.Copy(); !already {
		t.Error("copiar o próprio instalado deveria ser no-op")
	}
	// Instalado, mas fora do PATH: ainda oferece (para acertar o PATH).
	in.PathEnv = "/usr/bin"
	if in.Installed() {
		t.Error("fora do PATH não conta como instalado")
	}
}

func TestShellRC(t *testing.T) {
	in := testInstaller(t)
	for shell, want := range map[string]string{
		"/usr/bin/zsh":  `.zshrc|export PATH="$HOME/.local/bin:$PATH"`,
		"/bin/bash":     `.bashrc|export PATH="$HOME/.local/bin:$PATH"`,
		"/usr/bin/fish": `config.fish|fish_add_path ~/.local/bin`,
	} {
		in.Shell = shell
		file, line := in.ShellRC(in.DefaultDir())
		parts := strings.Split(want, "|")
		if filepath.Base(file) != parts[0] || line != parts[1] {
			t.Errorf("%s: %s %q", shell, file, line)
		}
	}
}

func TestAddToShellRCOnce(t *testing.T) {
	file := filepath.Join(t.TempDir(), ".zshrc")
	_ = os.WriteFile(file, []byte("alias x=y"), 0o644)
	line := `export PATH="$HOME/.local/bin:$PATH"`
	for i, want := range []bool{true, false} {
		added, err := AddToShellRC(file, line)
		if err != nil || added != want {
			t.Fatalf("vez %d: added=%v err=%v", i, added, err)
		}
	}
	b, _ := os.ReadFile(file)
	if strings.Count(string(b), line) != 1 || !strings.HasPrefix(string(b), "alias x=y\n") {
		t.Errorf("rc:\n%s", b)
	}
}
