package kiro

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/hxy91819/agent-session-manager/internal/session"
)

func BenchmarkDiscoverColdCache(b *testing.B) {
	home, cachePath := makeBenchmarkKiroStore(b)
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
	home, cachePath := makeBenchmarkKiroStore(b)
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

func makeBenchmarkKiroStore(b *testing.B) (string, string) {
	b.Helper()
	home := b.TempDir()
	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		b.Fatal(err)
	}
	sessionsDir := filepath.Join(home, "sessions", "cli")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		b.Fatal(err)
	}
	for i := range 40 {
		id := fmt.Sprintf("ses-%03d", i)
		metadata := fmt.Sprintf(`{"session_id":%q,"cwd":%q,"created_at":"2026-06-15T01:00:00Z","updated_at":"2026-06-15T01:10:00Z","title":"Kiro session %03d","session_created_reason":"user"}
`, id, repo, i)
		if err := os.WriteFile(filepath.Join(sessionsDir, id+".json"), []byte(metadata), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	return home, filepath.Join(b.TempDir(), "kiro-cache.json")
}
