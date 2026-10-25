// Package update procura uma release mais nova do spotdown no GitHub e
// baixa o binário dela, conferido com o .sha256 publicado ao lado (como no
// deploy-tools). Não substitui nada sozinho: quem chama instala o arquivo.
package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// LatestURL é o endpoint da API do GitHub com a última release.
const LatestURL = "https://api.github.com/repos/vinicgobbi/spotdown/releases/latest"

// Release é uma versão publicada e os arquivos dela.
type Release struct {
	Version string            // "v1.2.3"
	Assets  map[string]string // nome do arquivo -> URL de download
}

// Client fala com o GitHub; os campos têm padrões que funcionam.
type Client struct {
	HTTP *http.Client
	URL  string // LatestURL quando vazio
	// ChangelogURL é um formato com a tag (%s); ChangelogURL quando ele e
	// URL estão vazios (ver Changes).
	ChangelogURL string
}

func (c Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 2 * time.Minute}
}

// Latest devolve a última release.
func (c Client) Latest(ctx context.Context) (Release, error) {
	url := c.URL
	if url == "" {
		url = LatestURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := c.http().Do(req)
	if err != nil {
		return Release{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("GitHub respondeu %s ao consultar a última versão", resp.Status)
	}
	var body struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&body); err != nil {
		return Release{}, fmt.Errorf("resposta inesperada do GitHub: %w", err)
	}
	if _, ok := parse(body.TagName); !ok {
		return Release{}, fmt.Errorf("versão inesperada na última release: %q", body.TagName)
	}
	r := Release{Version: body.TagName, Assets: map[string]string{}}
	for _, a := range body.Assets {
		r.Assets[a.Name] = a.URL
	}
	return r, nil
}

// AssetName é o arquivo da release para goos/goarch, como o workflow de
// release nomeia (spotdown-linux-amd64, spotdown-linux-arm64).
func AssetName(binary, goos, goarch string) string {
	name := binary + "-" + goos + "-" + goarch
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

// Download grava o asset de r num executável novo em dir e devolve o
// caminho. O conteúdo tem que bater com o .sha256 do asset, senão nada fica.
func (c Client) Download(ctx context.Context, r Release, asset, dir string) (string, error) {
	url, ok := r.Assets[asset]
	if !ok {
		return "", fmt.Errorf("a release %s não tem %s", r.Version, asset)
	}
	sumURL, ok := r.Assets[asset+".sha256"]
	if !ok {
		return "", fmt.Errorf("a release %s não tem %s.sha256 para conferir o download", r.Version, asset)
	}
	sumText, err := c.get(ctx, sumURL, 4096)
	if err != nil {
		return "", err
	}
	want := strings.ToLower(strings.Fields(string(sumText) + " ")[0])
	if len(want) != 64 {
		return "", fmt.Errorf("%s.sha256 inválido", asset)
	}

	f, err := os.CreateTemp(dir, "."+asset+".*.download")
	if err != nil {
		return "", err
	}
	path := f.Name()
	ok = false
	defer func() {
		if !ok {
			f.Close()
			os.Remove(path)
		}
	}()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download de %s: %s", asset, resp.Status)
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, 256<<20)); err != nil {
		return "", err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return "", fmt.Errorf("o SHA-256 de %s não confere com o publicado (%s, esperado %s) — download descartado", asset, got[:12], want[:12])
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	if err := os.Chmod(path, 0o755); err != nil {
		return "", err
	}
	ok = true
	return path, nil
}

func (c Client) get(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download de %s: %s", filepath.Base(url), resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

// Compare compara "vX.Y.Z[-qualquer]"; ok=false quando um dos dois não é
// versão (ex.: "dev").
func Compare(a, b string) (cmp int, ok bool) {
	pa, oka := parse(a)
	pb, okb := parse(b)
	if !oka || !okb {
		return 0, false
	}
	for i := range pa {
		if pa[i] != pb[i] {
			if pa[i] < pb[i] {
				return -1, true
			}
			return 1, true
		}
	}
	return 0, true
}

// Updatable informa se o binário pode se atualizar: só builds de release
// ("v1.2.3"), nunca "dev" nem builds do git describe ("v1.2.3-4-gabc").
func Updatable(version string) bool {
	_, ok := parse(version)
	return ok && !strings.ContainsAny(version, "-+")
}

// Newer informa se latest é uma release mais nova que current.
func Newer(current, latest string) bool {
	c, ok := Compare(latest, current)
	return ok && c > 0
}

func parse(v string) ([3]int, bool) {
	var out [3]int
	if !strings.HasPrefix(v, "v") {
		return out, false
	}
	v = v[1:]
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// Due informa se a última verificação automática, registrada em stamp, é
// mais velha que every (ou nunca aconteceu).
func Due(stamp string, every time.Duration, now time.Time) bool {
	fi, err := os.Stat(stamp)
	return err != nil || now.Sub(fi.ModTime()) >= every
}

// Touch registra uma verificação automática em stamp.
func Touch(stamp string, now time.Time) error {
	if err := os.MkdirAll(filepath.Dir(stamp), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(stamp, nil, 0o644); err != nil {
		return err
	}
	return os.Chtimes(stamp, now, now)
}

// ErrNoUpdate: a versão em uso já é a mais recente.
var ErrNoUpdate = errors.New("já está na versão mais recente")
