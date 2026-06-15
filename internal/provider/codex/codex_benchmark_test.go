package codex

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hxy91819/agent-session-manager/internal/session"
)

func BenchmarkDiscoverColdCache(b *testing.B) {
	home, cachePath := makeBenchmarkCodexStore(b)
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
	home, cachePath := makeBenchmarkCodexStore(b)
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

func makeBenchmarkCodexStore(b *testing.B) (string, string) {
	b.Helper()
	home := b.TempDir()
	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		b.Fatal(err)
	}
	sessionDir := filepath.Join(home, "sessions", "2026", "06", "15")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		b.Fatal(err)
	}
	for i := range 40 {
		path := filepath.Join(sessionDir, fmt.Sprintf("session-%03d.jsonl", i))
		if err := os.WriteFile(path, []byte(benchmarkCodexSession(i, repo)), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	return home, filepath.Join(b.TempDir(), "codex-cache.json")
}

func benchmarkCodexSession(i int, repo string) string {
	var b strings.Builder
	fmt.Fprintf(&b, `{"timestamp":"2026-06-15T01:00:00Z","type":"session_meta","payload":{"id":"sid-%03d","timestamp":"2026-06-15T01:00:00Z","cwd":%q}}`+"\n", i, repo)
	fmt.Fprintf(&b, `{"timestamp":"2026-06-15T01:00:01Z","type":"turn_context","payload":{"cwd":%q,"model":"gpt-5"}}`+"\n", repo)
	for j := range 120 {
		fmt.Fprintf(&b, `{"timestamp":"2026-06-15T01:01:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"session %03d prompt %03d with enough text to make JSONL parsing visible in benchmarks"}]}}`+"\n", i, j)
	}
	return b.String()
}
