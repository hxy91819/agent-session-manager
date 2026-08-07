package sessioncache

import (
	"encoding/json"
	"errors"
	"fmt"
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

func TestCacheRoundTripsOpaqueStateForLatestPathEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	modTime := time.Date(2026, 6, 15, 1, 0, 0, 123, time.UTC)
	id := FileIdentity{Provider: "codex", Path: "/tmp/session.jsonl", Size: 42, ModTime: modTime}
	cache := Cache{Version: Version, Entries: make(map[string]Entry)}
	cache.PutWithState(id, session.Session{ID: "sid"}, []byte(`{"offset":42}`))
	if err := cache.Save(path); err != nil {
		t.Fatal(err)
	}

	latestID, got, state, ok := Load(path).GetLatest(id.Provider, id.Path)
	if !ok || got.ID != "sid" || latestID.Size != id.Size || !latestID.ModTime.Equal(modTime) {
		t.Fatalf("latest entry = %#v %#v, %v", latestID, got, ok)
	}
	if string(state) != `{"offset":42}` {
		t.Fatalf("state = %q", state)
	}
	state[0] = 'x'
	_, _, state, ok = Load(path).GetLatest(id.Provider, id.Path)
	if !ok || string(state) != `{"offset":42}` {
		t.Fatalf("persisted state was mutated: %q", state)
	}
}

func TestSkipLoadForEmptyDiscoveryOnlySkipsBoundedEmptyScans(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name      string
		opts      session.DiscoverOptions
		fileCount int
		want      bool
	}{
		{name: "since bounded empty", opts: session.DiscoverOptions{Since: now}, want: true},
		{name: "limit bounded empty", opts: session.DiscoverOptions{LimitFiles: 1}, want: true},
		{name: "unbounded empty", want: false},
		{name: "bounded with active file", opts: session.DiscoverOptions{Since: now, LimitFiles: 1}, fileCount: 1, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SkipLoadForEmptyDiscovery(tt.opts, tt.fileCount); got != tt.want {
				t.Fatalf("SkipLoadForEmptyDiscovery() = %v, want %v", got, tt.want)
			}
		})
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
	id := FileIdentity{Provider: "codex", Path: "/tmp/stale.jsonl", Size: 1, ModTime: time.Now()}
	t.Run("invalid JSON", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "cache.json")
		if err := os.WriteFile(path, []byte("{"), 0o644); err != nil {
			t.Fatal(err)
		}
		loaded := Load(path)
		if _, ok := loaded.Get(id); ok || loaded.Version != Version {
			t.Fatalf("loaded invalid cache = %#v", loaded)
		}
	})

	t.Run("wrong version", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "cache.json")
		if err := os.WriteFile(path, []byte(`{"version":4,"entries":{"stale":{}}}`), 0o644); err != nil {
			t.Fatal(err)
		}
		loaded := Load(path)
		if _, ok := loaded.Get(id); ok || loaded.Version != Version {
			t.Fatalf("loaded version-mismatched cache = %#v", loaded)
		}
	})
}

func TestShardedSavePreservesLegacyAndRoundTripsLargeUnicodeEntry(t *testing.T) {
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

	legacy, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(legacy), strings.Repeat("x", 32)) {
		t.Fatal("migration modified the legacy cache before a valid replacement existed")
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
	stored := Load(path)
	shard := stored.shardForKey(Key(id.Provider, id.Path))
	storedPath := shardPath(path, stored.generation, shard)
	before, err := os.Stat(storedPath)
	if err != nil {
		t.Fatal(err)
	}
	future := before.ModTime().Add(time.Hour)
	if err := os.Chtimes(storedPath, future, future); err != nil {
		t.Fatal(err)
	}
	if err := cache.Save(path); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(storedPath)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(future) {
		t.Fatalf("unchanged save rewrote cache: mtime = %v, want %v", after.ModTime(), future)
	}
}

func TestKeepPrunesAcrossShards(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	first, second := identitiesInDifferentShards(t)
	writeShardedTestCache(t, path, first, second)

	cache := Load(path)
	cache.Keep(map[string]struct{}{Key(first.Provider, first.Path): {}})
	if err := cache.Save(path); err != nil {
		t.Fatal(err)
	}

	loaded := Load(path)
	if _, ok := loaded.Get(first); !ok {
		t.Fatal("kept entry missed after cross-shard prune")
	}
	if _, ok := loaded.Get(second); ok {
		t.Fatal("dropped entry survived cross-shard prune")
	}
}

func TestOnlyDirtyShardIsRewritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	first, second := identitiesInDifferentShards(t)
	writeShardedTestCache(t, path, first, second)
	cache := Load(path)
	firstShard := cache.shardForKey(Key(first.Provider, first.Path))
	secondShard := cache.shardForKey(Key(second.Provider, second.Path))
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(shardPath(path, cache.generation, firstShard), future, future); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(shardPath(path, cache.generation, secondShard), future, future); err != nil {
		t.Fatal(err)
	}

	cache.Put(first, session.Session{ID: "updated"})
	if err := cache.Save(path); err != nil {
		t.Fatal(err)
	}
	firstInfo, err := os.Stat(shardPath(path, cache.generation, firstShard))
	if err != nil {
		t.Fatal(err)
	}
	secondInfo, err := os.Stat(shardPath(path, cache.generation, secondShard))
	if err != nil {
		t.Fatal(err)
	}
	if firstInfo.ModTime().Equal(future) {
		t.Fatal("dirty shard was not rewritten")
	}
	if !secondInfo.ModTime().Equal(future) {
		t.Fatal("clean shard was rewritten")
	}
}

func TestCorruptShardOnlyMissesItsEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	first, second := identitiesInDifferentShards(t)
	writeShardedTestCache(t, path, first, second)
	cache := Load(path)
	secondShard := cache.shardForKey(Key(second.Provider, second.Path))
	if err := os.WriteFile(shardPath(path, cache.generation, secondShard), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}

	loaded := Load(path)
	if _, ok := loaded.Get(first); !ok {
		t.Fatal("healthy shard missed after sibling corruption")
	}
	if _, ok := loaded.Get(second); ok {
		t.Fatal("corrupt shard unexpectedly hit")
	}
}

func TestLegacyCacheMigratesOnceWithoutDeletingSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	id := FileIdentity{Provider: "codex", Path: "/legacy.jsonl", Size: 10, ModTime: time.Now()}
	writeLegacyTestCache(t, path, id)
	legacyBefore, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	cache := Load(path)
	if _, ok := cache.Get(id); !ok {
		t.Fatal("legacy cache did not provide a read-compatible hit")
	}
	if err := cache.Save(path); err != nil {
		t.Fatal(err)
	}
	legacyAfter, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("legacy cache was deleted after migration")
	}
	if string(legacyAfter) != string(legacyBefore) {
		t.Fatal("legacy cache changed during migration")
	}
	if _, ok := Load(path).Get(id); !ok {
		t.Fatal("migrated shard did not preserve the session")
	}

	manifestInfo, err := os.Stat(manifestPath(path))
	if err != nil {
		t.Fatal(err)
	}
	future := manifestInfo.ModTime().Add(time.Hour)
	if err := os.Chtimes(manifestPath(path), future, future); err != nil {
		t.Fatal(err)
	}
	reloaded := Load(path)
	if _, ok := reloaded.Get(id); !ok {
		t.Fatal("repeat migration load missed")
	}
	if err := reloaded.Save(path); err != nil {
		t.Fatal(err)
	}
	manifestAfter, err := os.Stat(manifestPath(path))
	if err != nil {
		t.Fatal(err)
	}
	if !manifestAfter.ModTime().Equal(future) {
		t.Fatal("repeat migration rewrote the manifest")
	}
}

func TestMigrationFailurePreservesReadableLegacyCache(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	id := FileIdentity{Provider: "codex", Path: "/legacy.jsonl", Size: 10, ModTime: time.Now()}
	writeLegacyTestCache(t, path, id)
	legacyBefore, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(shardDir(path), []byte("blocks shard directory"), 0o644); err != nil {
		t.Fatal(err)
	}

	cache := Load(path)
	if _, ok := cache.Get(id); !ok {
		t.Fatal("legacy cache missed before failed migration")
	}
	if err := cache.Save(path); err == nil {
		t.Fatal("migration unexpectedly succeeded")
	}
	legacyAfter, err := os.ReadFile(path)
	if err != nil || string(legacyAfter) != string(legacyBefore) {
		t.Fatalf("failed migration changed legacy cache: err=%v", err)
	}
	if _, ok := Load(path).Get(id); !ok {
		t.Fatal("legacy cache was unreadable after failed migration")
	}
}

