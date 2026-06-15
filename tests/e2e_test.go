package tests

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCLIIndexesSearchesAndPrintsResumeCommand(t *testing.T) {
	home := t.TempDir()
	claudeHome := t.TempDir()
	sessionDir := filepath.Join(home, "sessions", "2026", "06", "13")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}

	writeSession(t, filepath.Join(sessionDir, "openclaw.jsonl"), "openclaw-session", "/data/code/openclaw/openclaw")
	writeSession(t, filepath.Join(sessionDir, "helper.jsonl"), "helper-session", "/data/code/lighthouse/helper")
	writeFile(t, filepath.Join(home, "history.jsonl"), `{"session_id":"openclaw-session","text":"fix openclaw bug"}
{"session_id":"helper-session","text":"helper deployment"}
`)

	base := time.Date(2026, 6, 13, 1, 0, 0, 0, time.UTC)
	if err := os.Chtimes(filepath.Join(sessionDir, "openclaw.jsonl"), base.Add(time.Hour), base.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(sessionDir, "helper.jsonl"), base, base); err != nil {
		t.Fatal(err)
	}

	out := runCommand(t, "--codex-home", home, "--claude-home", claudeHome, "--json", "--query", "openclaw")
	var payload struct {
		Projects []struct {
			CWD   string `json:"cwd"`
			Count int    `json:"count"`
		} `json:"projects"`
		Sessions []struct {
			ID       string `json:"id"`
			Provider string `json:"provider"`
			CWD      string `json:"cwd"`
			Title    string `json:"title"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if len(payload.Sessions) != 1 || payload.Sessions[0].ID != "openclaw-session" {
		t.Fatalf("unexpected sessions: %#v", payload.Sessions)
	}
	if payload.Sessions[0].Provider != "codex" {
		t.Fatalf("provider = %q, want codex", payload.Sessions[0].Provider)
	}
	if len(payload.Projects) != 1 || payload.Projects[0].CWD != "/data/code/openclaw/openclaw" || payload.Projects[0].Count != 1 {
		t.Fatalf("unexpected projects: %#v", payload.Projects)
	}

	cmd := runCommand(t, "--codex-home", home, "--claude-home", claudeHome, "--resume", "openclaw-session", "--print-exec")
	if !strings.Contains(cmd, `cd '/data/code/openclaw/openclaw' && 'codex' 'resume' 'openclaw-session'`) {
		t.Fatalf("unexpected resume command: %s", cmd)
	}
}

func TestCLIIndexesClaudeAndPrintsResumeCommand(t *testing.T) {
	codexHome := t.TempDir()
	claudeHome := t.TempDir()
	claudeDir := filepath.Join(claudeHome, "projects", "-data-code-openclaw-openclaw")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeClaudeSession(t, filepath.Join(claudeDir, "claude-session.jsonl"), "claude-session", "/data/code/openclaw/openclaw", "fix openclaw with claude")

	out := runCommand(t, "--codex-home", codexHome, "--claude-home", claudeHome, "--json", "--query", "claude")
	var payload struct {
		Projects []struct {
			CWD   string `json:"cwd"`
			Count int    `json:"count"`
		} `json:"projects"`
		Sessions []struct {
			ID       string `json:"id"`
			Provider string `json:"provider"`
			CWD      string `json:"cwd"`
			Title    string `json:"title"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if len(payload.Sessions) != 1 || payload.Sessions[0].ID != "claude-session" {
		t.Fatalf("unexpected sessions: %#v", payload.Sessions)
	}
	if payload.Sessions[0].Provider != "claude" {
		t.Fatalf("provider = %q, want claude", payload.Sessions[0].Provider)
	}
	if len(payload.Projects) != 1 || payload.Projects[0].Count != 1 {
		t.Fatalf("unexpected projects: %#v", payload.Projects)
	}

	cmd := runCommand(t, "--codex-home", codexHome, "--claude-home", claudeHome, "--resume", "claude-session", "--print-exec")
	if !strings.Contains(cmd, `cd '/data/code/openclaw/openclaw' && 'claude' '--resume' 'claude-session'`) {
		t.Fatalf("unexpected resume command: %s", cmd)
	}
}

func TestCLIIndexesKimiAndPrintsResumeCommand(t *testing.T) {
	codexHome := t.TempDir()
	claudeHome := t.TempDir()
	kimiHome := t.TempDir()
	kimiDir := filepath.Join(kimiHome, "sessions", "wd_openclaw", "ses_kimi")
	writeKimiSession(t, kimiHome, kimiDir, "ses_kimi", "/data/code/openclaw/openclaw", "fix openclaw with kimi")

	out := runCommand(t, "--codex-home", codexHome, "--claude-home", claudeHome, "--kimi-home", kimiHome, "--json", "--query", "kimi")
	var payload struct {
		Projects []struct {
			CWD   string `json:"cwd"`
			Count int    `json:"count"`
		} `json:"projects"`
		Sessions []struct {
			ID       string `json:"id"`
			Provider string `json:"provider"`
			CWD      string `json:"cwd"`
			Title    string `json:"title"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if len(payload.Sessions) != 1 || payload.Sessions[0].ID != "ses_kimi" {
		t.Fatalf("unexpected sessions: %#v", payload.Sessions)
	}
	if payload.Sessions[0].Provider != "kimi" {
		t.Fatalf("provider = %q, want kimi", payload.Sessions[0].Provider)
	}
	if len(payload.Projects) != 1 || payload.Projects[0].Count != 1 {
		t.Fatalf("unexpected projects: %#v", payload.Projects)
	}

	cmd := runCommand(t, "--codex-home", codexHome, "--claude-home", claudeHome, "--kimi-home", kimiHome, "--resume", "ses_kimi", "--print-exec")
	if !strings.Contains(cmd, `cd '/data/code/openclaw/openclaw' && 'kimi' '--session' 'ses_kimi'`) {
		t.Fatalf("unexpected resume command: %s", cmd)
	}
}

func TestCLISinceDaysFiltersOldSessions(t *testing.T) {
	home := t.TempDir()
	claudeHome := t.TempDir()
	sessionDir := filepath.Join(home, "sessions", "2025", "01", "01")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeSession(t, filepath.Join(sessionDir, "old.jsonl"), "old-session", "/repo/old")
	oldTime := time.Date(2025, 1, 1, 1, 0, 0, 0, time.UTC)
	if err := os.Chtimes(filepath.Join(sessionDir, "old.jsonl"), oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	out := runCommand(t, "--codex-home", home, "--claude-home", claudeHome, "--json")
	if strings.Contains(out, "old-session") {
		t.Fatalf("default window should hide old sessions: %s", out)
	}

	out = runCommand(t, "--codex-home", home, "--claude-home", claudeHome, "--json", "--since-days", "0")
	if !strings.Contains(out, "old-session") {
		t.Fatalf("since-days=0 should include old sessions: %s", out)
	}
}

func TestCLIUsesRolloutUserMessageWhenHistoryIsMissing(t *testing.T) {
	home := t.TempDir()
	claudeHome := t.TempDir()
	sessionDir := filepath.Join(home, "sessions", "2026", "06", "13")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(sessionDir, "session.jsonl"), `{"timestamp":"2026-06-13T01:00:00Z","type":"session_meta","payload":{"id":"session-without-history","timestamp":"2026-06-13T01:00:00Z","cwd":"/repo/openclaw"}}
{"timestamp":"2026-06-13T01:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"# AGENTS.md instructions for /repo\n\n<INSTRUCTIONS>ignore me</INSTRUCTIONS>"}]}}
{"timestamp":"2026-06-13T01:00:02Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"investigate missing title"}]}}
`)
	now := time.Now()
	if err := os.Chtimes(filepath.Join(sessionDir, "session.jsonl"), now, now); err != nil {
		t.Fatal(err)
	}

	out := runCommand(t, "--codex-home", home, "--claude-home", claudeHome, "--json", "--query", "missing title")
	var payload struct {
		Sessions []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if len(payload.Sessions) != 1 {
		t.Fatalf("unexpected sessions: %#v", payload.Sessions)
	}
	if payload.Sessions[0].Title != "investigate missing title" {
		t.Fatalf("title = %q", payload.Sessions[0].Title)
	}
}

func TestCLIUsesCodexSessionIndexTitle(t *testing.T) {
	home := t.TempDir()
	claudeHome := t.TempDir()
	sessionDir := filepath.Join(home, "sessions", "2026", "06", "13")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeSession(t, filepath.Join(sessionDir, "session.jsonl"), "native-title-session", "/repo/openclaw")
	writeFile(t, filepath.Join(home, "history.jsonl"), `{"session_id":"native-title-session","text":"history title"}
`)
	writeFile(t, filepath.Join(home, "session_index.jsonl"), `{"id":"native-title-session","thread_name":"Native Codex Title","updated_at":"2026-06-13T01:00:00Z"}
`)
	now := time.Now()
	if err := os.Chtimes(filepath.Join(sessionDir, "session.jsonl"), now, now); err != nil {
		t.Fatal(err)
	}

	out := runCommand(t, "--codex-home", home, "--claude-home", claudeHome, "--json", "--query", "Native Codex")
	var payload struct {
		Sessions []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if len(payload.Sessions) != 1 {
		t.Fatalf("unexpected sessions: %#v", payload.Sessions)
	}
	if payload.Sessions[0].Title != "Native Codex Title" {
		t.Fatalf("title = %q", payload.Sessions[0].Title)
	}
}

func runCommand(t *testing.T, args ...string) string {
	t.Helper()
	cmdArgs := append([]string{"run", "./cmd/session-manager"}, args...)
	cmd := exec.Command("go", cmdArgs...)
	cmd.Dir = ".."
	goCache := os.Getenv("GOCACHE")
	if goCache == "" {
		cacheDir, err := os.UserCacheDir()
		if err != nil {
			t.Fatal(err)
		}
		goCache = filepath.Join(cacheDir, "go-build")
	}
	cmd.Env = append(os.Environ(),
		"GOCACHE="+goCache,
		"XDG_CACHE_HOME="+t.TempDir(),
		"KIMI_CODE_HOME="+t.TempDir(),
		"KIMI_HOME="+t.TempDir(),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("command failed: %v\n%s", err, string(out))
	}
	return string(out)
}

func writeSession(t *testing.T, path, id, cwd string) {
	t.Helper()
	writeFile(t, path, `{"timestamp":"2026-06-13T01:00:00Z","type":"session_meta","payload":{"id":"`+id+`","timestamp":"2026-06-13T01:00:00Z","cwd":"`+cwd+`"}}
`)
}

func writeClaudeSession(t *testing.T, path, id, cwd, title string) {
	t.Helper()
	writeFile(t, path, `{"type":"user","sessionId":"`+id+`","cwd":"`+cwd+`","timestamp":"2026-06-13T01:00:00Z","message":{"role":"user","content":"`+title+`"}}
`)
}

func writeKimiSession(t *testing.T, home, sessionDir, id, cwd, title string) {
	t.Helper()
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(home, "session_index.jsonl"), `{"sessionId":"`+id+`","sessionDir":"`+sessionDir+`","workDir":"`+cwd+`"}
`)
	writeFile(t, filepath.Join(sessionDir, "state.json"), `{"createdAt":"2026-06-13T01:00:00Z","updatedAt":"2026-06-13T01:01:00Z","title":"`+title+`"}
`)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
