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

func BenchmarkDiscoverChangedLargeSession(b *testing.B) {
	home := b.TempDir()
	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		b.Fatal(err)
	}
	path := filepath.Join(home, "projects", "-repo", "large.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		b.Fatal(err)
	}
	body := fmt.Sprintf(`{"type":"user","sessionId":"large","cwd":%q,"timestamp":"2026-06-15T01:00:00Z","message":{"role":"user","content":"initial"}}`+"\n", repo) +
		`{"type":"assistant","sessionId":"large","timestamp":"2026-06-15T01:00:01Z","message":{"role":"assistant","content":"` + strings.Repeat("x", 5*1024*1024) + `"}}` + "\n"
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
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			b.Fatal(err)
		}
		_, writeErr := fmt.Fprintf(f, `{"type":"user","sessionId":"large","cwd":%q,"timestamp":"2026-06-15T01:00:02Z","message":{"role":"user","content":"iteration %d"}}`+"\n", repo, i)
		closeErr := f.Close()
		if writeErr != nil || closeErr != nil {
			b.Fatalf("append: write=%v close=%v", writeErr, closeErr)
		}
		b.StartTimer()
		got, err := provider.Discover(session.DiscoverOptions{})
		if err != nil || len(got) != 1 {
			b.Fatalf("sessions=%d err=%v", len(got), err)
		}
	}
}

func BenchmarkDiscoverColdCacheMixedTranscripts(b *testing.B) {
	benchmarkDiscoverColdCacheMixedTranscripts(b, 0)
}

func BenchmarkDiscoverColdCacheMixedTranscriptsWorkers(b *testing.B) {
	for _, workers := range []int{1, 2, 4, 8, 16} {
		b.Run(fmt.Sprintf("workers-%02d", workers), func(b *testing.B) {
			benchmarkDiscoverColdCacheMixedTranscripts(b, workers)
		})
	}
}

func BenchmarkDiscoverColdCacheWithSubagentTranscripts(b *testing.B) {
	home := b.TempDir()
	repo := filepath.FromSlash("/benchmark/claude-repo")
	projectDir := filepath.Join(home, "projects", "-repo")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		b.Fatal(err)
	}
	sizes := []int{64 << 10, 256 << 10, 1 << 20, 4 << 20}
	var storeBytes int64
	var mainBytes int64
	for i := range 16 {
		id := fmt.Sprintf("%08x-0000-4000-8000-%012x", i+1, i+1)
		mainBody := benchmarkClaudeMixedTranscript(id, repo, "main", sizes[i%len(sizes)])
		mainPath := filepath.Join(projectDir, id+".jsonl")
		if err := os.WriteFile(mainPath, []byte(mainBody), 0o644); err != nil {
			b.Fatal(err)
		}
		mainBytes += int64(len(mainBody))

		subagentBody := benchmarkClaudeMixedTranscript(id, repo, "subagent", sizes[(i+2)%len(sizes)])
		subagentDir := projectDir
		if i%2 == 0 {
			subagentDir = filepath.Join(projectDir, "subagents")
			if err := os.MkdirAll(subagentDir, 0o755); err != nil {
				b.Fatal(err)
			}
		}
		subagentPath := filepath.Join(subagentDir, fmt.Sprintf("agent-%016x.jsonl", i+1))
		if err := os.WriteFile(subagentPath, []byte(subagentBody), 0o644); err != nil {
			b.Fatal(err)
		}
		storeBytes += int64(len(subagentBody))
	}
	storeBytes += mainBytes

	cachePath := filepath.Join(b.TempDir(), "claude-cache.json")
	provider := Provider{Home: home, CachePath: cachePath}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := os.RemoveAll(filepath.Dir(cachePath)); err != nil {
			b.Fatal(err)
		}
		got, err := provider.Discover(session.DiscoverOptions{})
		if err != nil || len(got) != 16 {
			b.Fatalf("sessions=%d err=%v", len(got), err)
		}
	}
	b.ReportMetric(float64(storeBytes), "store-bytes/op")
	b.ReportMetric(float64(mainBytes), "main-transcript-bytes/op")
}

func benchmarkClaudeMixedTranscript(id, repo, title string, assistantBytes int) string {
	return fmt.Sprintf(`{"type":"user","message":{"role":"user","content":%q},"timestamp":"2026-08-10T01:00:00Z","entrypoint":"cli","cwd":%q,"sessionId":%q,"gitBranch":"main"}`+"\n", title, repo, id) +
		fmt.Sprintf(`{"message":{"role":"assistant","model":"claude-sonnet-4","content":"%s"},"type":"assistant","timestamp":"2026-08-10T01:00:01Z","entrypoint":"cli","cwd":%q,"sessionId":%q,"gitBranch":"main"}`+"\n", strings.Repeat("x", assistantBytes), repo, id) +
		fmt.Sprintf(`{"type":"summary","summary":%q,"sessionId":%q}`+"\n", title, id)
}

func benchmarkDiscoverColdCacheMixedTranscripts(b *testing.B, workers int) {
	home := b.TempDir()
	repo := filepath.FromSlash("/benchmark/claude-repo")
	projectDir := filepath.Join(home, "projects", "-repo")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		b.Fatal(err)
	}
	sizes := []int{64 << 10, 256 << 10, 1 << 20, 4 << 20}
	var inputBytes int64
	for i := range 24 {
		path := filepath.Join(projectDir, fmt.Sprintf("mixed-%03d.jsonl", i))
		body := fmt.Sprintf(`{"type":"user","message":{"role":"user","content":"prompt %03d"},"timestamp":"2026-06-15T01:00:00Z","entrypoint":"cli","cwd":%q,"sessionId":"mixed-%03d","gitBranch":"main"}`+"\n", i, repo, i) +
			fmt.Sprintf(`{"message":{"role":"assistant","model":"claude-sonnet-4","content":"%s"},"type":"assistant","timestamp":"2026-06-15T01:00:01Z","entrypoint":"cli","cwd":%q,"sessionId":"mixed-%03d","gitBranch":"main"}`+"\n", strings.Repeat("x", sizes[i%len(sizes)]), repo, i) +
			fmt.Sprintf(`{"type":"summary","summary":"Native Claude Title %03d","sessionId":"mixed-%03d"}`+"\n", i, i)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			b.Fatal(err)
		}
		inputBytes += int64(len(body))
	}
	cachePath := filepath.Join(b.TempDir(), "cache.json")
	provider := Provider{Home: home, CachePath: cachePath, parseWorkers: workers}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := os.RemoveAll(filepath.Dir(cachePath)); err != nil {
			b.Fatal(err)
		}
		got, err := provider.Discover(session.DiscoverOptions{})
		if err != nil || len(got) != 24 {
			b.Fatalf("sessions=%d err=%v", len(got), err)
		}
	}
	b.ReportMetric(float64(provider.workerCount(true)), "workers")
	b.ReportMetric(float64(inputBytes), "transcript-bytes/op")
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
