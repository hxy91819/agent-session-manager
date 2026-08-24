package pi

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hxy91819/agent-session-manager/internal/session"
)

func BenchmarkDiscoverColdCache(b *testing.B) {
	home, cachePath := makeBenchmarkPiStore(b)
	provider := Provider{Home: home, CachePath: cachePath}

	b.ReportAllocs()
	for b.Loop() {
		if err := os.RemoveAll(filepath.Join(cachePath + ".d")); err != nil {
			b.Fatal(err)
		}
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
	home, cachePath := makeBenchmarkPiStore(b)
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

func makeBenchmarkPiStore(b *testing.B) (string, string) {
	b.Helper()
	home := b.TempDir()
	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		b.Fatal(err)
	}
	for i := range 40 {
		id := fmt.Sprintf("ses-%03d", i)
		dir := filepath.Join(home, "sessions", "--"+id+"--")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			b.Fatal(err)
		}
		lines := []string{
			piHeader(id, repo, "2026-06-15T01:00:00.000Z", ""),
			piUserMessage("a1", "2026-06-15T01:00:01.000Z", 1781312401000, fmt.Sprintf("benchmark task %03d", i)),
			piAssistantMessage("a2", "2026-06-15T01:00:30.000Z", 1781312430000, "done"),
		}
		path := filepath.Join(dir, "2026-06-15T01-00-00-000Z_"+id+".jsonl")
		if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	return home, filepath.Join(b.TempDir(), "pi-cache.json")
}
