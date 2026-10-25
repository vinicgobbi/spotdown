// Package tag grava metadados, capa e letra no arquivo de áudio: mp3 com
// ID3v2.4 (escrito em Go) e os demais formatos com o ffmpeg (sem reconverter).
package tag

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"image"
	_ "image/jpeg" // registro dos decodificadores para ler as dimensões da capa
	_ "image/png"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/bogem/id3v2/v2"

	"github.com/vinicgobbi/spotdown/internal/track"
)

// Meta é o que vai para o arquivo.
type Meta struct {
	Track  track.Track
	Cover  []byte // JPEG ou PNG; vazio = sem capa
	Lyrics string // LRC sincronizado ou texto simples
	Source string // link do áudio no YouTube Music (vai no comentário)
}

// Write grava as tags em path, substituindo as que houver.
func Write(ctx context.Context, path string, m Meta) error {
	switch strings.ToLower(strings.TrimPrefix(filepath.Ext(path), ".")) {
	case "mp3":
		return writeID3(path, m)
	case "m4a", "flac", "opus", "ogg":
		return writeFFmpeg(ctx, path, m)
	}
	return fmt.Errorf("formato sem suporte a tags: %s", path)
}

func pair(n, total int) string {
	switch {
	case n <= 0:
		return ""
	case total > 0:
		return fmt.Sprintf("%d/%d", n, total)
	}
	return strconv.Itoa(n)
}

func comment(m Meta) string {
	c := m.Track.URL()
	if m.Source != "" {
		c += " | " + m.Source
	}
	return c
}

func albumArtist(t track.Track) string {
	if t.AlbumArtist != "" {
		return t.AlbumArtist
	}
	return t.Artist()
}

// ---- mp3 ----

func writeID3(path string, m Meta) error {
	tg, err := id3v2.Open(path, id3v2.Options{Parse: false})
	if err != nil {
		return fmt.Errorf("tags: %w", err)
	}
	defer tg.Close()
	tg.DeleteAllFrames()
	tg.SetVersion(4)
	tg.SetDefaultEncoding(id3v2.EncodingUTF8)

	t := m.Track
	text := func(id, v string) {
		if v != "" {
			tg.AddTextFrame(id, id3v2.EncodingUTF8, v)
		}
	}
	text("TIT2", t.Title)
	// ID3v2.4 separa múltiplos valores com NUL; a maioria dos players entende.
	text("TPE1", strings.Join(t.Artists, "\x00"))
	text("TPE2", albumArtist(t))
	text("TALB", t.Album)
	text("TRCK", pair(t.TrackNumber, t.TracksCount))
	text("TPOS", pair(t.DiscNumber, t.DiscCount))
	text("TDRC", t.Date)
	text("TCON", t.Genre)
	text("TPUB", t.Publisher)
	text("TSRC", t.ISRC)
	if t.DurationMs > 0 {
		text("TLEN", strconv.Itoa(t.DurationMs))
	}
	tg.AddCommentFrame(id3v2.CommentFrame{Encoding: id3v2.EncodingUTF8, Language: "eng", Text: comment(m)})
	tg.AddUserDefinedTextFrame(id3v2.UserDefinedTextFrame{Encoding: id3v2.EncodingUTF8, Description: "SPOTIFY_URL", Value: t.URL()})
	if m.Lyrics != "" {
		tg.AddUnsynchronisedLyricsFrame(id3v2.UnsynchronisedLyricsFrame{
			Encoding: id3v2.EncodingUTF8, Language: "eng", Lyrics: m.Lyrics,
		})
	}
	if len(m.Cover) > 0 {
		tg.AddAttachedPicture(id3v2.PictureFrame{
			Encoding: id3v2.EncodingUTF8, MimeType: http.DetectContentType(m.Cover),
			PictureType: id3v2.PTFrontCover, Description: "Cover", Picture: m.Cover,
		})
	}
	if err := tg.Save(); err != nil {
		return fmt.Errorf("tags: %w", err)
	}
	return nil
}

// ---- m4a / flac / opus / ogg ----

