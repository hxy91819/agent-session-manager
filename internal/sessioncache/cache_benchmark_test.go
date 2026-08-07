package sessioncache

import (
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
		if len(cache.Entries) != 2000 {
			b.Fatalf("entries = %d", len(cache.Entries))
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
	if err := os.WriteFile(path, []byte(strings.Repeat("{", 1024*1024)), 0o644); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if cache := Load(path); len(cache.Entries) != 0 {
			b.Fatal("corrupt cache was not empty")
		}
	}
}

func BenchmarkLegacyCacheLoad(b *testing.B) {
	// Before sharding, the current single-file format is the legacy migration input.
	path, _ := makeBenchmarkCache(b, 2000)
	b.ReportAllocs()
	for b.Loop() {
		if cache := Load(path); len(cache.Entries) != 2000 {
			b.Fatal("legacy cache load failed")
		}
	}
}

func makeBenchmarkCache(b *testing.B, count int) (string, []FileIdentity) {
	b.Helper()
	path := filepath.Join(b.TempDir(), "cache.json")
	cache := Cache{Version: Version, Entries: make(map[string]Entry, count)}
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
	if err := cache.Save(path); err != nil {
		b.Fatal(err)
	}
	return path, ids
}
