package openclaw

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hxy91819/agent-session-manager/internal/session"
)

func BenchmarkDiscover(b *testing.B) {
	stateDir := makeBenchmarkStore(b)
	provider := New(stateDir)

	b.ReportAllocs()
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

func makeBenchmarkStore(b *testing.B) string {
	b.Helper()
	stateDir := b.TempDir()
	repo := filepath.Join(stateDir, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		b.Fatal(err)
	}
	for agent := range 5 {
		var body strings.Builder
		body.WriteString("{")
		for i := range 40 {
			if i > 0 {
				body.WriteString(",")
			}
			key := fmt.Sprintf("agent:agent-%d:session-%03d", agent, i)
			fmt.Fprintf(&body, "%q:{%q:%q,%q:1781312460000,%q:%q,%q:%q}",
				key, "sessionId", fmt.Sprintf("native-%d-%03d", agent, i), "updatedAt", "spawnedCwd", repo, "displayName", key)
		}
		body.WriteString("}")
		writeBenchmarkFile(b, filepath.Join(stateDir, "agents", fmt.Sprintf("agent-%d", agent), "sessions", "sessions.json"), body.String())
	}
	return stateDir
}

func writeBenchmarkFile(b *testing.B, path, content string) {
	b.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		b.Fatal(err)
	}
}
