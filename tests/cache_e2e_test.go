package tests

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

type cliCachePayload struct {
	Projects       []map[string]any `json:"projects"`
	Sessions       []cliSession     `json:"sessions"`
	ProviderErrors []map[string]any `json:"provider_errors"`
}

type cliSession struct {
	ID       string            `json:"id"`
	Provider string            `json:"provider"`
	CWD      string            `json:"cwd"`
	Title    string            `json:"title"`
	Metadata map[string]string `json:"metadata"`
}

func TestCLICacheColdAndWarmResultsMatchAcrossProviders(t *testing.T) {
	env := newASMTestEnv(t)
	repo := t.TempDir()
	writeCachedProviderFixtures(t, env, repo)

	cold := runJSONWithEnv(t, env, "--since-days", "0", "--json")
	warm := runJSONWithEnv(t, env, "--since-days", "0", "--json")
	if !reflect.DeepEqual(cold, warm) {
		t.Fatalf("cold and warm output differ:\ncold=%#v\nwarm=%#v", cold, warm)
	}
	if len(warm.Sessions) != 6 {
		t.Fatalf("sessions = %#v, want all six cached providers", warm.Sessions)
	}
	providers := make(map[string]bool, len(warm.Sessions))
	for _, item := range warm.Sessions {
		providers[item.Provider] = true
	}
	for _, want := range []string{"codex", "claude", "kiro", "opencode", "codebuddy", "cursor"} {
		if !providers[want] {
			t.Fatalf("provider %q missing from ordered sessions %#v", want, warm.Sessions)
		}
	}
}

func TestCLICacheInvalidationAndDynamicInputs(t *testing.T) {
	env := newASMTestEnv(t)
	repo := t.TempDir()
	opencodeRepoAfter := t.TempDir()
	missing := filepath.Join(t.TempDir(), "created-after-cold-run")

	writeClaudeSession(t, filepath.Join(env.ProviderHome["claude"], "projects", "repo", "claude.jsonl"), "claude", repo, "claude before")
	writeSession(t, filepath.Join(env.ProviderHome["codex"], "sessions", "2026", "06", "13", "codex.jsonl"), "codex", repo)
	writeSession(t, filepath.Join(env.ProviderHome["codex"], "sessions", "2026", "06", "13", "codex-history.jsonl"), "codex-history", repo)
	writeFile(t, filepath.Join(env.ProviderHome["codex"], "session_index.jsonl"), `{"id":"codex","thread_name":"codex before"}`+"\n")
	writeFile(t, filepath.Join(env.ProviderHome["codex"], "history.jsonl"), `{"session_id":"codex-history","text":"history before"}`+"\n")
	writeKiroFallbackFixture(t, env.ProviderHome["kiro"], repo, "kiro before")
	writeOpencodeFallbackFixture(t, env.ProviderHome["opencode"], repo, "opencode before")
	writeCodeBuddySession(t, env.ProviderHome["codebuddy"], "codebuddy", missing, "codebuddy title")

	before := runJSONWithEnv(t, env, "--since-days", "0", "--json")
	assertSessionTitle(t, before, "claude", "claude before")
	assertSessionTitle(t, before, "codex", "codex before")
	assertSessionTitle(t, before, "codex-history", "history before")
	assertSessionTitle(t, before, "kiro", "kiro before")
	assertSessionTitle(t, before, "opencode", "opencode before")
	if got := sessionByID(t, before, "codebuddy").Metadata["cwd_missing"]; got != "true" {
		t.Fatalf("cold cwd_missing = %q, want true", got)
	}

	claudePath := filepath.Join(env.ProviderHome["claude"], "projects", "repo", "claude.jsonl")
	writeClaudeSession(t, claudePath, "claude", repo, "claude after primary change")
	writeFile(t, filepath.Join(env.ProviderHome["codex"], "session_index.jsonl"), `{"id":"codex","thread_name":"codex after index change"}`+"\n")
	writeFile(t, filepath.Join(env.ProviderHome["codex"], "history.jsonl"), `{"session_id":"codex-history","text":"history after change"}`+"\n")
	writeFile(t, filepath.Join(env.ProviderHome["kiro"], "sessions", "cli", "kiro.jsonl"), kiroPromptRecord("kiro after prompt change")+"\n")
	writeOpencodeProjectFallback(t, env.ProviderHome["opencode"], "project", opencodeRepoAfter)
	writeOpencodeMessageFallback(t, env.ProviderHome["opencode"], "opencode", "message-after", "opencode after message change")
	if err := os.MkdirAll(missing, 0o755); err != nil {
		t.Fatal(err)
	}

	after := runJSONWithEnv(t, env, "--since-days", "0", "--json")
	assertSessionTitle(t, after, "claude", "claude after primary change")
	assertSessionTitle(t, after, "codex", "codex after index change")
	assertSessionTitle(t, after, "codex-history", "history after change")
	assertSessionTitle(t, after, "kiro", "kiro after prompt change")
	assertSessionTitle(t, after, "opencode", "opencode after message change")
	if got := sessionByID(t, after, "opencode").CWD; got != opencodeRepoAfter {
		t.Fatalf("opencode dynamic project cwd = %q, want %q", got, opencodeRepoAfter)
	}
	if got := sessionByID(t, after, "codebuddy").Metadata["cwd_missing"]; got != "" {
		t.Fatalf("warm cwd_missing = %q, want refreshed status", got)
	}
}

