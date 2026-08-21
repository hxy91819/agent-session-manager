package dsh

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/hxy91819/agent-session-manager/internal/session"
)

func BenchmarkDiscoverColdCache(b *testing.B) {
	home, cachePath := makeBenchmarkDshStore(b)
	provider := Provider{Home: home, CachePath: cachePath}

	b.ReportAllocs()
	for b.Loop() {
		if err := os.Remove(cachePath); err != nil && !os.IsNotExist(err) {
			b.Fatal(err)
		}
		got, err := provider.Discover(session.DiscoverOptions{})
		if err != nil {
			b.Fatal(err)
		}
		if len(got) == 0 {
			b.Fatal("expected sessions")
		}
	}
}

func BenchmarkDiscoverHotCache(b *testing.B) {
	home, cachePath := makeBenchmarkDshStore(b)
	provider := Provider{Home: home, CachePath: cachePath}
	if _, err := provider.Discover(session.DiscoverOptions{}); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		got, err := provider.Discover(session.DiscoverOptions{})
		if err != nil {
			b.Fatal(err)
		}
		if len(got) == 0 {
			b.Fatal("expected sessions")
		}
	}
}

func makeBenchmarkDshStore(b *testing.B) (string, string) {
	b.Helper()
	home := b.TempDir()
	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		b.Fatal(err)
	}
	for i := range 40 {
		id := fmt.Sprintf("session-%03d", i)
		lines := []string{header(id, repo, 1781312400000+int64(i)*1000)}
		for j := range 8 {
			lines = append(lines,
				fmt.Sprintf(`{"type":"user/message","seq":%d,"time":%d,"data":{"id":"m%d","role":"user","content":[{"type":"text","text":"bench prompt %03d turn %02d with enough text to make zstd decoding visible"}],"source":{"kind":"user"}}}`,
					j+100, 1781312400000+int64(i)*1000+int64(j), j, i, j))
		}
		writeZstdLog(b, home, "--bench-proj--", id, lines)
	}
	return home, filepath.Join(b.TempDir(), "cache.json")
}
