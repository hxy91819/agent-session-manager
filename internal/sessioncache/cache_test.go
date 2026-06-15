package sessioncache

import (
	"path/filepath"
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
