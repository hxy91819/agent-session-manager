package claude

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hxy91819/agent-session-manager/internal/session"
)

func BenchmarkDiscoverColdCache(b *testing.B) {
	home, cachePath := makeBenchmarkClaudeStore(b)
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
	home, cachePath := makeBenchmarkClaudeStore(b)
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

func makeBenchmarkClaudeStore(b *testing.B) (string, string) {
	b.Helper()
	home := b.TempDir()
	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		b.Fatal(err)
	}
	projectDir := filepath.Join(home, "projects", "-repo")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		b.Fatal(err)
	}
	for i := range 80 {
		path := filepath.Join(projectDir, fmt.Sprintf("session-%03d.jsonl", i))
		if err := os.WriteFile(path, []byte(benchmarkClaudeSession(i, repo)), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	return home, filepath.Join(b.TempDir(), "claude-cache.json")
}

func benchmarkClaudeSession(i int, repo string) string {
	var b strings.Builder
	for j := range 80 {
		fmt.Fprintf(&b, `{"type":"user","sessionId":"sid-%03d","cwd":%q,"timestamp":"2026-06-15T01:00:00Z","message":{"role":"user","content":"session %03d prompt %03d with enough text to make JSONL parsing visible in benchmarks"}}`+"\n", i, repo, i, j)
		fmt.Fprintf(&b, `{"type":"assistant","sessionId":"sid-%03d","cwd":%q,"timestamp":"2026-06-15T01:00:01Z","message":{"role":"assistant","model":"claude-sonnet-4","content":[]}}`+"\n", i, repo)
	}
	fmt.Fprintf(&b, `{"type":"summary","sessionId":"sid-%03d","summary":"Native Claude Title %03d"}`+"\n", i, i)
	return b.String()
}
