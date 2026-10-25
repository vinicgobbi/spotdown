// Package download baixa o áudio do YouTube com o yt-dlp (que usa o ffmpeg
// para converter) e informa o progresso.
package download

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Formats são os formatos de saída aceitos.
var Formats = []string{"mp3", "m4a", "opus", "flac", "ogg"}

// Options configura o download.
type Options struct {
	YtDlp   string // caminho do yt-dlp (padrão: "yt-dlp" no PATH)
	Format  string // um de Formats
	Bitrate string // "auto" (melhor qualidade) ou "128k", "320k"...
	Cookies string // arquivo cookies.txt opcional (vídeos com restrição de idade)
}

// Valid confere formato e bitrate.
func (o Options) Valid() error {
	ok := false
	for _, f := range Formats {
		ok = ok || f == o.Format
	}
	if !ok {
		return fmt.Errorf("formato inválido %q (use %s)", o.Format, strings.Join(Formats, ", "))
	}
	if o.Bitrate != "" && o.Bitrate != "auto" && !bitrateRe.MatchString(o.Bitrate) {
		return fmt.Errorf("bitrate inválido %q (use auto ou algo como 320k)", o.Bitrate)
	}
	return nil
}

var bitrateRe = regexp.MustCompile(`^\d{2,3}[kK]$`)

func (o Options) bin() string {
	if o.YtDlp != "" {
		return o.YtDlp
	}
	return "yt-dlp"
}

// selector escolhe o stream que evita reconverter quando possível: o opus do
// YouTube vira .opus/.ogg só com remux, e o AAC vira .m4a.
func (o Options) selector() string {
	switch o.Format {
	case "opus", "ogg":
		return "bestaudio[acodec=opus]/bestaudio"
	case "m4a":
		return "bestaudio[ext=m4a]/bestaudio"
	}
	return "bestaudio"
}

func (o Options) quality() string {
	if o.Bitrate == "" || o.Bitrate == "auto" {
		return "0"
	}
	return strings.ToUpper(o.Bitrate)
}

const progressPrefix = "SPOTDOWN "

// Fetch baixa o vídeo para dir e devolve o caminho do arquivo convertido.
// progress recebe a fração baixada (0–1).
func Fetch(ctx context.Context, o Options, url, dir string, progress func(float64)) (string, error) {
	args := []string{
		"--no-playlist", "--no-config", "--quiet", "--no-warnings", "--progress", "--newline",
		"-f", o.selector(),
		"-x", "--audio-format", o.Format, "--audio-quality", o.quality(),
		"--progress-template", "download:" + progressPrefix + "%(progress.downloaded_bytes)s %(progress.total_bytes)s %(progress.total_bytes_estimate)s",
		"--print", "after_move:FILE %(filepath)s",
		"-P", dir, "-o", "%(id)s.%(ext)s",
	}
	if o.Cookies != "" {
		args = append(args, "--cookies", o.Cookies)
	}
	args = append(args, "--", url)

	cmd := exec.CommandContext(ctx, o.bin(), args...)
	gracefulCancel(cmd)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("não foi possível executar o yt-dlp: %w", err)
	}
	var file string
	sc := bufio.NewScanner(stdout)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, progressPrefix):
			if progress != nil {
				if f, ok := parseProgress(line[len(progressPrefix):]); ok {
					progress(f)
				}
			}
		case strings.HasPrefix(line, "FILE "):
			file = strings.TrimPrefix(line, "FILE ")
		}
	}
	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("yt-dlp: %s", lastLine(stderr.String(), err))
	}
	if file == "" {
		// yt-dlp antigo sem --print after_move: procura pelo ID.
		matches, _ := filepath.Glob(filepath.Join(dir, "*."+o.Format))
		if len(matches) == 1 {
			file = matches[0]
		}
	}
	if file == "" {
		return "", errors.New("yt-dlp terminou sem gerar o arquivo")
	}
	return file, nil
}

func parseProgress(s string) (float64, bool) {
	f := strings.Fields(s)
	if len(f) < 3 {
		return 0, false
	}
	done, err := strconv.ParseFloat(f[0], 64)
	if err != nil {
		return 0, false
	}
	total, err := strconv.ParseFloat(f[1], 64)
	if err != nil || total <= 0 {
		total, err = strconv.ParseFloat(f[2], 64)
		if err != nil || total <= 0 {
			return 0, false
		}
	}
	return min(1, done/total), true
}

// lastLine devolve a última linha de erro do yt-dlp (a mais útil).
func lastLine(s string, fallback error) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			return strings.TrimPrefix(l, "ERROR: ")
		}
	}
	return fallback.Error()
}

// maxAgeDays é a idade a partir da qual o yt-dlp é considerado velho: o
// YouTube muda com frequência e versões antigas (como as dos repositórios
// das distros) deixam de baixar.
const maxAgeDays = 120

// CheckTools confere yt-dlp e ffmpeg. Devolve um aviso (não fatal) quando o
// yt-dlp parece velho demais, e erro se algum dos dois não existir.
func CheckTools(ctx context.Context, o Options, now time.Time) (warning string, err error) {
	out, err := exec.CommandContext(ctx, o.bin(), "--version").Output()
	if err != nil {
		return "", fmt.Errorf("yt-dlp não encontrado (%s). Instale com \"pipx install yt-dlp\" ou baixe o binário em https://github.com/yt-dlp/yt-dlp/releases", o.bin())
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return "", errors.New("ffmpeg não encontrado no PATH (instale pelo gerenciador de pacotes)")
	}
	ver := strings.TrimSpace(string(out))
	if age, ok := ageDays(ver, now); ok && age > maxAgeDays {
		return fmt.Sprintf("yt-dlp %s tem %d dias; versões antigas costumam falhar no YouTube. Atualize (\"yt-dlp -U\" ou \"pipx upgrade yt-dlp\")", ver, age), nil
	}
	return "", nil
}

// ageDays calcula a idade de uma versão "AAAA.MM.DD[.x]".
func ageDays(ver string, now time.Time) (int, bool) {
	if len(ver) < 10 {
		return 0, false
	}
	t, err := time.Parse("2006.01.02", ver[:10])
	if err != nil {
		return 0, false
	}
	return int(now.Sub(t).Hours() / 24), true
}

// gracefulCancel faz o cancelamento mandar SIGTERM ao grupo de processos
// (yt-dlp e o ffmpeg que ele chama) em vez de SIGKILL só no yt-dlp: o binário
// oficial (PyInstaller) só apaga a pasta que extrai no /tmp se puder terminar
// normalmente. Se não sair em 5 s, o Go mata de vez.
func gracefulCancel(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	}
	cmd.WaitDelay = 5 * time.Second
}

// TempDir cria a pasta temporária de uma execução.
func TempDir() (string, error) { return os.MkdirTemp("", "spotdown-") }
