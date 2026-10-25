package spotify

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// diskCache guarda em disco os metadados de faixas e álbuns (que quase nunca
// mudam), para que baixar a mesma playlist de novo só consulte o que é novo.
// As listas de playlist não entram aqui: elas mudam.
type diskCache struct {
	dir string
	ttl time.Duration
}

const cacheTTL = 30 * 24 * time.Hour

// cacheVersion muda quando o formato gravado muda, invalidando o cache antigo.
const cacheVersion = "v1"

func newDiskCache(dir string) *diskCache {
	if dir == "" {
		return nil
	}
	dir = filepath.Join(dir, cacheVersion)
	if os.MkdirAll(dir, 0o755) != nil {
		return nil
	}
	return &diskCache{dir: dir, ttl: cacheTTL}
}

func (c *diskCache) path(key string) string { return filepath.Join(c.dir, key+".json") }

func (c *diskCache) get(key string, v any) bool {
	if c == nil {
		return false
	}
	p := c.path(key)
	st, err := os.Stat(p)
	if err != nil || time.Since(st.ModTime()) > c.ttl {
		return false
	}
	b, err := os.ReadFile(p)
	return err == nil && json.Unmarshal(b, v) == nil
}

func (c *diskCache) put(key string, v any) {
	if c == nil {
		return
	}
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	// Grava num temporário e renomeia: duas execuções ao mesmo tempo não
	// deixam um arquivo pela metade.
	tmp, err := os.CreateTemp(c.dir, ".tmp-*")
	if err != nil {
		return
	}
	_, werr := tmp.Write(b)
	cerr := tmp.Close()
	if werr != nil || cerr != nil || os.Rename(tmp.Name(), c.path(key)) != nil {
		os.Remove(tmp.Name())
	}
}
