// Package track define a faixa que passa por todo o pipeline: metadados do
// Spotify, match no YouTube Music, download, tags e caminho final.
package track

import "strings"

// Track reúne os metadados de uma faixa. Campos vazios/zero significam
// "desconhecido" e são preenchidos por fontes posteriores quando possível.
type Track struct {
	ID          string // ID do Spotify (22 caracteres)
	Title       string
	Artists     []string
	Album       string
	AlbumID     string
	AlbumArtist string
	Genre       string
	DiscNumber  int
	DiscCount   int
	TrackNumber int
	TracksCount int
	DurationMs  int
	Date        string // AAAA-MM-DD, AAAA-MM ou AAAA
	ISRC        string
	Publisher   string
	Explicit    bool
	CoverURL    string

	// Contexto da lista (playlist/álbum) de onde a faixa veio.
	ListName     string
	ListPosition int
	ListLength   int
}

// URL devolve o link público da faixa no Spotify.
func (t Track) URL() string { return "https://open.spotify.com/track/" + t.ID }

// Artist devolve o artista principal.
func (t Track) Artist() string {
	if len(t.Artists) == 0 {
		return ""
	}
	return t.Artists[0]
}

// ArtistsString junta os artistas como o spotdl: "A, B, C".
func (t Track) ArtistsString() string { return strings.Join(t.Artists, ", ") }

// Year devolve o ano da data de lançamento.
func (t Track) Year() string {
	if len(t.Date) >= 4 {
		return t.Date[:4]
	}
	return ""
}

// Seconds devolve a duração em segundos, arredondada.
func (t Track) Seconds() int { return (t.DurationMs + 500) / 1000 }
