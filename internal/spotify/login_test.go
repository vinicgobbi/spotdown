package spotify

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeSpotify responde como accounts.spotify.com e api.spotify.com.
func fakeSpotify(t *testing.T, playlist http.HandlerFunc) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		switch r.Form.Get("grant_type") {
		case "authorization_code":
			if r.Form.Get("code") != "codigo" || r.Form.Get("code_verifier") == "" || r.Form.Get("redirect_uri") != RedirectURI {
				w.WriteHeader(400)
				fmt.Fprint(w, `{"error":"invalid_grant","error_description":"x"}`)
				return
			}
			fmt.Fprint(w, `{"access_token":"user-1","refresh_token":"r1","expires_in":3600}`)
		case "refresh_token":
			fmt.Fprint(w, `{"access_token":"user-2","expires_in":3600}`)
		case "client_credentials":
			fmt.Fprint(w, `{"access_token":"app","expires_in":3600}`)
		}
	})
	mux.HandleFunc("/v1/me", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"display_name":"Vini","id":"v"}`)
	})
	if playlist == nil {
		playlist = http.NotFound
	}
	mux.HandleFunc("/v1/playlists/", playlist)
	srv := httptest.NewServer(mux)
	oldBase, oldTok, oldApp, oldAuth := apiBase, tokenURL, appTokenURL, authorizeURL
	apiBase, tokenURL, appTokenURL, authorizeURL = srv.URL+"/v1", srv.URL+"/token", srv.URL+"/token", srv.URL+"/authorize"
	t.Cleanup(func() {
		srv.Close()
		apiBase, tokenURL, appTokenURL, authorizeURL = oldBase, oldTok, oldApp, oldAuth
	})
	return srv
}

func TestLoginFlow(t *testing.T) {
	fakeSpotify(t, nil)
	// "Navegador" falso: lê o state do link e chama o callback local.
	open := func(link string) error {
		u, _ := url.Parse(link)
		q := u.Query()
		if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" || q.Get("redirect_uri") != RedirectURI {
			return fmt.Errorf("link de autorização inválido: %s", link)
		}
		go func() {
			resp, err := http.Get(RedirectURI + "?code=codigo&state=" + url.QueryEscape(q.Get("state")))
			if err == nil {
				resp.Body.Close()
			}
		}()
		return nil
	}
	var shown string
	tok, err := Login(context.Background(), "meu-app", open, func(s string) { shown = s })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(shown, "client_id=meu-app") {
		t.Errorf("link mostrado: %s", shown)
	}
	if tok.Access != "user-1" || tok.Refresh != "r1" || tok.User != "Vini" || tok.ClientID != "meu-app" {
		t.Errorf("%+v", tok)
	}
}

func TestLoginRejectsWrongState(t *testing.T) {
	fakeSpotify(t, nil)
	open := func(string) error {
		go func() {
			resp, err := http.Get(RedirectURI + "?code=codigo&state=outro")
			if err == nil {
				resp.Body.Close()
			}
		}()
		return nil
	}
	if _, err := Login(context.Background(), "x", open, func(string) {}); err == nil || !strings.Contains(err.Error(), "state") {
		t.Errorf("err = %v", err)
	}
}

func TestUserTokenRefreshAndSave(t *testing.T) {
	fakeSpotify(t, nil)
	path := filepath.Join(t.TempDir(), "tok.json")
	u := &userAuth{path: path, tok: &UserToken{ClientID: "c", Access: "velho", Refresh: "r1", Expiry: time.Now().Add(-time.Hour), User: "Vini"}}
	got, err := u.access(context.Background(), http.DefaultClient)
	if err != nil || got != "user-2" {
		t.Fatalf("got %q err %v", got, err)
	}
	saved, err := LoadToken(path)
	if err != nil || saved.Access != "user-2" || saved.Refresh != "r1" || saved.User != "Vini" {
		t.Fatalf("saved %+v err %v", saved, err)
	}
}

func items(ids ...string) string {
	var rows []string
	for _, id := range ids {
		rows = append(rows, fmt.Sprintf(`{"item":{"id":%q,"type":"track","name":"F %s","artists":[{"name":"A"}],"album":{"id":"al","name":"Al"}},"track":true}`, id, id))
	}
	return strings.Join(rows, ",")
}

func TestPlaylistViaAPI(t *testing.T) {
	var srv *httptest.Server
	srv = fakeSpotify(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer user-1" {
			w.WriteHeader(401)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/items") && r.URL.Query().Get("offset") == "":
			fmt.Fprintf(w, `{"items":[%s],"next":%q,"total":3}`, items("1", "2"), srv.URL+r.URL.Path+"?offset=2")
		case strings.HasSuffix(r.URL.Path, "/items"):
			fmt.Fprintf(w, `{"items":[%s],"next":null,"total":3}`, items("3"))
		default:
			fmt.Fprint(w, `{"name":"Minha"}`)
		}
	})
	a := &api{hc: http.DefaultClient, lim: newLimiter(0), id: "c",
		user: &userAuth{tok: &UserToken{ClientID: "c", Access: "user-1", Expiry: time.Now().Add(time.Hour)}}}
	name, tracks, err := a.playlist(context.Background(), "p")
	if err != nil || name != "Minha" || len(tracks) != 3 || tracks[2].ID != "3" {
		t.Fatalf("name=%q tracks=%d err=%v", name, len(tracks), err)
	}
}

func TestPlaylistRestricted(t *testing.T) {
	for _, tc := range []struct {
		name string
		h    http.HandlerFunc
	}{
		{"401 no client credentials", func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/items") {
				w.WriteHeader(401)
				return
			}
			fmt.Fprint(w, `{"name":"Dos outros"}`)
		}},
		{"sem items (playlist de outra pessoa)", func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/items") {
				fmt.Fprint(w, `{"total":290}`)
				return
			}
			fmt.Fprint(w, `{"name":"Dos outros"}`)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fakeSpotify(t, tc.h)
			a := &api{hc: http.DefaultClient, lim: newLimiter(0), id: "c", secret: "s"}
			if _, _, err := a.playlist(context.Background(), "p"); !errors.Is(err, ErrPlaylistRestricted) {
				t.Errorf("err = %v", err)
			}
		})
	}
}

func TestNewLoadsTokenOnlyForSameClientID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tok.json")
	if err := (&UserToken{ClientID: "app-a", Access: "x", Expiry: time.Now().Add(time.Hour)}).Save(path); err != nil {
		t.Fatal(err)
	}
	if c := New(Options{ClientID: "app-a", TokenFile: path}); !c.LoggedIn() || !c.HasAPI() {
		t.Error("deveria usar o login (sem secret)")
	}
	if c := New(Options{ClientID: "app-b", TokenFile: path}); c.LoggedIn() || c.HasAPI() {
		t.Error("token de outro Client ID não pode valer")
	}
}
