package sessioncache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/hxy91819/agent-session-manager/internal/session"
)

const Version = 1

type FileIdentity struct {
	Provider string
	Path     string
	Size     int64
	ModTime  time.Time
}

type Cache struct {
	Version int              `json:"version"`
	Entries map[string]Entry `json:"entries"`
	dirty   bool
}

type Entry struct {
	Provider        string          `json:"provider"`
	Path            string          `json:"path"`
	Size            int64           `json:"size"`
	ModTimeUnixNano int64           `json:"mod_time_unix_nano"`
	Session         session.Session `json:"session"`
}

func DefaultPath(provider string) (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "agent-session-manager", provider+"-sessions.json"), nil
}

func Load(path string) Cache {
	cache := Cache{Version: Version, Entries: make(map[string]Entry)}
	if path == "" {
		return cache
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return cache
	}
	if json.Unmarshal(data, &cache) != nil || cache.Version != Version {
		return Cache{Version: Version, Entries: make(map[string]Entry)}
	}
	if cache.Entries == nil {
		cache.Entries = make(map[string]Entry)
	}
	return cache
}

func (c Cache) Get(id FileIdentity) (session.Session, bool) {
	entry, ok := c.Entries[Key(id.Provider, id.Path)]
	if !ok || entry.Provider != id.Provider || entry.Path != id.Path || entry.Size != id.Size {
		return session.Session{}, false
	}
	if entry.ModTimeUnixNano != id.ModTime.UnixNano() {
		return session.Session{}, false
	}
	return cloneSession(entry.Session), true
}

func (c *Cache) Put(id FileIdentity, s session.Session) {
	if c.Entries == nil {
		c.Entries = make(map[string]Entry)
	}
	c.Entries[Key(id.Provider, id.Path)] = Entry{
		Provider:        id.Provider,
		Path:            id.Path,
		Size:            id.Size,
		ModTimeUnixNano: id.ModTime.UnixNano(),
		Session:         cloneSession(s),
	}
	c.dirty = true
}

func (c *Cache) Keep(keys map[string]struct{}) {
	for key := range c.Entries {
		if _, ok := keys[key]; !ok {
			delete(c.Entries, key)
			c.dirty = true
		}
	}
}

func (c *Cache) Save(path string) error {
	if path == "" || !c.dirty {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	encErr := json.NewEncoder(f).Encode(c)
	closeErr := f.Close()
	if encErr != nil {
		_ = os.Remove(tmp)
		return encErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	c.dirty = false
	return nil
}

func Key(provider, path string) string {
	return provider + "\x00" + path
}

func cloneSession(s session.Session) session.Session {
	if s.Metadata == nil {
		return s
	}
	metadata := make(map[string]string, len(s.Metadata))
	for key, value := range s.Metadata {
		metadata[key] = value
	}
	s.Metadata = metadata
	return s
}