func TestAtomicShardWriteFailureKeepsLastValidEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	id := FileIdentity{Provider: "codex", Path: "/atomic.jsonl", Size: 10, ModTime: time.Now()}
	writeShardedTestCache(t, path, id)
	cache := Load(path)
	cache.Put(id, session.Session{ID: "replacement"})
	cache.renameFile = func(string, string) error { return errors.New("injected rename failure") }
	if err := cache.Save(path); err == nil {
		t.Fatal("save unexpectedly succeeded")
	}
	got, ok := Load(path).Get(id)
	if !ok || got.ID != id.Path {
		t.Fatalf("last valid entry = %#v, hit=%v", got, ok)
	}
}

func TestSamePathFromDifferentProvidersDoesNotCollide(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	modTime := time.Now()
	codexID := FileIdentity{Provider: "codex", Path: "/same.jsonl", Size: 10, ModTime: modTime}
	claudeID := FileIdentity{Provider: "claude", Path: "/same.jsonl", Size: 10, ModTime: modTime}
	writeShardedTestCache(t, path, codexID, claudeID)
	loaded := Load(path)
	for _, id := range []FileIdentity{codexID, claudeID} {
		got, ok := loaded.Get(id)
		if !ok || got.ID != id.Path {
			t.Fatalf("provider %q entry = %#v, hit=%v", id.Provider, got, ok)
		}
	}
}

func TestAdaptiveShardLayoutKeepsSmallCachesInline(t *testing.T) {
	tests := []struct {
		name       string
		entries    int
		wantInline bool
		wantShards int
	}{
		{name: "small inline", entries: 128, wantInline: true, wantShards: 1},
		{name: "medium sixteen", entries: 129, wantShards: 16},
		{name: "large sixty four", entries: 1025, wantShards: 64},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "cache.json")
			cache := Load(path)
			for i := 0; i < tt.entries; i++ {
				id := FileIdentity{Provider: "codex", Path: fmt.Sprintf("/%04d.jsonl", i), Size: int64(i), ModTime: time.Unix(int64(i), 0)}
				cache.Put(id, session.Session{ID: id.Path})
			}
			if err := cache.Save(path); err != nil {
				t.Fatal(err)
			}
			loaded := Load(path)
			if loaded.inline != tt.wantInline || loaded.shardCount != tt.wantShards {
				t.Fatalf("layout inline=%v shards=%d, want inline=%v shards=%d", loaded.inline, loaded.shardCount, tt.wantInline, tt.wantShards)
			}
		})
	}
}

func identitiesInDifferentShards(t *testing.T) (FileIdentity, FileIdentity) {
	t.Helper()
	cache := loadWithShardCount("", defaultShardCount)
	first := FileIdentity{Provider: "codex", Path: "/first.jsonl", Size: 10, ModTime: time.Now()}
	firstShard := cache.shardForKey(Key(first.Provider, first.Path))
	for i := 0; i < 1000; i++ {
		second := FileIdentity{Provider: "codex", Path: fmt.Sprintf("/second-%d.jsonl", i), Size: 20, ModTime: first.ModTime}
		if cache.shardForKey(Key(second.Provider, second.Path)) != firstShard {
			return first, second
		}
	}
	t.Fatal("could not find identities in different shards")
	return FileIdentity{}, FileIdentity{}
}

func writeShardedTestCache(t *testing.T, path string, ids ...FileIdentity) {
	t.Helper()
	cache := Cache{Version: Version, Entries: make(map[string]Entry)}
	for _, id := range ids {
		cache.Put(id, session.Session{ID: id.Path, Provider: id.Provider})
	}
	if err := cache.Save(path); err != nil {
		t.Fatal(err)
	}
}

func writeLegacyTestCache(t *testing.T, path string, ids ...FileIdentity) {
	t.Helper()
	entries := make(map[string]Entry, len(ids))
	for _, id := range ids {
		entries[Key(id.Provider, id.Path)] = Entry{
			Provider: id.Provider, Path: id.Path, Size: id.Size,
			ModTimeUnixNano: id.ModTime.UnixNano(),
			Session:         session.Session{ID: id.Path, Provider: id.Provider},
		}
	}
	data, err := json.Marshal(shardFile{Version: Version, Entries: entries})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}
