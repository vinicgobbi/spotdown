package spotify

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// Kind é o tipo de recurso do Spotify.
type Kind string

const (
	KindTrack    Kind = "track"
	KindAlbum    Kind = "album"
	KindPlaylist Kind = "playlist"
)

var idRe = regexp.MustCompile(`^[A-Za-z0-9]{22}$`)

// ParseURL aceita "https://open.spotify.com/[intl-xx/]{tipo}/{id}?...",
// "spotify:{tipo}:{id}" e o link de embed.
func ParseURL(s string) (Kind, string, error) {
	s = strings.TrimSpace(s)
	var kind, id string
	if rest, ok := strings.CutPrefix(s, "spotify:"); ok {
		parts := strings.Split(rest, ":")
		if len(parts) >= 2 {
			kind, id = parts[len(parts)-2], parts[len(parts)-1]
		}
	} else {
		u, err := url.Parse(s)
		if err != nil || !strings.HasSuffix(u.Host, "spotify.com") {
			return "", "", fmt.Errorf("não é um link do Spotify: %s", s)
		}
		var segs []string
		for _, p := range strings.Split(u.Path, "/") {
			if p != "" && p != "embed" && !strings.HasPrefix(p, "intl-") {
				segs = append(segs, p)
			}
		}
		if len(segs) >= 2 {
			kind, id = segs[len(segs)-2], segs[len(segs)-1]
		}
	}
	switch Kind(kind) {
	case KindTrack, KindAlbum, KindPlaylist:
	default:
		return "", "", fmt.Errorf("link não suportado (use faixa, álbum ou playlist): %s", s)
	}
	if !idRe.MatchString(id) {
		return "", "", fmt.Errorf("ID inválido no link: %s", s)
	}
	return Kind(kind), id, nil
}