// writeFFmpeg passa as tags por um arquivo ffmetadata (a capa em base64 não
// cabe num argumento de linha de comando) e copia o áudio sem reconverter.
func writeFFmpeg(ctx context.Context, path string, m Meta) error {
	ext := strings.ToLower(filepath.Ext(path))
	vorbis := ext == ".opus" || ext == ".ogg"
	dir := filepath.Dir(path)

	t := m.Track
	fields := [][2]string{
		{"title", t.Title},
		{"artist", t.ArtistsString()},
		{"album_artist", albumArtist(t)},
		{"album", t.Album},
		{"date", t.Date},
		{"track", pair(t.TrackNumber, t.TracksCount)},
		{"disc", pair(t.DiscNumber, t.DiscCount)},
		{"genre", t.Genre},
		{"comment", comment(m)},
		{"lyrics", m.Lyrics},
	}
	if vorbis || ext == ".flac" {
		fields = append(fields,
			[2]string{"ISRC", t.ISRC},
			[2]string{"ORGANIZATION", t.Publisher}, // nome padrão do Vorbis comment para gravadora
			[2]string{"SPOTIFY_URL", t.URL()})
	} else {
		fields = append(fields, [2]string{"copyright", t.Publisher})
	}
	coverFile := ""
	if len(m.Cover) > 0 {
		if vorbis {
			// Ogg não aceita stream de imagem: a capa vai como bloco FLAC em base64.
			fields = append(fields, [2]string{"METADATA_BLOCK_PICTURE", pictureBlock(m.Cover)})
		} else {
			f, err := os.CreateTemp(dir, ".spotdown-cover-*")
			if err != nil {
				return err
			}
			coverFile = f.Name()
			defer os.Remove(coverFile)
			_, werr := f.Write(m.Cover)
			if cerr := f.Close(); werr != nil || cerr != nil {
				return fmt.Errorf("capa: %v %v", werr, cerr)
			}
		}
	}

	var meta bytes.Buffer
	meta.WriteString(";FFMETADATA1\n")
	for _, f := range fields {
		if f[1] != "" {
			meta.WriteString(f[0] + "=" + escape(f[1]) + "\n")
		}
	}
	mf, err := os.CreateTemp(dir, ".spotdown-meta-*")
	if err != nil {
		return err
	}
	defer os.Remove(mf.Name())
	_, werr := mf.Write(meta.Bytes())
	if cerr := mf.Close(); werr != nil || cerr != nil {
		return fmt.Errorf("tags: %v %v", werr, cerr)
	}

	out := filepath.Join(dir, ".spotdown-tag-"+filepath.Base(path))
	args := []string{"-v", "error", "-y", "-i", path, "-f", "ffmetadata", "-i", mf.Name()}
	if coverFile != "" {
		args = append(args, "-i", coverFile, "-map", "0:a", "-map", "2:0", "-disposition:v:0", "attached_pic")
	} else {
		args = append(args, "-map", "0:a")
	}
	args = append(args, "-map_metadata", "1", "-c", "copy")
	if vorbis {
		// No Ogg as tags ficam no stream, não no contêiner.
		args = append(args, "-map_metadata:s:a:0", "1:g")
	}
	if ext == ".m4a" {
		args = append(args, "-f", "mp4")
	}
	args = append(args, out)
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	if b, err := cmd.CombinedOutput(); err != nil {
		os.Remove(out)
		return fmt.Errorf("ffmpeg (tags): %s", strings.TrimSpace(string(b)))
	}
	return os.Rename(out, path)
}

// escape protege os caracteres especiais do formato ffmetadata.
func escape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, "=", `\=`, ";", `\;`, "#", `\#`, "\n", "\\\n")
	return r.Replace(strings.ReplaceAll(s, "\r", ""))
}

// pictureBlock monta o bloco PICTURE do FLAC (capa frontal) em base64.
func pictureBlock(img []byte) string {
	mime := http.DetectContentType(img)
	w, h := 0, 0
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(img)); err == nil {
		w, h = cfg.Width, cfg.Height
	}
	var b bytes.Buffer
	put := func(v uint32) { _ = binary.Write(&b, binary.BigEndian, v) }
	put(3) // capa frontal
	put(uint32(len(mime)))
	b.WriteString(mime)
	put(0) // sem descrição
	put(uint32(w))
	put(uint32(h))
	put(24)
	put(0)
	put(uint32(len(img)))
	b.Write(img)
	return base64.StdEncoding.EncodeToString(b.Bytes())
}
