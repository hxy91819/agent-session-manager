package opencode

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

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

func BenchmarkDiscoverDBCold(b *testing.B) {
	home := makeBenchmarkOpencodeDBStore(b)
	provider := New(home)
	opts := session.DiscoverOptions{}

	b.ReportAllocs()
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

func BenchmarkDiscoverDBWithPreviews(b *testing.B) {
	home := makeBenchmarkOpencodeDBStore(b)
	provider := New(home)
	opts := session.DiscoverOptions{
		Preview: session.PreviewOptions{UserMessagesPerEdge: 2, MaxChars: 500},
	}

	b.ReportAllocs()
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

func makeBenchmarkOpencodeDBStore(b *testing.B) string {
	b.Helper()
	home := b.TempDir()
	db := createOpencodeDB(b, home)
	defer closeDB(b, db)

	base := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	for i := range 40 {
		sessionID := fmt.Sprintf("ses_bench_%03d", i)
		writeOpencodeDBSession(b, db, opencodeDBSessionFixture{
			ID:        sessionID,
			Directory: "/repo/bench",
			Title:     fmt.Sprintf("bench session %03d", i),
			Version:   "1.18.19",
			CreatedAt: base.Add(time.Duration(i) * time.Minute),
			UpdatedAt: base.Add(time.Duration(i) * time.Minute),
		})
		for j := range 8 {
			writeOpencodeDBMessage(b, db, sessionID, fmt.Sprintf("msg_%03d_%02d", i, j), "user",
				fmt.Sprintf("bench prompt %03d turn %02d with enough text to make sqlite parsing visible", i, j),
				base.Add(time.Duration(i)*time.Minute+time.Duration(j)*time.Second))
		}
	}
	return home
}
