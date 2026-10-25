// Package install copia o binário em uso para uma pasta de programas do
// usuário (~/.local/bin, sem sudo) e garante que ela está no PATH, como o
// "deploy-dev install" do deploy-tools.
package install

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// BinName é o nome do executável instalado.
const BinName = "spotdown"

// Installer guarda o que depende do sistema, para dar para testar.
type Installer struct {
	GOOS    string
	Home    string
	Self    string // caminho real do binário em uso
	PathEnv string // $PATH
	Shell   string // $SHELL
}

// New monta o Installer do ambiente atual.
func New() (*Installer, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return &Installer{
		GOOS: runtime.GOOS, Home: home, Self: cleanReal(self),
		PathEnv: os.Getenv("PATH"), Shell: os.Getenv("SHELL"),
	}, nil
}

// DefaultDir é a pasta de programas do usuário, que não exige sudo.
func (in *Installer) DefaultDir() string { return filepath.Join(in.Home, ".local", "bin") }

// Target é onde o binário fica instalado.
func (in *Installer) Target() string { return filepath.Join(in.DefaultDir(), BinName) }

// OnPath informa se dir está no PATH.
func (in *Installer) OnPath(dir string) bool {
	want := filepath.Clean(dir)
	for _, d := range filepath.SplitList(in.PathEnv) {
		d = strings.Replace(d, "~", in.Home, 1)
		if filepath.Clean(d) == want {
			return true
		}
	}
	return false
}

// Installed informa se o binário em uso é o instalado em DefaultDir e se a
// pasta está no PATH — aí não há o que oferecer.
func (in *Installer) Installed() bool {
	return in.Self == cleanReal(in.Target()) && in.OnPath(in.DefaultDir())
}

// Copy instala Self em Target: grava ao lado e renomeia por cima (troca
// atômica, mesmo com uma versão antiga em execução).
func (in *Installer) Copy() (dst string, already bool, err error) {
	dst = in.Target()
	if in.Self == cleanReal(dst) {
		return dst, true, nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", false, err
	}
	tmp := dst + ".new"
	if err := copyFile(in.Self, tmp); err != nil {
		_ = os.Remove(tmp)
		return "", false, err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return "", false, err
	}
	return dst, false, nil
}

func cleanReal(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	return filepath.Clean(p)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chmod(dst, 0o755)
}

// ShellRC devolve o arquivo de inicialização do shell do usuário e a linha
// que põe dir no PATH.
func (in *Installer) ShellRC(dir string) (file, line string) {
	shown := dir
	if rel, err := filepath.Rel(in.Home, dir); err == nil && !strings.HasPrefix(rel, "..") {
		shown = "$HOME/" + filepath.ToSlash(rel)
	}
	switch filepath.Base(in.Shell) {
	case "fish":
		return filepath.Join(in.Home, ".config", "fish", "config.fish"), "fish_add_path " + strings.Replace(shown, "$HOME", "~", 1)
	case "bash":
		rc := ".bashrc"
		if in.GOOS == "darwin" {
			rc = ".bash_profile"
		}
		return filepath.Join(in.Home, rc), `export PATH="` + shown + `:$PATH"`
	default: // zsh: padrão no macOS e comum no Linux
		return filepath.Join(in.Home, ".zshrc"), `export PATH="` + shown + `:$PATH"`
	}
}

// AddToShellRC acrescenta line a file, a não ser que já esteja lá.
func AddToShellRC(file, line string) (bool, error) {
	b, err := os.ReadFile(file)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if strings.Contains(string(b), line) {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return false, err
	}
	f, err := os.OpenFile(file, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return false, err
	}
	defer f.Close()
	prefix := "\n"
	if len(b) == 0 || strings.HasSuffix(string(b), "\n") {
		prefix = ""
	}
	_, err = fmt.Fprintf(f, "%s\n# spotdown\n%s\n", prefix, line)
	return err == nil, err
}
