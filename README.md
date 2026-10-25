# spotdown

Baixa faixas, álbuns e playlists do Spotify com uma TUI em Go:

```
Spotify (páginas públicas)  →  YouTube Music (busca)  →  yt-dlp + ffmpeg  →  tags, capa e letra
       metadados                 match do áudio             download           arquivo final
```

- **Sem login**: os metadados vêm das páginas públicas do Spotify (embed e página da faixa/álbum).
- **Caminho de saída por template**, com as mesmas variáveis do spotdl, além de zeros à esquerda (`{track-number:02}`).
- **M3U** para cada álbum e playlist baixado (faixas soltas não geram playlist).
- **Letras** do [LRCLIB](https://lrclib.net) (sincronizadas quando houver): embutidas no arquivo, em `.lrc` ao lado, nos dois ou desligadas.
- **Tags completas** em mp3 (ID3v2.4), m4a, flac, opus e ogg: título, artistas, álbum, artista do álbum, faixa/total, disco/total, data, gravadora, capa, letra e link do Spotify.
- **Cache e limite de requisições**: metadados de faixas e álbuns ficam em cache por 30 dias, e as requisições a cada host respeitam um intervalo mínimo e o `Retry-After`.

## Requisitos

- **yt-dlp recente.** O YouTube muda com frequência, e versões antigas, como a dos repositórios das distros, falham com "Please sign in". Use `pipx install yt-dlp` ou o binário oficial de <https://github.com/yt-dlp/yt-dlp/releases>, e mantenha atualizado com `yt-dlp -U`. O spotdown avisa quando a versão tem mais de 120 dias.
- **ffmpeg** no PATH.

## Uso

```sh
spotdown                       # abre a TUI para colar os links
spotdown LINK...               # já lê os links e vai para as opções
spotdown -y LINK...            # baixa direto, sem revisar
spotdown --no-tui LINK...      # texto simples (scripts/cron; padrão sem terminal)
spotdown --dry-run LINK...     # só lista os caminhos que seriam gerados
```

As flags podem vir antes ou depois dos links. Exemplos:

```sh
spotdown -o "{album-artist}/{album}/{disc-number}-{track-number:02} - {title}" --format opus LINK_DO_ALBUM
spotdown --m3u --m3u-name "Playlists/{list-name}.m3u8" -o "{list-name}/{list-position:03} - {artists} - {title}" LINK_DA_PLAYLIST
spotdown --overwrite metadata LINK   # só regrava as tags dos arquivos que já existem
```

Na TUI o fluxo é: **Links → Leitura → Opções → Revisão → Download → Fim**.

- **Opções**: mostra uma prévia do caminho final usando a primeira faixa lida, que muda enquanto você digita. `tab` lista as variáveis, e `ctrl+s` grava as opções como padrão.
- **Revisão**: lista cada faixa com o caminho de destino. `espaço` marca ou desmarca, e as que já existem aparecem com `●`.
- **Download**: `ctrl+c` pede confirmação e cancela sem deixar arquivos pela metade nem temporários.

## Template de caminho

| Variável | Conteúdo |
|---|---|
| `{title}` | título |
| `{artists}` / `{artist}` | todos os artistas (`A, B`) / artista principal |
| `{album}` / `{album-artist}` | álbum / artista do álbum |
| `{track-number}` / `{tracks-count}` | número da faixa / faixas no álbum |
| `{disc-number}` / `{disc-count}` | disco / quantidade de discos |
| `{year}` / `{original-date}` | ano / data (AAAA-MM-DD) |
| `{duration}` | duração em segundos |
| `{isrc}` | ISRC (só com a API do Spotify) |
| `{publisher}` | gravadora |
| `{genre}` | gênero (só com a API do Spotify, e raro) |
| `{track-id}` | ID da faixa no Spotify |
| `{list-name}` / `{list-position}` / `{list-length}` | nome, posição e tamanho do álbum/playlist baixado |
| `{output-ext}` | extensão (`mp3`, `m4a`...) |

Regras, iguais às do spotdl:

- Um template terminado em `/` recebe `{artists} - {title}.{output-ext}`.
- Sem `{output-ext}`, a extensão é acrescentada.
- Templates relativos partem de `output_dir`. `~` e `$VARIAVEIS` são expandidos.
- Os valores são sanitizados por componente: `AC/DC` vira `AC-DC`, e não uma subpasta. Uma variável vazia não cria uma pasta vazia.
- `--restrict ascii` remove os acentos, e `--restrict strict` também troca espaços por `_` e mantém só `[A-Za-z0-9._-]`.
- Duas faixas diferentes que gerariam o mesmo caminho recebem ` (2)`, ` (3)`... Uma faixa que aparece em mais de um link é baixada uma vez só.

## M3U

Com `--m3u` (ou `m3u = true`), cada álbum e cada playlist ganha um `.m3u8`. Por padrão ele se chama `{list-name}.m3u8`, fica em `output_dir` e usa caminhos relativos à pasta dele. Use `--m3u-absolute` para caminhos absolutos.

- As faixas aparecem na ordem original, incluindo as que já existiam.
- As que não foram baixadas viram comentário (`# ausente: ... (motivo)`), para você ver o que faltou.
- Se o nome do M3U não usar `{list-name}`, as listas são juntadas num arquivo só.

## Configuração

O arquivo fica em `~/.config/spotdown/config.toml` e é criado na primeira execução, com todos os campos comentados. As flags têm prioridade sobre o arquivo, e `--save-config` grava as flags passadas como novo padrão.

### Playlists com mais de 100 faixas

Sem login, o Spotify só libera as **primeiras 100 faixas** de uma playlist pela página pública. O spotdown avisa quando isso acontece. Para pegar a playlist completa, além do ISRC e do gênero, crie um app gratuito em <https://developer.spotify.com/dashboard>:

1. Clique em **Create app** e preencha nome e descrição (qualquer texto).
2. Em **Redirect URIs**, coloque `http://127.0.0.1:8888/callback` e clique em **Add**. O spotdown nunca usa essa URL, porque o fluxo *client credentials* não tem redirect, mas o formulário exige uma. O Spotify não aceita `localhost`: use o IP `127.0.0.1` ou uma URL `https://`.
3. Em **Which API/SDKs are you planning to use?**, marque **Web API**, aceite os termos e salve.
4. Em **Settings**, copie o **Client ID** e, em **View client secret**, o **Client secret**.

Depois, configure as credenciais:

```toml
spotify_client_id = "..."
spotify_client_secret = "..."
```

Também dá para usar as variáveis `SPOTIFY_CLIENT_ID` e `SPOTIFY_CLIENT_SECRET`, que não são gravadas no arquivo.

Esse é o fluxo *client credentials* da API oficial, sem login de usuário. Para economizar requisições, a consulta extra a cada álbum (que traz gravadora, gênero e quantidade de discos) só é feita quando o template usa `{publisher}`, `{disc-count}` ou `{genre}`.

## Arquivos

- `~/.config/spotdown/config.toml`: configuração (permissão 0600, porque pode guardar o Client Secret).
- `~/.cache/spotdown/meta/`: cache de metadados. Pode apagar à vontade.
- `<output_dir>/spotdown-falhas.txt`: faixas sem correspondência ou com erro na última execução, com o link do Spotify.

## Desenvolvimento

```sh
go vet ./... && go test ./...    # os testes de tags usam o ffmpeg, se houver
go build -o spotdown ./cmd/spotdown
```

| Pacote | Função |
|---|---|
| `internal/spotify` | links, embed, páginas públicas, API opcional, cache e limitador |
| `internal/ytmusic` | busca InnerTube e nota de correspondência (título, artistas, duração, versão) |
| `internal/download` | yt-dlp, progresso e cancelamento limpo |
| `internal/tag` | ID3 em Go; demais formatos via ffmpeg, sem reconverter |
| `internal/lyrics` | LRCLIB |
| `internal/pathtpl` | template e sanitização |
| `internal/m3u` | playlists |
| `internal/engine` | pipeline concorrente e eventos |
| `internal/tui` | interface bubbletea |
| `internal/plain` | saída em texto |

Os commits seguem Conventional Commits. A cada push na `main`, o workflow de release gera a versão com commitizen e publica os binários estáticos.