func TestCLICachePreservesSinceLimitOrderingAndResumeSafety(t *testing.T) {
	env := newASMTestEnv(t)
	repo := t.TempDir()
	missing := filepath.Join(t.TempDir(), "missing")
	root := filepath.Join(env.ProviderHome["codex"], "sessions", "2026", "06", "13")
	recentPath := filepath.Join(root, "recent.jsonl")
	oldPath := filepath.Join(root, "old.jsonl")
	missingPath := filepath.Join(root, "missing.jsonl")
	writeSession(t, recentPath, "recent", repo)
	writeSession(t, oldPath, "old", repo)
	writeSession(t, missingPath, "missing", missing)
	now := time.Now()
	setModTime(t, recentPath, now.Add(-time.Hour))
	setModTime(t, missingPath, now.Add(-2*time.Hour))
	setModTime(t, oldPath, now.AddDate(0, 0, -60))

	bounded := runJSONWithEnv(t, env, "--json")
	assertSessionIDs(t, bounded, "recent", "missing")
	unbounded := runJSONWithEnv(t, env, "--since-days", "0", "--json")
	assertSessionIDs(t, unbounded, "recent", "missing", "old")
	boundedAgain := runJSONWithEnv(t, env, "--json")
	assertSessionIDs(t, boundedAgain, "recent", "missing")
	limited := runJSONWithEnv(t, env, "--since-days", "0", "--limit", "1", "--json")
	assertSessionIDs(t, limited, "recent")

	out, err := env.Run(t, "--since-days", "0", "--resume", "missing", "--print-exec")
	if err == nil || !strings.Contains(out, "cwd is unavailable") {
		t.Fatalf("missing-cwd resume = %v\n%s", err, out)
	}
}

