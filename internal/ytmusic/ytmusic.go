// Package ytmusic busca no YouTube Music (API InnerTube do cliente web, sem
// login) e escolhe o resultado que melhor corresponde a uma faixa do Spotify.
package ytmusic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// UserAgent é definido pelo main com a versão.
var UserAgent = "spotdown/dev (+https://github.com/vinicgobbi/spotdown)"

// Filtros de busca (parâmetro "params" do InnerTube).
const (
	filterSongs  = "EgWKAQIIAWoMEA4QChADEAQQCRAF"
	filterVideos = "EgWKAQIQAWoMEA4QChADEAQQCRAF"
)

// Result é um item da busca.
type Result struct {
	VideoID  string
	Title    string
	Artists  []string // artistas (músicas) ou canal (vídeos)
	Album    string
	Duration int // segundos
	IsSong   bool
}

// URL devolve o link do resultado no YouTube Music.
func (r Result) URL() string { return "https://music.youtube.com/watch?v=" + r.VideoID }

// Client faz as buscas.
type Client struct {
	HTTP *http.Client
}

// New cria um cliente com timeout.
func New() *Client { return &Client{HTTP: &http.Client{Timeout: 20 * time.Second}} }

func clientVersion() string {
	return "1." + time.Now().AddDate(0, 0, -7).Format("20060102") + ".01.00"
}

// Search busca músicas (songs=true) ou vídeos.
func (c *Client) Search(ctx context.Context, query string, songs bool) ([]Result, error) {
	params := filterVideos
	if songs {
		params = filterSongs
	}
	payload, _ := json.Marshal(map[string]any{
		"context": map[string]any{"client": map[string]any{
			"clientName": "WEB_REMIX", "clientVersion": clientVersion(), "hl": "en", "gl": "US",
		}},
		"query":  query,
		"params": params,
	})
	var body []byte
	var err error
	for attempt := range 3 {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt) * 2 * time.Second):
			}
		}
		body, err = c.post(ctx, payload)
		if err == nil {
			break
		}
	}
	if err != nil {
		return nil, err
	}
	return parseSearch(body, songs)
}

func (c *Client) post(ctx context.Context, payload []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://music.youtube.com/youtubei/v1/search?prettyPrint=false", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://music.youtube.com")
	req.Header.Set("User-Agent", UserAgent)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("YouTube Music: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("YouTube Music: HTTP %d", resp.StatusCode)
	}
	return body, nil
}

// ---- parser ----

type run struct {
	Text               string `json:"text"`
	NavigationEndpoint *struct {
		WatchEndpoint *struct {
			VideoID string `json:"videoId"`
		} `json:"watchEndpoint"`
		BrowseEndpoint *struct {
			Configs struct {
				Music struct {
					PageType string `json:"pageType"`
				} `json:"browseEndpointContextMusicConfig"`
			} `json:"browseEndpointContextSupportedConfigs"`
		} `json:"browseEndpoint"`
	} `json:"navigationEndpoint"`
}

func (r run) pageType() string {
	if r.NavigationEndpoint == nil || r.NavigationEndpoint.BrowseEndpoint == nil {
		return ""
	}
	return r.NavigationEndpoint.BrowseEndpoint.Configs.Music.PageType
}

type listItem struct {
	FlexColumns []struct {
		Col struct {
			Text struct {
				Runs []run `json:"runs"`
			} `json:"text"`
		} `json:"musicResponsiveListItemFlexColumnRenderer"`
	} `json:"flexColumns"`
	PlaylistItemData *struct {
		VideoID string `json:"videoId"`
	} `json:"playlistItemData"`
}

var durationRe = regexp.MustCompile(`^\d{1,2}(:\d{2}){1,2}$`)

func parseSearch(body []byte, songs bool) ([]Result, error) {
	var doc struct {
		Contents struct {
			Tabbed struct {
				Tabs []struct {
					TabRenderer struct {
						Content struct {
							SectionList struct {
								Contents []struct {
									Shelf *struct {
										Contents []struct {
											Item *listItem `json:"musicResponsiveListItemRenderer"`
										} `json:"contents"`
									} `json:"musicShelfRenderer"`
								} `json:"contents"`
							} `json:"sectionListRenderer"`
						} `json:"content"`
					} `json:"tabRenderer"`
				} `json:"tabs"`
			} `json:"tabbedSearchResultsRenderer"`
		} `json:"contents"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("YouTube Music: resposta inválida: %w", err)
	}
	var out []Result
	for _, tab := range doc.Contents.Tabbed.Tabs {
		for _, sec := range tab.TabRenderer.Content.SectionList.Contents {
			if sec.Shelf == nil {
				continue
			}
			for _, it := range sec.Shelf.Contents {
				if it.Item == nil {
					continue
				}
				if r, ok := parseItem(it.Item, songs); ok {
					out = append(out, r)
				}
			}
		}
	}
	return out, nil
}

// parseItem lê uma linha: coluna 0 = título; coluna 1 = grupos separados por
// " • " (artistas • álbum • duração, ou canal • views • duração).
func parseItem(it *listItem, songs bool) (Result, bool) {
	if len(it.FlexColumns) < 2 {
		return Result{}, false
	}
	r := Result{IsSong: songs}
	for _, ru := range it.FlexColumns[0].Col.Text.Runs {
		r.Title += ru.Text
		if ru.NavigationEndpoint != nil && ru.NavigationEndpoint.WatchEndpoint != nil {
			r.VideoID = ru.NavigationEndpoint.WatchEndpoint.VideoID
		}
	}
	if r.VideoID == "" && it.PlaylistItemData != nil {
		r.VideoID = it.PlaylistItemData.VideoID
	}
	if r.VideoID == "" {
		return Result{}, false
	}

	var groups [][]run
	cur := []run{}
	for _, ru := range it.FlexColumns[1].Col.Text.Runs {
		if strings.TrimSpace(ru.Text) == "•" {
			groups = append(groups, cur)
			cur = []run{}
			continue
		}
		cur = append(cur, ru)
	}
	groups = append(groups, cur)

	for gi, g := range groups {
		text := ""
		for _, ru := range g {
			text += ru.Text
		}
		text = strings.TrimSpace(text)
		switch {
		case gi == 0:
			r.Artists = splitArtistRuns(g)
		case durationRe.MatchString(text):
			r.Duration = parseDuration(text)
		case len(g) > 0 && g[0].pageType() == "MUSIC_PAGE_TYPE_ALBUM":
			r.Album = text
		}
	}
	return r, true
}

// splitArtistRuns separa "A, B & C" (os nomes vêm em runs separados por
// runs de texto ", " e " & ").
func splitArtistRuns(g []run) []string {
	var out []string
	for _, ru := range g {
		t := strings.TrimSpace(ru.Text)
		if t == "" || t == "," || t == "&" {
			continue
		}
		out = append(out, t)
	}
	if len(out) == 1 {
		// Um run só (comum em vídeos): pode ser "A & B".
		out = splitRe.Split(out[0], -1)
	}
	return out
}

var splitRe = regexp.MustCompile(`\s*(?:,|&)\s*`)

func parseDuration(s string) int {
	total := 0
	for _, p := range strings.Split(s, ":") {
		n, _ := strconv.Atoi(p)
		total = total*60 + n
	}
	return total
}
