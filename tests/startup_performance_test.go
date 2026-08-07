package tests

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func BenchmarkCLIStartup(b *testing.B) {
	buildEnv := newASMTestEnv(b)
	binary := buildEnv.Build(b)

	b.Run("EmptyStoresEmptyCache", func(b *testing.B) {
		env := newASMTestEnv(b)
		verifyBinarySessions(b, env, binary, 0)
		benchmarkStartupProbe(b, env, binary, nil)
		reportStartupFixture(b, env, 0)
	})

	b.Run("EmptyStoresHistoricalCache", func(b *testing.B) {
		env := newASMTestEnv(b)
		populateCodexHistory(b, env.ProviderHome["codex"], 500, 0)
		mustRunBinary(b, env, binary, "--since-days", "0", "--resume", "__warm_cache__")
		if err := os.RemoveAll(env.ProviderHome["codex"]); err != nil {
			b.Fatal(err)
		}
		if err := os.MkdirAll(env.ProviderHome["codex"], 0o755); err != nil {
			b.Fatal(err)
		}
		verifyBinarySessions(b, env, binary, 0)
		benchmarkStartupProbe(b, env, binary, nil)
		reportStartupFixture(b, env, 500)
	})

	b.Run("WarmRecentWindow", func(b *testing.B) {
		env := newASMTestEnv(b)
		populateCachedProviders(b, env, 20)
		mustRunBinary(b, env, binary, "--resume", "__warm_cache__")
		verifyBinarySessions(b, env, binary, 120)
		benchmarkStartupProbe(b, env, binary, nil)
		reportStartupFixture(b, env, 120)
	})

	b.Run("WarmHistoryHeavy", func(b *testing.B) {
		env := newASMTestEnv(b)
		populateCodexHistory(b, env.ProviderHome["codex"], 2000, 10)
		mustRunBinary(b, env, binary, "--since-days", "0", "--resume", "__warm_cache__")
		verifyBinarySessions(b, env, binary, 10)
		benchmarkStartupProbe(b, env, binary, nil)
		reportStartupFixture(b, env, 2000)
	})

	b.Run("ColdPopulatedStores", func(b *testing.B) {
		env := newASMTestEnv(b)
		populateCachedProviders(b, env, 20)
		verifyBinarySessions(b, env, binary, 120)
		benchmarkStartupProbe(b, env, binary, func() {
			if err := os.RemoveAll(filepath.Join(env.CacheHome, "asm")); err != nil {
				b.Fatal(err)
			}
		})
		reportStartupFixture(b, env, 120)
	})

	b.Run("ChangedLargeCodexSession", func(b *testing.B) {
		env := newASMTestEnv(b)
		repo := b.TempDir()
		path := filepath.Join(env.ProviderHome["codex"], "sessions", "2026", "06", "13", "large.jsonl")
		writeFile(b, path, `{"timestamp":"2026-06-13T01:00:00Z","type":"session_meta","payload":{"id":"large","timestamp":"2026-06-13T01:00:00Z","cwd":`+jsonString(repo)+`}}
{"timestamp":"2026-06-13T01:00:01Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":`+jsonString(strings.Repeat("x", 16*1024*1024))+`}]}}
`)
		mustRunBinary(b, env, binary, "--resume", "__warm_cache__")
		verifyBinarySessions(b, env, binary, 1)
		iteration := 0
		benchmarkStartupProbe(b, env, binary, func() {
			iteration++
			f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				b.Fatal(err)
			}
			_, writeErr := fmt.Fprintf(f, "{\"timestamp\":\"2026-06-13T01:00:02Z\",\"type\":\"response_item\",\"payload\":{\"type\":\"message\",\"role\":\"user\",\"content\":[{\"type\":\"input_text\",\"text\":\"iteration %d\"}]}}\n", iteration)
			closeErr := f.Close()
			if writeErr != nil {
				b.Fatal(writeErr)
			}
			if closeErr != nil {
				b.Fatal(closeErr)
			}
		})
		reportStartupFixture(b, env, 1)
	})
}

func verifyBinarySessions(t testing.TB, env asmTestEnv, binary string, want int) {
	t.Helper()
	out, err := env.RunBinary(t, binary, "--json")
	if err != nil {
		t.Fatalf("verify benchmark fixture: %v\n%s", err, out)
	}
	var payload struct {
		Sessions []json.RawMessage `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("verify benchmark fixture JSON: %v\n%s", err, out)
	}
	if len(payload.Sessions) != want {
		t.Fatalf("fixture sessions = %d, want %d", len(payload.Sessions), want)
	}
}

func reportStartupFixture(b *testing.B, env asmTestEnv, sessions int) {
	b.Helper()
	b.StopTimer()
	var cacheBytes int64
	err := filepath.WalkDir(env.CacheHome, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		cacheBytes += info.Size()
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		b.Fatal(err)
	}
	b.ReportMetric(float64(cacheBytes), "cache-bytes")
	b.ReportMetric(float64(sessions), "fixture-sessions")
}

func benchmarkStartupProbe(b *testing.B, env asmTestEnv, binary string, beforeEach func()) {
	b.Helper()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if beforeEach != nil {
			b.StopTimer()
			beforeEach()
			b.StartTimer()
		}
		out, err := env.RunBinary(b, binary, "--resume", "__startup_probe__")
		if err == nil || !strings.Contains(out, "session not found") {
			b.Fatalf("startup probe = %v\n%s", err, out)
		}
	}
}

func mustRunBinary(t testing.TB, env asmTestEnv, binary string, args ...string) {
	t.Helper()
	out, err := env.RunBinary(t, binary, args...)
	if err == nil || !strings.Contains(out, "session not found") {
		t.Fatalf("warm-up probe = %v\n%s", err, out)
	}
}

func populateCachedProviders(t testing.TB, env asmTestEnv, each int) {
	t.Helper()
	for i := 0; i < each; i++ {
		repo := t.TempDir()
		suffix := fmt.Sprintf("%04d", i)
		writeSession(t, filepath.Join(env.ProviderHome["codex"], "sessions", "2026", "06", "13", suffix+".jsonl"), "codex-"+suffix, repo)
		writeClaudeSession(t, filepath.Join(env.ProviderHome["claude"], "projects", "repo", suffix+".jsonl"), "claude-"+suffix, repo, "claude "+suffix)
		writeKiroSession(t, env.ProviderHome["kiro"], "kiro-"+suffix, repo, "kiro "+suffix)
		writeOpencodeSession(t, env.ProviderHome["opencode"], "project", "opencode-"+suffix, repo, "opencode "+suffix)
		writeCodeBuddySession(t, env.ProviderHome["codebuddy"], "codebuddy-"+suffix, repo, "codebuddy "+suffix)
		writeCursorSession(t, env.ProviderHome["cursor"], "cursor-"+suffix, repo, "cursor "+suffix)
	}
}

func populateCodexHistory(t testing.TB, home string, count, recent int) {
	t.Helper()
	repo := t.TempDir()
	now := time.Now()
	for i := 0; i < count; i++ {
		suffix := fmt.Sprintf("%04d", i)
		path := filepath.Join(home, "sessions", "2026", "01", "01", suffix+".jsonl")
		writeSession(t, path, "history-"+suffix, repo)
		modTime := now.AddDate(0, 0, -60)
		if i < recent {
			modTime = now.Add(-time.Duration(i+1) * time.Minute)
		}
		setModTime(t, path, modTime)
	}
}
