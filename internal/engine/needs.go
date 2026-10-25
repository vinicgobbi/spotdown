package engine

import (
	"strings"

	"github.com/vinicgobbi/spotdown/internal/config"
)

// NeedsAlbumDetails informa se os templates usam dados que, na leitura pela
// API do Spotify, só vêm numa consulta extra ao álbum (página pública). Sem
// eles, essa consulta é pulada — uma playlist de 500 faixas de 400 álbuns
// economiza 400 requisições.
func NeedsAlbumDetails(cfg config.Config) bool {
	tpl := cfg.Output
	if cfg.M3U {
		tpl += cfg.M3UName
	}
	for _, v := range []string{"{publisher", "{disc-count", "{genre"} {
		if strings.Contains(tpl, v) {
			return true
		}
	}
	return false
}