func TestCLIReportKeepsEvidenceIndependentFromNormalizedTitle(t *testing.T) {
	env := newASMTestEnv(t)
	repo := t.TempDir()
	path := filepath.Join(env.ProviderHome["codex"], "sessions", "2026", "06", "13", "report.jsonl")
	longMessage := "evidence-start " + strings.Repeat("内容🙂", 100) + " evidence-end"
	writeFile(t, path, `{"timestamp":"2026-06-13T01:00:00Z","type":"session_meta","payload":{"id":"report","timestamp":"2026-06-13T01:00:00Z","cwd":`+jsonString(repo)+`}}
{"timestamp":"2026-06-13T01:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":`+jsonString(longMessage)+`}]}}
`)
	writeFile(t, filepath.Join(env.ProviderHome["codex"], "session_index.jsonl"), `{"id":"report","thread_name":"short native title","updated_at":"2026-06-13T01:00:01Z"}`+"\n")

	out, err := env.Run(t, "report", "--start", "2026-06-13", "--end", "2026-06-14", "--preview-max-chars", "2000")
	if err != nil {
		t.Fatalf("report command: %v\n%s", err, out)
	}
	var payload struct {
		Sessions []struct {
			Title    string `json:"title"`
			Evidence []struct {
				Text string `json:"text"`
			} `json:"evidence"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("invalid report JSON: %v\n%s", err, out)
	}
	if len(payload.Sessions) != 1 || payload.Sessions[0].Title != "" {
		t.Fatalf("report sessions = %#v", payload.Sessions)
	}
	if len(payload.Sessions[0].Evidence) != 1 || payload.Sessions[0].Evidence[0].Text != longMessage {
		t.Fatalf("title changed report evidence: %#v", payload.Sessions[0].Evidence)
	}
}

func TestCLITitleNormalizationAcrossProviders(t *testing.T) {
	env := newASMTestEnv(t)
	repo := t.TempDir()
	providers := []string{"codex", "claude", "kimi", "kiro", "opencode", "codebuddy", "cursor", "openclaw", "zcode"}
	const suffix = "tail-search-token"

	writeTitleNormalizationFixtures(t, env, repo, suffix)
	payload := runJSONWithEnv(t, env, "--since-days", "0", "--json")
	if len(payload.Sessions) != len(providers)*2 {
		t.Fatalf("sessions = %d, want %d: %#v", len(payload.Sessions), len(providers)*2, payload.Sessions)
	}

	for _, provider := range providers {
		normal := sessionByID(t, payload, provider+"-normal")
		long := sessionByID(t, payload, provider+"-long")
		wantNormal := "普通 title " + provider
		if normal.Title != wantNormal {
			t.Errorf("%s normal title = %q, want %q", provider, normal.Title, wantNormal)
		}
		if !utf8.ValidString(long.Title) {
			t.Errorf("%s long title is not valid UTF-8", provider)
		}
		if got := utf8.RuneCountInString(long.Title); got > 512 {
			t.Errorf("%s long title runes = %d, want <= 512", provider, got)
		}
		if got := len(long.Title); got > 2048 {
			t.Errorf("%s long title bytes = %d, want <= 2048", provider, got)
		}
		if !strings.HasSuffix(long.Title, "…") {
			t.Errorf("%s long title = %q, want truncation ellipsis", provider, long.Title)
		}
		if strings.Contains(long.Title, suffix) {
			t.Errorf("%s long title still contains truncated suffix", provider)
		}
		if normal.Metadata["title_source"] != long.Metadata["title_source"] {
			t.Errorf("%s title_source changed: normal=%q long=%q", provider, normal.Metadata["title_source"], long.Metadata["title_source"])
		}
	}

	truncatedSuffix := runJSONWithEnv(t, env, "--since-days", "0", "--json", "--query", suffix)
	if len(truncatedSuffix.Sessions) != 0 {
		t.Fatalf("truncated suffix remains searchable: %#v", truncatedSuffix.Sessions)
	}
}

func writeTitleNormalizationFixtures(t testing.TB, env asmTestEnv, repo string, suffix string) {
	t.Helper()
	title := func(provider string, long bool) string {
		if !long {
			return "普通 title " + provider
		}
		return "阶段E " + provider + " " + strings.Repeat("汉🙂", 400) + " " + suffix
	}

	codexRoot := filepath.Join(env.ProviderHome["codex"], "sessions", "2026", "06", "13")
	writeSession(t, filepath.Join(codexRoot, "normal.jsonl"), "codex-normal", repo)
	writeSession(t, filepath.Join(codexRoot, "long.jsonl"), "codex-long", repo)
	writeFile(t, filepath.Join(env.ProviderHome["codex"], "session_index.jsonl"),
		`{"id":"codex-normal","thread_name":`+jsonString(title("codex", false))+`}`+"\n"+
			`{"id":"codex-long","thread_name":`+jsonString(title("codex", true))+`}`+"\n")

	for _, long := range []bool{false, true} {
		kind := "normal"
		if long {
			kind = "long"
		}
		writeClaudeSession(t, filepath.Join(env.ProviderHome["claude"], "projects", "repo", kind+".jsonl"), "claude-"+kind, repo, title("claude", long))
		writeKiroSession(t, env.ProviderHome["kiro"], "kiro-"+kind, repo, title("kiro", long))
		writeOpencodeSession(t, env.ProviderHome["opencode"], "project-"+kind, "opencode-"+kind, repo, title("opencode", long))
		writeCodeBuddySession(t, env.ProviderHome["codebuddy"], "codebuddy-"+kind, repo, title("codebuddy", long))
		writeCursorSession(t, env.ProviderHome["cursor"], "cursor-"+kind, repo, title("cursor", long))
	}

	kimiNormalDir := filepath.Join(env.ProviderHome["kimi"], "sessions", "normal")
	kimiLongDir := filepath.Join(env.ProviderHome["kimi"], "sessions", "long")
	writeFile(t, filepath.Join(env.ProviderHome["kimi"], "session_index.jsonl"),
		`{"sessionId":"kimi-normal","sessionDir":`+jsonString(kimiNormalDir)+`,"workDir":`+jsonString(repo)+`}`+"\n"+
			`{"sessionId":"kimi-long","sessionDir":`+jsonString(kimiLongDir)+`,"workDir":`+jsonString(repo)+`}`+"\n")
	writeFile(t, filepath.Join(kimiNormalDir, "state.json"), `{"createdAt":"2026-06-13T01:00:00Z","title":`+jsonString(title("kimi", false))+`}`)
	writeFile(t, filepath.Join(kimiLongDir, "state.json"), `{"createdAt":"2026-06-13T01:00:00Z","title":`+jsonString(title("kimi", true))+`}`)

	writeFile(t, filepath.Join(env.ProviderHome["openclaw"], "agents", "main", "sessions", "sessions.json"),
		`{"openclaw-normal":{"sessionId":"native-normal","updatedAt":1781312460000,"spawnedCwd":`+jsonString(repo)+`,"displayName":`+jsonString(title("openclaw", false))+`},`+
			`"openclaw-long":{"sessionId":"native-long","updatedAt":1781312400000,"spawnedCwd":`+jsonString(repo)+`,"displayName":`+jsonString(title("openclaw", true))+`}}`)

	writeZCodeSessions(t, env.ProviderHome["zcode"], []zcodeSessionFixture{
		{ID: "zcode-normal", CWD: repo, Title: title("zcode", false), CreatedAt: 1781322000000, UpdatedAt: 1781322060000},
		{ID: "zcode-long", CWD: repo, Title: title("zcode", true), CreatedAt: 1781321900000, UpdatedAt: 1781321960000},
	})
}

func writeCachedProviderFixtures(t testing.TB, env asmTestEnv, repo string) {
	t.Helper()
	writeSession(t, filepath.Join(env.ProviderHome["codex"], "sessions", "2026", "06", "13", "codex.jsonl"), "codex", repo)
	writeFile(t, filepath.Join(env.ProviderHome["codex"], "session_index.jsonl"), `{"id":"codex","thread_name":"codex title"}`+"\n")
	writeClaudeSession(t, filepath.Join(env.ProviderHome["claude"], "projects", "repo", "claude.jsonl"), "claude", repo, "claude title")
	writeKiroSession(t, env.ProviderHome["kiro"], "kiro", repo, "kiro title")
	writeOpencodeSession(t, env.ProviderHome["opencode"], "project", "opencode", repo, "opencode title")
	writeCodeBuddySession(t, env.ProviderHome["codebuddy"], "codebuddy", repo, "codebuddy title")
	writeCursorSession(t, env.ProviderHome["cursor"], "cursor", repo, "cursor title")
}

func writeKiroFallbackFixture(t testing.TB, home, cwd, title string) {
	t.Helper()
	writeFile(t, filepath.Join(home, "sessions", "cli", "kiro.json"), `{"session_id":"kiro","cwd":`+jsonString(cwd)+`,"created_at":"2026-06-13T01:00:00Z","updated_at":"2026-06-13T01:01:00Z","title":"","session_created_reason":"user"}`+"\n")
	writeFile(t, filepath.Join(home, "sessions", "cli", "kiro.jsonl"), kiroPromptRecord(title)+"\n")
}

func kiroPromptRecord(title string) string {
	return `{"kind":"Prompt","version":"v1","data":{"message_id":"msg","content":[{"kind":"text","data":` + jsonString(title) + `}],"meta":{"timestamp":1781312400}}}`
}

func writeOpencodeFallbackFixture(t testing.TB, home, cwd, title string) {
	t.Helper()
	writeFile(t, filepath.Join(home, "storage", "session", "project", "opencode.json"), `{"id":"opencode","projectID":"project","directory":"","title":"","time":{"created":1781322000000}}`)
	writeOpencodeProjectFallback(t, home, "project", cwd)
	writeOpencodeMessageFallback(t, home, "opencode", "message-before", title)
}

func writeOpencodeProjectFallback(t testing.TB, home, projectID, cwd string) {
	t.Helper()
	writeFile(t, filepath.Join(home, "storage", "project", projectID+".json"), `{"id":`+jsonString(projectID)+`,"worktree":`+jsonString(cwd)+`}`)
}

func writeOpencodeMessageFallback(t testing.TB, home, sessionID, messageID, title string) {
	t.Helper()
	writeFile(t, filepath.Join(home, "storage", "message", sessionID, messageID+".json"), `{"id":`+jsonString(messageID)+`,"sessionID":`+jsonString(sessionID)+`,"role":"user"}`)
	writeFile(t, filepath.Join(home, "storage", "part", messageID, "part.json"), `{"type":"text","text":`+jsonString(title)+`}`)
}

func runJSONWithEnv(t testing.TB, env asmTestEnv, args ...string) cliCachePayload {
	t.Helper()
	out, err := env.Run(t, args...)
	if err != nil {
		t.Fatalf("asm command: %v\n%s", err, out)
	}
	var payload cliCachePayload
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	return payload
}

func sessionByID(t testing.TB, payload cliCachePayload, id string) cliSession {
	t.Helper()
	for _, item := range payload.Sessions {
		if item.ID == id {
			return item
		}
	}
	t.Fatalf("session %q not found in %#v", id, payload.Sessions)
	return cliSession{}
}

func assertSessionTitle(t testing.TB, payload cliCachePayload, id, want string) {
	t.Helper()
	if got := sessionByID(t, payload, id).Title; got != want {
		t.Fatalf("session %q title = %q, want %q", id, got, want)
	}
}

func assertSessionIDs(t testing.TB, payload cliCachePayload, want ...string) {
	t.Helper()
	got := make([]string, 0, len(payload.Sessions))
	for _, item := range payload.Sessions {
		got = append(got, item.ID)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("session IDs = %#v, want %#v", got, want)
	}
}

func setModTime(t testing.TB, path string, modTime time.Time) {
	t.Helper()
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatal(err)
	}
}
