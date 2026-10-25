package spotify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/vinicgobbi/spotdown/internal/track"
)

// Fonte padrão para playlists, no mesmo método do downtify
// (github.com/henriquesebastiao/downtify): o embed traz um token anônimo do
// player embutido, e com ele a API GraphQL interna do web player
// (api-partner.spotify.com/pathfinder) devolve a playlist inteira, de 100 em
// 100, de qualquer pessoa, sem app nem login. Não é API pública: o Spotify
// pode mudar ou fechar a qualquer momento — então tudo cai para o embed
// (até 100 faixas) quando falhar.

// partnerAPI é variável só para os testes apontarem para um servidor falso.
var partnerAPI = "https://api-partner.spotify.com/pathfinder/v1/query"

// DefaultGraphQLHash é o sha256 da consulta fetchPlaylist no código do web
// player (o mesmo valor usado pelo downtify em out/2026). Quando o Spotify
// atualiza o player, a API responde PersistedQueryNotFound: aí é preciso o
// hash novo (spotify_graphql_hash na config, sem esperar uma release).
const DefaultGraphQLHash = "a65e12194ed5fc443a1cdebed5fabe33ca5b07b987185d63c72483867ad13cb4"

const (
	gqlPageSize = 100
	gqlEvery    = pageEvery * 2 // intervalo mínimo entre páginas
)

// ErrGraphQLHash indica que o hash da consulta ficou desatualizado.
var ErrGraphQLHash = errors.New("hash da consulta do web player desatualizado (PersistedQueryNotFound): atualize spotify_graphql_hash")

// gqlTrack é a faixa como o GraphQL devolve (mesmo formato da página da
// playlist, com a duração às vezes em trackDuration).
type gqlTrack struct {
	pageTrack
	TrackDuration struct {
		TotalMilliseconds int `json:"totalMilliseconds"`
	} `json:"trackDuration"`
}

type gqlPage struct {
	Name    string `json:"name"`
	Content struct {
		TotalCount int `json:"totalCount"`
		Items      []struct {
			ItemV2 struct {
				TypeName string          `json:"__typename"`
				Data     json.RawMessage `json:"data"`
			} `json:"itemV2"`
		} `json:"items"`
	} `json:"content"`
}

func (p *gqlPage) tracks() []track.Track {
	var out []track.Track
	for _, it := range p.Content.Items {
		if it.ItemV2.TypeName != "TrackResponseWrapper" {
			continue // episódios, faixas indisponíveis
		}
		var g gqlTrack
		if json.Unmarshal(it.ItemV2.Data, &g) != nil {
			continue
		}
		t := g.track()
		if !idRe.MatchString(t.ID) {
			continue // arquivos locais (spotify:local:...)
		}
		if t.DurationMs == 0 {
			t.DurationMs = g.TrackDuration.TotalMilliseconds
		}
		out = append(out, t)
	}
	return out
}

// webPlaylistPage pede uma página da playlist ao GraphQL do web player.
func (c *Client) webPlaylistPage(ctx context.Context, id, token string, offset int) (*gqlPage, error) {
	vars, _ := json.Marshal(map[string]any{
		"uri": "spotify:playlist:" + id, "offset": offset, "limit": gqlPageSize,
		"enableWatchFeedEntrypoint": false, "includeEpisodeContentRatingsV2": false,
	})
	ext, _ := json.Marshal(map[string]any{
		"persistedQuery": map[string]any{"version": 1, "sha256Hash": c.gqlHash},
	})
	q := url.Values{"operationName": {"fetchPlaylist"}, "variables": {string(vars)}, "extensions": {string(ext)}}
	body, err := get(ctx, c.hc, c.gqlLim, partnerAPI+"?"+q.Encode(), http.Header{
		"Authorization": {"Bearer " + token},
		"App-Platform":  {"WebPlayer"},
	})
	if err != nil {
		return nil, err
	}
	var doc struct {
		Data struct {
			PlaylistV2 *gqlPage `json:"playlistV2"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("resposta inválida: %w", err)
	}
	if len(doc.Errors) > 0 {
		if strings.Contains(doc.Errors[0].Message, "PersistedQueryNotFound") {
			return nil, ErrGraphQLHash
		}
		return nil, errors.New(doc.Errors[0].Message)
	}
	if doc.Data.PlaylistV2 == nil {
		return nil, errors.New("resposta sem playlist")
	}
	return doc.Data.PlaylistV2, nil
}

// webPlaylist busca a playlist inteira pelo GraphQL do web player.
func (c *Client) webPlaylist(ctx context.Context, id, token string) (string, []track.Track, error) {
	first, err := c.webPlaylistPage(ctx, id, token, 0)
	if err != nil {
		return "", nil, err
	}
	tracks := first.tracks()
	total, offset := first.Content.TotalCount, len(first.Content.Items)
	// Avança pelo que cada página de fato trouxe (itens filtrados contam).
	for offset < total {
		p, err := c.webPlaylistPage(ctx, id, token, offset)
		if err != nil {
			return "", nil, err
		}
		if len(p.Content.Items) == 0 {
			break
		}
		tracks = append(tracks, p.tracks()...)
		offset += len(p.Content.Items)
	}
	return first.Name, tracks, nil
}
