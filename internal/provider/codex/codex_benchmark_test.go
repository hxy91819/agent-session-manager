package codex

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hxy91819/agent-session-manager/internal/session"
)

func BenchmarkDiscoverChangedLargeSession(b *testing.B) {
	home := b.TempDir()
	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		b.Fatal(err)
	}
	path := filepath.Join(home, "sessions", "2026", "06", "15", "large.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		b.Fatal(err)
	}
	large, err := json.Marshal(strings.Repeat("x", 16*1024*1024))
	if err != nil {
		b.Fatal(err)
	}
	body := fmt.Sprintf(`{"timestamp":"2026-06-15T01:00:00Z","type":"session_meta","payload":{"id":"large","timestamp":"2026-06-15T01:00:00Z","cwd":%q}}`+"\n", repo) +
		`{"timestamp":"2026-06-15T01:00:01Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":` + string(large) + `}]}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		b.Fatal(err)
	}
	provider := Provider{Home: home, CachePath: filepath.Join(b.TempDir(), "cache.json")}
	if _, err := provider.Discover(session.DiscoverOptions{}); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		b.StopTimer()
		appendCodexBenchmarkRecord(b, path, i)
		b.StartTimer()
		got, err := provider.Discover(session.DiscoverOptions{})
		if err != nil || len(got) != 1 {
			b.Fatalf("sessions=%d err=%v", len(got), err)
		}
	}
}

func BenchmarkDiscoverObjectSourceWarmCache(b *testing.B) {
	home := b.TempDir()
	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		b.Fatal(err)
	}
	path := filepath.Join(home, "sessions", "2026", "06", "15", "subagent.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		b.Fatal(err)
	}
	var body strings.Builder
	fmt.Fprintf(&body, `{"timestamp":"2026-06-15T01:00:00Z","type":"session_meta","payload":{"id":"subagent","parent_thread_id":"parent","timestamp":"2026-06-15T01:00:00Z","cwd":%q,"source":{"subagent":{"thread_spawn":{"parent_thread_id":"parent","depth":1,"agent_role":"explorer"}}}}}`+"\n", repo)
	large := strings.Repeat("x", 128*1024)
	for range 128 {
		fmt.Fprintf(&body, `{"timestamp":"2026-06-15T01:00:01Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":%q}]}}`+"\n", large)
	}
	if err := os.WriteFile(path, []byte(body.String()), 0o644); err != nil {
		b.Fatal(err)
	}
	provider := Provider{Home: home, CachePath: filepath.Join(b.TempDir(), "cache.json")}
	initial, err := provider.Discover(session.DiscoverOptions{})
	if err != nil {
		b.Fatal(err)
	}
	b.ReportMetric(float64(len(initial)), "initial_sessions")

	lastCount := 0
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		got, err := provider.Discover(session.DiscoverOptions{})
		if err != nil {
			b.Fatal(err)
		}
		lastCount = len(got)
	}
	b.ReportMetric(float64(lastCount), "sessions/op")
}

func appendCodexBenchmarkRecord(b *testing.B, path string, iteration int) {
	b.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		b.Fatal(err)
	}
	_, writeErr := fmt.Fprintf(f, `{"timestamp":"2026-06-15T01:00:02Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"iteration %d"}]}}`+"\n", iteration)
	closeErr := f.Close()
	if writeErr != nil {
		b.Fatal(writeErr)
	}
	if closeErr != nil {
		b.Fatal(closeErr)
	}
}

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

func BenchmarkDiscoverColdCacheMixedRollouts(b *testing.B) {
	benchmarkDiscoverColdCacheMixedRollouts(b, 0)
}

func BenchmarkDiscoverColdCacheMixedRolloutsWorkers(b *testing.B) {
	for _, workers := range []int{1, 2, 4, 8, 16} {
		b.Run(fmt.Sprintf("workers-%02d", workers), func(b *testing.B) {
			benchmarkDiscoverColdCacheMixedRollouts(b, workers)
		})
	}
}

func benchmarkDiscoverColdCacheMixedRollouts(b *testing.B, workers int) {
	home, cachePath := makeMixedBenchmarkCodexStore(b)
	provider := Provider{Home: home, CachePath: cachePath, parseWorkers: workers}
	opts := session.DiscoverOptions{}

	b.ReportMetric(float64(provider.workerCount()), "workers")
	b.ReportAllocs()
	for b.Loop() {
		if err := os.RemoveAll(filepath.Dir(cachePath)); err != nil {
			b.Fatal(err)
		}
		got, err := provider.Discover(opts)
		if err != nil {
			b.Fatal(err)
		}
		if len(got) != 24 {
			b.Fatalf("sessions = %d, want 24", len(got))
		}
	}
}

func makeMixedBenchmarkCodexStore(b *testing.B) (string, string) {
	b.Helper()
	home := b.TempDir()
	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		b.Fatal(err)
	}
	sessionDir := filepath.Join(home, "sessions", "2026", "08", "07")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		b.Fatal(err)
	}
	sizes := []int{64 * 1024, 256 * 1024, 1024 * 1024, 4 * 1024 * 1024}
	base := time.Date(2026, 8, 7, 1, 0, 0, 0, time.UTC)
	for i := range 24 {
		path := filepath.Join(sessionDir, fmt.Sprintf("mixed-%02d.jsonl", i))
		body := mixedBenchmarkCodexSession(i, repo, sizes[i%len(sizes)])
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			b.Fatal(err)
		}
		modTime := base.Add(time.Duration(i) * time.Second)
		if err := os.Chtimes(path, modTime, modTime); err != nil {
			b.Fatal(err)
		}
	}
	return home, filepath.Join(b.TempDir(), "cache", "codex-cache.json")
}

func mixedBenchmarkCodexSession(i int, repo string, payloadBytes int) string {
	var body strings.Builder
	fmt.Fprintf(&body, `{"timestamp":"2026-08-07T01:00:00Z","type":"session_meta","payload":{"id":"mixed-%02d","parent_thread_id":"parent-%02d","timestamp":"2026-08-07T01:00:00Z","cwd":%q,"source":{"subagent":{"thread_spawn":{"parent_thread_id":"parent-%02d","depth":1,"agent_role":"explorer"}}}}}`+"\n", i, i, repo, i)
	fmt.Fprintf(&body, `{"timestamp":"2026-08-07T01:00:01Z","type":"turn_context","payload":{"cwd":%q,"model":"gpt-5.6"}}`+"\n", repo)
	payload := strings.Repeat("x", payloadBytes)
	fmt.Fprintf(&body, `{"timestamp":"2026-08-07T01:00:02Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":%q}]}}`+"\n", payload)
	return body.String()
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
