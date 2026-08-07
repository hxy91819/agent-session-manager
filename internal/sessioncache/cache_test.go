package sessioncache

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hxy91819/agent-session-manager/internal/session"
)

func TestCacheHitsOnlyMatchingIdentity(t *testing.T) {
	modTime := time.Date(2026, 6, 15, 1, 0, 0, 0, time.UTC)
	id := FileIdentity{Provider: "codex", Path: "/tmp/session.jsonl", Size: 10, ModTime: modTime}
	cache := Cache{Version: Version, Entries: make(map[string]Entry)}
	cache.Put(id, session.Session{
		ID:       "sid",
		Provider: "codex",
		Metadata: map[string]string{"model": "gpt-5"},
	})

	got, ok := cache.Get(id)
	if !ok {
		t.Fatal("expected cache hit")
	}
	got.Metadata["model"] = "changed"
	got, ok = cache.Get(id)
	if !ok || got.Metadata["model"] != "gpt-5" {
		t.Fatalf("cache entry was mutated through returned session: %#v", got)
	}

	changed := id
	changed.Size++
	if _, ok := cache.Get(changed); ok {
		t.Fatal("cache hit after size changed")
	}
	changed = id
	changed.ModTime = changed.ModTime.Add(time.Nanosecond)
	if _, ok := cache.Get(changed); ok {
		t.Fatal("cache hit after mtime changed")
	}
	changed = id
	changed.Path += ".moved"
	if _, ok := cache.Get(changed); ok {
		t.Fatal("cache hit after path changed")
	}
	changed = id
	changed.Provider = "claude"
	if _, ok := cache.Get(changed); ok {
		t.Fatal("cache hit after provider changed")
	}
}

func TestCacheSaveLoadAndKeep(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	modTime := time.Date(2026, 6, 15, 1, 0, 0, 0, time.UTC)
	keepID := FileIdentity{Provider: "codex", Path: "/tmp/keep.jsonl", Size: 10, ModTime: modTime}
	dropID := FileIdentity{Provider: "codex", Path: "/tmp/drop.jsonl", Size: 20, ModTime: modTime}

	cache := Cache{Version: Version, Entries: make(map[string]Entry)}
	cache.Put(keepID, session.Session{ID: "keep"})
	cache.Put(dropID, session.Session{ID: "drop"})
	cache.Keep(map[string]struct{}{Key(keepID.Provider, keepID.Path): {}})
	if err := cache.Save(path); err != nil {
		t.Fatal(err)
	}

	loaded := Load(path)
	if _, ok := loaded.Get(dropID); ok {
		t.Fatal("dropped entry was loaded")
	}
	got, ok := loaded.Get(keepID)
	if !ok || got.ID != "keep" {
		t.Fatalf("loaded entry = %#v, %v", got, ok)
	}
}

func TestLoadTreatsInvalidJSONAndVersionAsEmpty(t *testing.T) {
	t.Run("invalid JSON", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "cache.json")
		if err := os.WriteFile(path, []byte("{"), 0o644); err != nil {
			t.Fatal(err)
		}
		loaded := Load(path)
		if loaded.Version != Version || len(loaded.Entries) != 0 {
			t.Fatalf("loaded invalid cache = %#v", loaded)
		}
	})

	t.Run("wrong version", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "cache.json")
		if err := os.WriteFile(path, []byte(`{"version":4,"entries":{"stale":{}}}`), 0o644); err != nil {
			t.Fatal(err)
		}
		loaded := Load(path)
		if loaded.Version != Version || len(loaded.Entries) != 0 {
			t.Fatalf("loaded version-mismatched cache = %#v", loaded)
		}
	})
}

func TestSaveReplacesOldContentAndRoundTripsLargeUnicodeEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 4096)), 0o644); err != nil {
		t.Fatal(err)
	}
	modTime := time.Date(2026, 6, 15, 1, 0, 0, 123, time.UTC)
	id := FileIdentity{Provider: "codex", Path: "/tmp/unicode.jsonl", Size: 42, ModTime: modTime}
	large := strings.Repeat("标题🙂", 4096)
	cache := Cache{Version: Version, Entries: make(map[string]Entry)}
	cache.Put(id, session.Session{
		ID:       "unicode",
		Provider: "codex",
		Title:    large,
		Metadata: map[string]string{"large": large},
	})
	if err := cache.Save(path); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), strings.Repeat("x", 32)) {
		t.Fatal("save left bytes from the old cache content")
	}
	got, ok := Load(path).Get(id)
	if !ok || got.Title != session.NormalizeTitle(large) || got.Metadata["large"] != large {
		t.Fatalf("round trip = %#v, hit=%v", got, ok)
	}
}

func TestSaveWithoutChangesDoesNotRewriteFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	id := FileIdentity{
		Provider: "codex",
		Path:     "/tmp/session.jsonl",
		Size:     10,
		ModTime:  time.Date(2026, 6, 15, 1, 0, 0, 0, time.UTC),
	}
	cache := Cache{Version: Version, Entries: make(map[string]Entry)}
	cache.Put(id, session.Session{ID: "sid"})
	if err := cache.Save(path); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	future := before.ModTime().Add(time.Hour)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}
	if err := cache.Save(path); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(future) {
		t.Fatalf("unchanged save rewrote cache: mtime = %v, want %v", after.ModTime(), future)
	}
}
