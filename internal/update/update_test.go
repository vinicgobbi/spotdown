package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeGitHub serves a latest release with one binary and its .sha256.
func fakeGitHub(t *testing.T, tag string, bin []byte, sum string) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest":
			fmt.Fprintf(w, `{"tag_name":%q,"assets":[
				{"name":"spotdown-linux-amd64","browser_download_url":"%s/dl/bin"},
				{"name":"spotdown-linux-amd64.sha256","browser_download_url":"%s/dl/sum"}]}`, tag, srv.URL, srv.URL)
		case "/dl/bin":
			_, _ = w.Write(bin)
		case "/dl/sum":
			fmt.Fprintf(w, "%s  spotdown-linux-amd64\n", sum)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func sha(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func TestLatestAndDownload(t *testing.T) {
	bin := []byte("#!/bin/sh\necho new\n")
	srv := fakeGitHub(t, "v0.10.0", bin, sha(bin))
	c := Client{URL: srv.URL + "/latest"}
	r, err := c.Latest(context.Background())
	if err != nil || r.Version != "v0.10.0" {
		t.Fatalf("Latest = %+v, %v", r, err)
	}
	dir := t.TempDir()
	path, err := c.Download(context.Background(), r, AssetName("spotdown", "linux", "amd64"), dir)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	fi, _ := os.Stat(path)
	if string(got) != string(bin) || fi.Mode().Perm() != 0o755 || filepath.Dir(path) != dir {
		t.Fatalf("downloaded %q mode %v at %s", got, fi.Mode(), path)
	}
	if _, err := c.Download(context.Background(), r, "deploy-dev-linux-amd64", dir); err == nil {
		t.Fatal("missing asset must fail")
	}
}

func TestDownloadRejectsWrongChecksum(t *testing.T) {
	bin := []byte("tampered")
	srv := fakeGitHub(t, "v0.10.0", bin, sha([]byte("original")))
	c := Client{URL: srv.URL + "/latest"}
	r, _ := c.Latest(context.Background())
	dir := t.TempDir()
	if _, err := c.Download(context.Background(), r, "spotdown-linux-amd64", dir); err == nil || !strings.Contains(err.Error(), "não confere") {
		t.Fatalf("err = %v", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("left files behind: %v", entries)
	}
}

func TestLatestRejectsOddTag(t *testing.T) {
	srv := fakeGitHub(t, "nightly", nil, "")
	if _, err := (Client{URL: srv.URL + "/latest"}).Latest(context.Background()); err == nil {
		t.Fatal("odd tag accepted")
	}
}

func TestVersions(t *testing.T) {
	for _, c := range []struct {
		cur, latest string
		newer       bool
	}{
		{"v0.9.0", "v0.10.0", true},
		{"v0.10.0", "v0.10.0", false},
		{"v1.0.0", "v0.10.0", false},
		{"dev", "v0.10.0", false},
	} {
		if got := Newer(c.cur, c.latest); got != c.newer {
			t.Errorf("Newer(%s, %s) = %v", c.cur, c.latest, got)
		}
	}
	for v, want := range map[string]bool{"v0.9.0": true, "dev": false, "v0.9.0-3-gabc1234": false, "v0.9.0-dirty": false, "0.9.0": false} {
		if Updatable(v) != want {
			t.Errorf("Updatable(%q) != %v", v, want)
		}
	}
	if AssetName("deploy-dev", "windows", "amd64") != "deploy-dev-windows-amd64.exe" {
		t.Fatal("windows asset name")
	}
}

func TestDueAndTouch(t *testing.T) {
	stamp := filepath.Join(t.TempDir(), "cache", "update-check")
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	if !Due(stamp, 24*time.Hour, now) {
		t.Fatal("never checked must be due")
	}
	if err := Touch(stamp, now); err != nil {
		t.Fatal(err)
	}
	if Due(stamp, 24*time.Hour, now.Add(23*time.Hour)) || !Due(stamp, 24*time.Hour, now.Add(25*time.Hour)) {
		t.Fatal("throttle")
	}
}
