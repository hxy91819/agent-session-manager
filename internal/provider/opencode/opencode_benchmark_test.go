package opencode

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/hxy91819/agent-session-manager/internal/session"
)

func BenchmarkDiscoverColdCache(b *testing.B) {
	home, cachePath := makeBenchmarkOpencodeStore(b)
	provider := Provider{Home: home, CachePath: cachePath}
	opts := session.DiscoverOptions{}

	b.ReportAllocs()
	for b.Loop() {
		if err := os.Remove(cachePath); err != nil && !os.IsNotExist(err) {
			b.Fatal(err)
		}
		got, err := provider.Discover(opts)
		if err != nil {
			b.Fatal(err)
		}
		if len(got) == 0 {
			b.Fatal("expected sessions")
		}
	}
}

func BenchmarkDiscoverHotCache(b *testing.B) {
	home, cachePath := makeBenchmarkOpencodeStore(b)
	provider := Provider{Home: home, CachePath: cachePath}
	opts := session.DiscoverOptions{}
	if _, err := provider.Discover(opts); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		got, err := provider.Discover(opts)
		if err != nil {
			b.Fatal(err)
		}
		if len(got) == 0 {
			b.Fatal("expected sessions")
		}
	}
}

func makeBenchmarkOpencodeStore(b *testing.B) (string, string) {
	b.Helper()
	home := b.TempDir()
	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		b.Fatal(err)
	}
	for i := range 200 {
		projectID := fmt.Sprintf("project-%03d", i%10)
		sessionID := fmt.Sprintf("session-%03d", i)
		writeBenchmarkOpencodeSession(b, home, projectID, sessionID, repo)
	}
	return home, filepath.Join(b.TempDir(), "opencode-cache.json")
}

func writeBenchmarkOpencodeSession(b *testing.B, home, projectID, sessionID, cwd string) {
	b.Helper()
	sessionDir := filepath.Join(home, "storage", "session", projectID)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		b.Fatal(err)
	}
	body := fmt.Sprintf(`{
  "id": %q,
  "version": "1.1.11",
  "projectID": %q,
  "directory": %q,
  "title": "benchmark opencode title %s",
  "time": {"created": 1781312400000, "updated": 1781312460000}
}`, sessionID, projectID, cwd, sessionID)
	if err := os.WriteFile(filepath.Join(sessionDir, sessionID+".json"), []byte(body), 0o644); err != nil {
		b.Fatal(err)
	}
}
