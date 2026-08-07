package codebuddy

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hxy91819/agent-session-manager/internal/session"
)

func BenchmarkDiscoverColdCache(b *testing.B) {
	home, cachePath := makeBenchmarkStore(b)
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

func BenchmarkDiscoverHistoryHeavyWarmCache(b *testing.B) {
	home := b.TempDir()
	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		b.Fatal(err)
	}
	now := time.Now()
	for i := range 2000 {
		path := filepath.Join(home, "projects", "repo", fmt.Sprintf("session-%04d.jsonl", i))
		body := fmt.Sprintf(`{"sessionId":"session-%04d","cwd":%q,"timestamp":"2026-06-13T01:00:00Z","ai-title":"benchmark title %04d"}`+"\n", i, repo, i)
		writeBenchmarkFile(b, path, body)
		modTime := now.AddDate(0, 0, -60)
		if i < 10 {
			modTime = now.Add(-time.Duration(i+1) * time.Minute)
		}
		if err := os.Chtimes(path, modTime, modTime); err != nil {
			b.Fatal(err)
		}
	}
	provider := Provider{Home: home, CachePath: filepath.Join(b.TempDir(), "cache.json")}
	if _, err := provider.Discover(session.DiscoverOptions{}); err != nil {
		b.Fatal(err)
	}
	opts := session.DiscoverOptions{Since: now.AddDate(0, 0, -30)}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		got, err := provider.Discover(opts)
		if err != nil || len(got) != 10 {
			b.Fatalf("sessions=%d err=%v", len(got), err)
		}
	}
}

func BenchmarkDiscoverHotCache(b *testing.B) {
	home, cachePath := makeBenchmarkStore(b)
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

func makeBenchmarkStore(b *testing.B) (string, string) {
	b.Helper()
	home := b.TempDir()
	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		b.Fatal(err)
	}
	for i := range 200 {
		body := fmt.Sprintf(`{"sessionId":"session-%03d","cwd":%q,"timestamp":"2026-06-13T01:00:00Z","ai-title":"benchmark title %03d","model":"codebuddy"}`+"\n", i, repo, i)
		writeBenchmarkFile(b, filepath.Join(home, "projects", "repo", fmt.Sprintf("session-%03d.jsonl", i)), body)
	}
	return home, filepath.Join(b.TempDir(), "codebuddy-cache.json")
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
