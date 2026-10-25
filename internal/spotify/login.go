package spotify

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Desde fevereiro de 2026, apps em Development Mode só recebem o conteúdo
// de playlists que o usuário logado criou ou colabora: o token client
// credentials (sem usuário) leva 401. Por isso o login com a conta, pelo
// fluxo Authorization Code + PKCE, que não precisa do Client Secret.

// RedirectURI precisa estar cadastrada, igual, no app do dashboard do Spotify.
const RedirectURI = "http://127.0.0.1:8888/callback"

const loginScopes = "playlist-read-private playlist-read-collaborative"

// Variáveis só para os testes apontarem para um servidor falso.
var (
	authorizeURL = "https://accounts.spotify.com/authorize"
	tokenURL     = "https://accounts.spotify.com/api/token"
)

// UserToken é o login de um usuário, gravado em disco para não pedir de novo.
type UserToken struct {
	ClientID string    `json:"client_id"`
	Access   string    `json:"access_token"`
	Refresh  string    `json:"refresh_token"`
	Expiry   time.Time `json:"expiry"`
	User     string    `json:"user"` // nome de exibição, para mostrar quem está logado
}

// LoadToken lê o token gravado; (nil, nil) se não houver.
func LoadToken(path string) (*UserToken, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var t UserToken
	if err := json.Unmarshal(b, &t); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &t, nil
}

// Save grava o token com permissão 0600 (dá acesso às suas playlists).
func (t *UserToken) Save(path string) error {
	b, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func randomString(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// AuthorizeURL monta o link de autorização (exportado para testes).
func AuthorizeURL(clientID, state, challenge string) string {
	q := url.Values{
		"client_id":             {clientID},
		"response_type":         {"code"},
		"redirect_uri":          {RedirectURI},
		"code_challenge_method": {"S256"},
		"code_challenge":        {challenge},
		"state":                 {state},
		"scope":                 {loginScopes},
	}
	return authorizeURL + "?" + q.Encode()
}

// Login abre o navegador (open) para o usuário autorizar o app e espera o
// retorno em RedirectURI. show recebe o link, para quem não tiver navegador
// abrindo sozinho. Cancela com ctx.
func Login(ctx context.Context, clientID string, open func(string) error, show func(string)) (*UserToken, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:8888")
	if err != nil {
		return nil, fmt.Errorf("não foi possível abrir 127.0.0.1:8888 para receber o login (porta ocupada?): %w", err)
	}
	verifier := randomString(64)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	state := randomString(16)

	type result struct {
		code string
		err  error
	}
	got := make(chan result, 1)
	var once sync.Once
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		var res result
		switch {
		case q.Get("state") != state:
			res.err = errors.New("resposta de login inválida (state não confere)")
		case q.Get("error") != "":
			res.err = fmt.Errorf("o Spotify recusou o login: %s", q.Get("error"))
		case q.Get("code") == "":
			res.err = errors.New("resposta de login sem código")
		default:
			res.code = q.Get("code")
		}
		msg := "Pronto! Pode fechar esta aba e voltar ao spotdown."
		if res.err != nil {
			msg = "Não deu certo: " + res.err.Error()
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, "<!doctype html><meta charset=utf-8><title>spotdown</title><p style=\"font:16px sans-serif;margin:3em\">%s</p>", html.EscapeString(msg))
		once.Do(func() { got <- res })
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	link := AuthorizeURL(clientID, state, challenge)
	show(link)
	if open != nil {
		_ = open(link) // sem navegador, o link mostrado basta
	}

	var res result
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(10 * time.Minute):
		return nil, errors.New("tempo esgotado esperando o login")
	case res = <-got:
	}
	if res.err != nil {
		return nil, res.err
	}

	hc := &http.Client{Timeout: 20 * time.Second}
	tok, err := exchange(ctx, hc, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {res.code},
		"redirect_uri":  {RedirectURI},
		"client_id":     {clientID},
		"code_verifier": {verifier},
	})
	if err != nil {
		return nil, err
	}
	tok.ClientID = clientID
	tok.User, _ = displayName(ctx, hc, tok.Access)
	return tok, nil
}

// exchange troca código ou refresh token por um token de acesso.
func exchange(ctx context.Context, hc *http.Client, form url.Values) (*UserToken, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", UserAgent)
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("login do Spotify: %w", err)
	}
	defer resp.Body.Close()
	var t struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		Error        string `json:"error"`
		Description  string `json:"error_description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&t); err != nil || t.AccessToken == "" {
		if t.Error != "" {
			return nil, fmt.Errorf("login do Spotify: %s (%s)", t.Error, t.Description)
		}
		return nil, fmt.Errorf("login do Spotify: HTTP %d", resp.StatusCode)
	}
	return &UserToken{
		Access: t.AccessToken, Refresh: t.RefreshToken,
		Expiry: time.Now().Add(time.Duration(t.ExpiresIn) * time.Second),
	}, nil
}

func displayName(ctx context.Context, hc *http.Client, access string) (string, error) {
	body, err := get(ctx, hc, newLimiter(0), apiBase+"/me", http.Header{"Authorization": {"Bearer " + access}})
	if err != nil {
		return "", err
	}
	var me struct {
		DisplayName string `json:"display_name"`
		ID          string `json:"id"`
	}
	if err := json.Unmarshal(body, &me); err != nil {
		return "", err
	}
	return firstNonEmpty(me.DisplayName, me.ID), nil
}

// userAuth mantém o token do usuário válido, renovando e regravando quando vence.
type userAuth struct {
	mu   sync.Mutex
	tok  *UserToken
	path string
}

func (u *userAuth) access(ctx context.Context, hc *http.Client) (string, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if time.Until(u.tok.Expiry) > time.Minute {
		return u.tok.Access, nil
	}
	nt, err := exchange(ctx, hc, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {u.tok.Refresh},
		"client_id":     {u.tok.ClientID},
	})
	if err != nil {
		return "", fmt.Errorf("%w — entre com a conta de novo (spotdown --login)", err)
	}
	if nt.Refresh == "" {
		nt.Refresh = u.tok.Refresh // o Spotify nem sempre manda um novo
	}
	nt.ClientID, nt.User = u.tok.ClientID, u.tok.User
	u.tok = nt
	if u.path != "" {
		_ = nt.Save(u.path)
	}
	return nt.Access, nil
}
