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

	out := runCommand(t, "--codex-home", home, "--json", "--query", "openclaw")
	var payload struct {
		Projects []struct {
			CWD   string `json:"cwd"`
			Count int    `json:"count"`
		} `json:"projects"`
		Sessions []struct {
			ID    string `json:"id"`
			CWD   string `json:"cwd"`
			Title string `json:"title"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if len(payload.Sessions) != 1 || payload.Sessions[0].ID != "openclaw-session" {
		t.Fatalf("unexpected sessions: %#v", payload.Sessions)
	}
	if len(payload.Projects) != 1 || payload.Projects[0].CWD != "/data/code/openclaw/openclaw" || payload.Projects[0].Count != 1 {
		t.Fatalf("unexpected projects: %#v", payload.Projects)
	}

	cmd := runCommand(t, "--codex-home", home, "--resume", "openclaw-session", "--print-exec")
	if !strings.Contains(cmd, `cd "/data/code/openclaw/openclaw" && "codex" "resume" "openclaw-session"`) {
		t.Fatalf("unexpected resume command: %s", cmd)
	}
}

func TestCLISinceDaysFiltersOldSessions(t *testing.T) {
	home := t.TempDir()
	sessionDir := filepath.Join(home, "sessions", "2025", "01", "01")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeSession(t, filepath.Join(sessionDir, "old.jsonl"), "old-session", "/repo/old")
	oldTime := time.Date(2025, 1, 1, 1, 0, 0, 0, time.UTC)
	if err := os.Chtimes(filepath.Join(sessionDir, "old.jsonl"), oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	out := runCommand(t, "--codex-home", home, "--json")
	if strings.Contains(out, "old-session") {
		t.Fatalf("default window should hide old sessions: %s", out)
	}

	out = runCommand(t, "--codex-home", home, "--json", "--since-days", "0")
	if !strings.Contains(out, "old-session") {
		t.Fatalf("since-days=0 should include old sessions: %s", out)
	}
}

func runCommand(t *testing.T, args ...string) string {
	t.Helper()
	cmdArgs := append([]string{"run", "./cmd/session-manager"}, args...)
	cmd := exec.Command("go", cmdArgs...)
	cmd.Dir = ".."
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

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
