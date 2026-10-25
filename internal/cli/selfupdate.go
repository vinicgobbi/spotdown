package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/vinicgobbi/spotdown/internal/install"
	"github.com/vinicgobbi/spotdown/internal/update"
)

// Quantas vezes a verificação automática pode consultar o GitHub, e quanto
// ela pode atrasar a abertura.
const (
	autoCheckEvery   = 24 * time.Hour
	autoCheckTimeout = 3 * time.Second
	downloadTimeout  = 2 * time.Minute
)

// updateStamp é onde fica registrada a última verificação automática.
func updateStamp() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "spotdown", "update-check")
}

func (e *Env) stamp() string {
	if e.UpdateStamp != "" {
		return e.UpdateStamp
	}
	return updateStamp()
}

func (e *Env) installer() (*install.Installer, error) {
	if e.NewInstaller != nil {
		return e.NewInstaller()
	}
	return install.New()
}

// Installed informa se este binário já está instalado no PATH do usuário.
func (e *Env) Installed() bool {
	in, err := e.installer()
	return err != nil || in.Installed() // sem como saber: não oferece
}

// AutoCheckUpdate consulta o GitHub no máximo uma vez por dia (e só em
// builds de release) e guarda a versão nova em UpdateAvailable. Falhas são
// silenciosas: nunca atrapalham o que o usuário foi fazer.
func AutoCheckUpdate(env *Env) {
	now := time.Now()
	if !update.Updatable(env.Version) || !update.Due(env.stamp(), autoCheckEvery, now) {
		return
	}
	_ = update.Touch(env.stamp(), now)
	ctx, cancel := context.WithTimeout(context.Background(), autoCheckTimeout)
	defer cancel()
	if r, err := env.Updates.Latest(ctx); err == nil && update.Newer(env.Version, r.Version) {
		env.UpdateAvailable = r.Version
	}
}

// Install copia este binário para ~/.local/bin/spotdown e garante o PATH
// (perguntando antes de mexer no arquivo do shell, a não ser com yes).
func Install(env *Env, yes bool) error {
	u := env.UI
	in, err := env.installer()
	if err != nil {
		return err
	}
	dst, already, err := in.Copy()
	if err != nil {
		return fmt.Errorf("instalando em %s: %w", in.DefaultDir(), err)
	}
	if already {
		u.OK("Já é este binário: %s", Tilde(dst))
	} else {
		u.OK("spotdown %s instalado em %s", env.Version, Tilde(dst))
	}
	dir := in.DefaultDir()
	if in.OnPath(dir) {
		u.Println("Pronto: rode `spotdown` em qualquer terminal.")
		return nil
	}
	file, line := in.ShellRC(dir)
	if !yes {
		if !u.Interactive {
			u.Warn("%s não está no PATH. Adicione ao %s:\n    %s", Tilde(dir), Tilde(file), line)
			return nil
		}
		ok, err := u.ConfirmDefault(Tilde(dir)+" não está no PATH. Adicionar ao "+Tilde(file)+"?", line, true)
		if err != nil {
			return err
		}
		if !ok {
			u.Warn("Adicione manualmente ao %s:\n    %s", Tilde(file), line)
			return nil
		}
	}
	added, err := install.AddToShellRC(file, line)
	if err != nil {
		return err
	}
	if added {
		u.OK("Adicionado ao %s.", Tilde(file))
	}
	u.Println("Abra um terminal novo (ou rode `source " + Tilde(file) + "`) para o PATH valer.")
	return nil
}

// updateTarget é o arquivo que a atualização substitui: o instalado, se
// houver, senão o próprio binário em uso.
func (e *Env) updateTarget() (string, error) {
	in, err := e.installer()
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(in.Target()); err == nil {
		return in.Target(), nil
	}
	return in.Self, nil
}

// applyUpdate baixa a release (conferindo o SHA-256) ao lado do alvo e
// troca o arquivo de uma vez. A versão nova vale na próxima execução.
func applyUpdate(ctx context.Context, env *Env, r update.Release) (string, error) {
	target, err := env.updateTarget()
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()
	env.UI.Status("Baixando o spotdown %s…", r.Version)
	tmp, err := env.Updates.Download(ctx, r, update.AssetName("spotdown", runtime.GOOS, runtime.GOARCH), filepath.Dir(target))
	env.UI.Status("")
	if err != nil {
		return "", err
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("substituindo %s: %w", target, err)
	}
	env.UpdateAvailable = ""
	return target, nil
}

// CheckUpdate procura uma versão nova, mostra o que muda e, com apply (ou
// confirmando na TUI), instala.
func CheckUpdate(ctx context.Context, env *Env, apply bool) error {
	u := env.UI
	ctx2, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	u.Status("Consultando o GitHub…")
	r, err := env.Updates.Latest(ctx2)
	u.Status("")
	if err != nil {
		return err
	}
	_ = update.Touch(env.stamp(), time.Now())
	u.KV([2]string{"esta versão", env.Version}, [2]string{"última release", r.Version})
	u.Println()
	if !update.Updatable(env.Version) {
		u.Warn("Esta é uma versão de desenvolvimento (%s): ela não se atualiza sozinha.", env.Version)
		u.Hint("Baixe a release em %s", update.ReleasesURL)
		return nil
	}
	if !update.Newer(env.Version, r.Version) {
		env.UpdateAvailable = ""
		u.OK("Está na versão mais recente.")
		return nil
	}
	env.UpdateAvailable = r.Version
	ctx3, cancel3 := context.WithTimeout(ctx, autoCheckTimeout*2)
	env.Updates.ShowChanges(ctx3, u, env.Version, r.Version, 0)
	cancel3()
	if !apply {
		if !u.Interactive {
			u.Println("Para atualizar: spotdown --update")
			return nil
		}
		ok, err := u.ConfirmDefault("Atualizar agora para "+r.Version+"?", "Troca o binário de uma vez; vale a partir da próxima execução.", true)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
	}
	target, err := applyUpdate(ctx, env, r)
	if err != nil {
		return err
	}
	u.OK("spotdown atualizado para %s em %s. Saia e abra de novo para usar a versão nova.", r.Version, Tilde(target))
	return nil
}
