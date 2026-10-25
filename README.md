# spotdown

Baixa faixas, álbuns e playlists do Spotify com uma TUI em Go:

```
Spotify (páginas públicas)  →  YouTube Music (busca)  →  yt-dlp + ffmpeg  →  tags, capa e letra
       metadados                 match do áudio             download           arquivo final
```

- **Sem login**: os metadados vêm das páginas públicas do Spotify, e as playlists inteiras do web player, pelo mesmo método do downtify. A API oficial, com as suas credenciais, é opcional.
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
spotdown                       # abre a interface interativa
spotdown LINK...               # baixa direto, em texto (bom para scripts e cron)
spotdown --dry-run LINK...     # só lista os caminhos que seriam gerados
```

As flags podem vir antes ou depois dos links. Exemplos:

```sh
spotdown -o "{album-artist}/{album}/{disc-number}-{track-number:02} - {title}" --format opus LINK_DO_ALBUM
spotdown --m3u --m3u-name "Playlists/{list-name}.m3u8" -o "{list-name}/{list-position:03} - {artists} - {title}" LINK_DA_PLAYLIST
spotdown --overwrite metadata LINK   # só regrava as tags dos arquivos que já existem
```

### Interface interativa

A interface segue o estilo do deploy-tools: as ações ficam numa barra lateral e o painel ao lado descreve a selecionada, com o comando equivalente em "Pelo terminal:". Ao abrir uma ação, o painel passa a executá-la, com as perguntas e a saída ali dentro.

- **Baixar**
  - **Baixar links**: cole um ou mais links. Depois da leitura, escolha baixar todas as faixas, escolher quais (lista com filtro; `●` marca as que já existem) ou cancelar. Durante o download, uma linha de status mostra as faixas em andamento.
  - **Simular caminhos**: mostra os caminhos que seriam gerados, sem baixar nada.
- **Opções**: caminho dos arquivos (com prévia ao vivo do template), formato e qualidade, playlists M3U, letras, arquivos existentes, downloads simultâneos e API do Spotify. Cada uma grava direto no arquivo de configuração.
- **Sistema**: dependências (versões do yt-dlp e do ffmpeg), limpar cache e arquivo de configuração.

Teclas: `↑↓` navega, `enter` abre e `q` sai. Durante uma ação, `ctrl+c` duas vezes cancela sem deixar arquivos pela metade nem temporários. Com `ACCESSIBLE=1`, os mesmos itens viram um menu por linhas, para leitores de tela.

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
| `{isrc}` | ISRC (só com `spotify_source = "official"`) |
| `{publisher}` | gravadora |
| `{genre}` | gênero (quase nunca disponível) |
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

### Fonte do Spotify: web player (padrão) ou API oficial

O embed público do Spotify para em 100 faixas. Para trazer playlists inteiras, o spotdown tem duas fontes, configuráveis em **Opções › Fonte do Spotify**, em `spotify_source` no config ou pela flag `--spotify-source`.

**`web` — web player (padrão, sem conta).** É o mesmo método do [downtify](https://github.com/henriquesebastiao/downtify):

1. O embed traz um token anônimo do player embutido.
2. Com esse token, o spotdown chama a API GraphQL interna do web player (`api-partner.spotify.com/pathfinder`, consulta `fetchPlaylist`).
3. A playlist vem inteira, de qualquer pessoa, de 100 em 100: são só 3 requisições para 290 faixas.
4. Álbum, data e gravadora vêm da página pública de cada álbum, com cache, uma requisição por álbum e não por faixa.

Cuidados com essa fonte:

- **Não é uma API pública** e vai contra os termos de uso do Spotify. Pode parar de funcionar, ou o IP pode ser limitado, sem aviso.
- **O hash da consulta muda.** Quando o Spotify atualiza o player, aparece o aviso `PersistedQueryNotFound` e as playlists caem para o embed, até 100 faixas. Para voltar a funcionar sem esperar uma release, preencha `spotify_graphql_hash` com o valor novo, tirado do código do web player ou do `_GRAPHQL_HASH` em `downtify/spotify.py`.
- **Qualquer outra falha** também cai para o embed, com um aviso.

**`official` — API oficial, com as suas credenciais.** É estável e documentada, mas, desde [fevereiro de 2026](https://developer.spotify.com/documentation/web-api/tutorials/february-2026-migration-guide), apps pessoais (*Development Mode*) só recebem o conteúdo de playlists **suas ou colaborativas**, e só depois do login da sua conta. As de outras pessoas ficam em 100 faixas. O dono do app precisa ter **Spotify Premium**. Para configurar:

1. Em <https://developer.spotify.com/dashboard>, clique em **Create app** e preencha nome e descrição (qualquer texto).
2. Em **Redirect URIs**, coloque exatamente `http://127.0.0.1:8888/callback` e clique em **Add**. O login volta para esse endereço, e o spotdown escuta ali enquanto você entra. O Spotify não aceita `localhost`.
3. Em **Which API/SDKs are you planning to use?**, marque **Web API**, aceite os termos e salve.
4. Em **Settings**, copie o **Client ID**. O secret é opcional.
5. Em **Opções › Fonte do Spotify**, escolha **Usar a API oficial** e depois **Entrar com a conta do Spotify**. Pelo terminal:

   ```sh
   spotdown --login     # abre o navegador; autorize e volte ao terminal
   spotdown --logout    # apaga o login
   ```

O login usa o fluxo *Authorization Code + PKCE*, que não precisa do secret, e pede só permissão para ler suas playlists (`playlist-read-private` e `playlist-read-collaborative`). O token fica em `~/.config/spotdown/spotify-token.json` e é renovado sozinho. Se outra pessoa for usar o seu app, cadastre a conta dela em **User Management**, no dashboard.

O Client ID também pode ficar no config (`spotify_client_id`) ou nas variáveis `SPOTIFY_CLIENT_ID`/`SPOTIFY_CLIENT_SECRET`. Com a fonte `web`, as credenciais ficam guardadas, mas não são usadas.

## Arquivos

- `~/.config/spotdown/config.toml`: configuração (permissão 0600, porque pode guardar o Client Secret).
- `~/.config/spotdown/spotify-token.json`: login do Spotify (permissão 0600). Apague com `spotdown --logout`.
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
| `internal/ui` | prompts (huh) e saída formatada, iguais aos do deploy-tools |
| `internal/tui` | barra lateral + painel (bubbletea) |
| `internal/cli` | ações: download, opções e sistema |

Os commits seguem Conventional Commits. A cada push na `main`, o workflow de release gera a versão com commitizen e publica os binários estáticos.
