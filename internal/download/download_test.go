package download

import (
	"testing"
	"time"
)

func TestParseProgress(t *testing.T) {
	cases := []struct {
		in   string
		want float64
		ok   bool
	}{
		{"1024 4096 NA", 0.25, true},
		{"2048 NA 4096.5", 2048 / 4096.5, true},
		{"10 NA NA", 0, false},
	}
	for _, c := range cases {
		got, ok := parseProgress(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("%q: got %v %v", c.in, got, ok)
		}
	}
}

func TestAgeDays(t *testing.T) {
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	if d, ok := ageDays("2024.04.09", now); !ok || d < 900 {
		t.Errorf("got %d %v", d, ok)
	}
	if d, ok := ageDays("2026.10.21.1", now); !ok || d != 3 {
		t.Errorf("got %d %v", d, ok)
	}
	if _, ok := ageDays("nightly", now); ok {
		t.Error("esperava falha")
	}
}

func TestValid(t *testing.T) {
	if (Options{Format: "mp3", Bitrate: "320k"}).Valid() != nil {
		t.Error("mp3 320k deveria valer")
	}
	if (Options{Format: "wav"}).Valid() == nil || (Options{Format: "mp3", Bitrate: "alto"}).Valid() == nil {
		t.Error("esperava erro")
	}
}
