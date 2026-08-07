package sessioncache

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hxy91819/agent-session-manager/internal/session"
)

func BenchmarkHistoryHeavyLoad(b *testing.B) {
	path, _ := makeBenchmarkCache(b, 2000)
	b.ReportAllocs()
	for b.Loop() {
		cache := Load(path)
		if !cache.useShards {
			b.Fatal("sharded cache manifest was not loaded")
		}
	}
}

func BenchmarkActiveIdentityLookup(b *testing.B) {
	path, ids := makeBenchmarkCache(b, 2000)
	cache := Load(path)
	active := ids[:10]
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		for _, id := range active {
			if _, ok := cache.Get(id); !ok {
				b.Fatal("cache miss")
			}
		}
	}
}

func BenchmarkSingleEntryUpdateSave(b *testing.B) {
	path, ids := makeBenchmarkCache(b, 2000)
	cache := Load(path)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		cache.Put(ids[0], session.Session{ID: "updated", Title: "updated title"})
		if err := cache.Save(path); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCorruptCacheFallback(b *testing.B) {
	path := filepath.Join(b.TempDir(), "cache.json")
	id := FileIdentity{Provider: "codex", Path: "/corrupt.jsonl", Size: 10, ModTime: time.Now()}
	if err := os.WriteFile(path, []byte(strings.Repeat("{", 1024*1024)), 0o644); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, ok := Load(path).Get(id); ok {
			b.Fatal("corrupt cache unexpectedly hit")
		}
	}
}

func BenchmarkLegacyCacheLoad(b *testing.B) {
	path, ids := makeBenchmarkLegacyCache(b, 2000)
	b.ReportAllocs()
	for b.Loop() {
		if _, ok := Load(path).Get(ids[0]); !ok {
			b.Fatal("legacy cache load failed")
		}
	}
}

func BenchmarkShardCounts(b *testing.B) {
	for _, count := range []int{16, 32, 64} {
		path, ids := makeBenchmarkCacheWithShardCount(b, 2000, count)
		b.Run(fmt.Sprintf("%d/ActiveLoad", count), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				cache := Load(path)
				for _, id := range ids[:10] {
					if _, ok := cache.Get(id); !ok {
						b.Fatal("active shard cache miss")
					}
				}
			}
		})
		b.Run(fmt.Sprintf("%d/SingleEntryUpdate", count), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				cache := Load(path)
				cache.Put(ids[0], session.Session{ID: "updated"})
				if err := cache.Save(path); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func makeBenchmarkCache(b *testing.B, count int) (string, []FileIdentity) {
	return makeBenchmarkCacheWithShardCount(b, count, defaultShardCount)
}

func makeBenchmarkCacheWithShardCount(b *testing.B, count, shardCount int) (string, []FileIdentity) {
	b.Helper()
	path := filepath.Join(b.TempDir(), "cache.json")
	cache := Cache{Version: Version, Entries: make(map[string]Entry, count), shardCount: shardCount}
	ids := populateBenchmarkCache(&cache, count)
	if err := cache.Save(path); err != nil {
		b.Fatal(err)
	}
	return path, ids
}

func makeBenchmarkLegacyCache(b *testing.B, count int) (string, []FileIdentity) {
	b.Helper()
	path := filepath.Join(b.TempDir(), "cache.json")
	cache := Cache{Version: Version, Entries: make(map[string]Entry, count)}
	ids := populateBenchmarkCache(&cache, count)
	data, err := json.Marshal(shardFile{Version: Version, Entries: cache.Entries})
	if err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		b.Fatal(err)
	}
	return path, ids
}

func populateBenchmarkCache(cache *Cache, count int) []FileIdentity {
	ids := make([]FileIdentity, 0, count)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < count; i++ {
		id := FileIdentity{
			Provider: "codex",
			Path:     fmt.Sprintf("/sessions/%04d.jsonl", i),
			Size:     int64(1024 + i),
			ModTime:  base.Add(time.Duration(i) * time.Second),
		}
		cache.Put(id, session.Session{
			ID:       fmt.Sprintf("session-%04d", i),
			Provider: "codex",
			CWD:      fmt.Sprintf("/repo/%04d", i%20),
			Title:    strings.Repeat("benchmark title ", 8),
			Metadata: map[string]string{"title_source": "history", "model": "gpt-5"},
		})
		ids = append(ids, id)
	}
	return ids
}
