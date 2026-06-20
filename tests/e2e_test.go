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

	cmd = runCommand(t, "resume", "--codex-home", home, "--claude-home", claudeHome, "--provider", "codex", "--print-exec", "openclaw-session")
	if !strings.Contains(cmd, `cd '/data/code/openclaw/openclaw' && 'codex' 'resume' 'openclaw-session'`) {
		t.Fatalf("unexpected resume subcommand: %s", cmd)
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

func TestCLIIndexesOpencodeAndPrintsResumeCommand(t *testing.T) {
	codexHome := t.TempDir()
	claudeHome := t.TempDir()
	kimiHome := t.TempDir()
	opencodeHome := t.TempDir()
	writeOpencodeSession(t, opencodeHome, "project_one", "ses_opencode", "/data/code/openclaw/openclaw", "fix openclaw with opencode")

	out := runCommand(t, "--codex-home", codexHome, "--claude-home", claudeHome, "--kimi-home", kimiHome, "--opencode-home", opencodeHome, "--json", "--query", "opencode")
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
	if len(payload.Sessions) != 1 || payload.Sessions[0].ID != "ses_opencode" {
		t.Fatalf("unexpected sessions: %#v", payload.Sessions)
	}
	if payload.Sessions[0].Provider != "opencode" {
		t.Fatalf("provider = %q, want opencode", payload.Sessions[0].Provider)
	}
	if len(payload.Projects) != 1 || payload.Projects[0].Count != 1 {
		t.Fatalf("unexpected projects: %#v", payload.Projects)
	}

	cmd := runCommand(t, "--codex-home", codexHome, "--claude-home", claudeHome, "--kimi-home", kimiHome, "--opencode-home", opencodeHome, "--resume", "ses_opencode", "--print-exec")
	if !strings.Contains(cmd, `cd '/data/code/openclaw/openclaw' && 'opencode' '-s' 'ses_opencode'`) {
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

func TestCLIReportYesterdayIncludesWindowedPreviews(t *testing.T) {
	home := t.TempDir()
	claudeHome := t.TempDir()
	sessionDir := filepath.Join(home, "sessions", "2026", "06", "13")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now().In(time.Local)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	yesterday := today.AddDate(0, 0, -1)
	ts := func(offset time.Duration) string {
		return yesterday.Add(offset).Format(time.RFC3339Nano)
	}
	inWindowPath := filepath.Join(sessionDir, "in-window.jsonl")
	endPath := filepath.Join(sessionDir, "at-end.jsonl")
	writeFile(t, inWindowPath, `{"timestamp":"`+yesterday.Add(-time.Hour).Format(time.RFC3339Nano)+`","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"stale report prompt"}]}}
{"timestamp":"`+ts(time.Hour)+`","type":"session_meta","payload":{"id":"report-session","timestamp":"`+ts(time.Hour)+`","cwd":"/repo/report"}}
{"timestamp":"`+ts(time.Hour+time.Second)+`","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"first report prompt"}]}}
{"timestamp":"`+ts(time.Hour+2*time.Second)+`","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"second report prompt"}]}}
{"timestamp":"`+ts(time.Hour+3*time.Second)+`","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"third report prompt"}]}}
{"timestamp":"`+ts(time.Hour+4*time.Second)+`","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fourth report prompt"}]}}
{"timestamp":"`+ts(time.Hour+5*time.Second)+`","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fifth report prompt"}]}}
`)
	writeSession(t, endPath, "excluded-session", "/repo/excluded")

	if err := os.Chtimes(inWindowPath, yesterday.Add(time.Hour), yesterday.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(endPath, today, today); err != nil {
		t.Fatal(err)
	}

	out := runCommand(t, "report", "--codex-home", home, "--claude-home", claudeHome, "--period", "yesterday")
	var payload struct {
		Period string `json:"period"`
		Totals struct {
			Sessions  int            `json:"sessions"`
			Projects  int            `json:"projects"`
			Providers map[string]int `json:"providers"`
		} `json:"totals"`
		Sessions []struct {
			ID            string `json:"id"`
			Provider      string `json:"provider"`
			ResumeCommand string `json:"resume_command"`
			Previews      []struct {
				Text string `json:"text"`
			} `json:"previews"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if payload.Period != "yesterday" {
		t.Fatalf("period = %q", payload.Period)
	}
	if payload.Totals.Sessions != 1 || payload.Totals.Projects != 1 || payload.Totals.Providers["codex"] != 1 {
		t.Fatalf("unexpected totals: %#v", payload.Totals)
	}
	if len(payload.Sessions) != 1 || payload.Sessions[0].ID != "report-session" {
		t.Fatalf("unexpected sessions: %#v", payload.Sessions)
	}
	if payload.Sessions[0].ResumeCommand != "asm resume --provider 'codex' 'report-session'" {
		t.Fatalf("resume_command = %q", payload.Sessions[0].ResumeCommand)
	}
	want := []string{"first report prompt", "second report prompt", "fourth report prompt", "fifth report prompt"}
	var previews []string
	for _, preview := range payload.Sessions[0].Previews {
		previews = append(previews, preview.Text)
	}
	if strings.Join(previews, "|") != strings.Join(want, "|") {
		t.Fatalf("previews = %#v, want %#v", previews, want)
	}

	out = runCommand(t, "report", "--codex-home", home, "--claude-home", claudeHome, "--period", "yesterday", "--preview-messages-per-edge", "3")
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	previews = previews[:0]
	for _, preview := range payload.Sessions[0].Previews {
		previews = append(previews, preview.Text)
	}
	want = []string{"first report prompt", "second report prompt", "third report prompt", "fourth report prompt", "fifth report prompt"}
	if strings.Join(previews, "|") != strings.Join(want, "|") {
		t.Fatalf("expanded previews = %#v, want %#v", previews, want)
	}

	out = runCommand(t, "report", "--codex-home", home, "--claude-home", claudeHome, "--period", "yesterday", "--preview-messages-per-edge", "2", "--preview-edge-offset", "2")
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	previews = previews[:0]
	for _, preview := range payload.Sessions[0].Previews {
		previews = append(previews, preview.Text)
	}
	want = []string{"third report prompt"}
	if strings.Join(previews, "|") != strings.Join(want, "|") {
		t.Fatalf("incremental previews = %#v, want %#v", previews, want)
	}
}

func TestCLIReportTodayIncludesSessionsThroughNow(t *testing.T) {
	home := t.TempDir()
	claudeHome := t.TempDir()
	sessionDir := filepath.Join(home, "sessions", "2026", "06", "18")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}

	now := time.Now().In(time.Local)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	if now.Sub(today) < 2*time.Second {
		t.Skip("too close to local midnight for stable today report timestamps")
	}
	inWindow := now.Add(-time.Second)
	future := now.Add(time.Hour)
	inWindowPath := filepath.Join(sessionDir, "today.jsonl")
	futurePath := filepath.Join(sessionDir, "future.jsonl")
	writeFile(t, inWindowPath, `{"timestamp":"`+inWindow.Format(time.RFC3339Nano)+`","type":"session_meta","payload":{"id":"today-session","timestamp":"`+inWindow.Format(time.RFC3339Nano)+`","cwd":"/repo/today"}}
{"timestamp":"`+inWindow.Format(time.RFC3339Nano)+`","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"today report prompt"}]}}
`)
	writeFile(t, futurePath, `{"timestamp":"`+future.Format(time.RFC3339Nano)+`","type":"session_meta","payload":{"id":"future-session","timestamp":"`+future.Format(time.RFC3339Nano)+`","cwd":"/repo/future"}}
{"timestamp":"`+future.Format(time.RFC3339Nano)+`","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"future report prompt"}]}}
`)
	if err := os.Chtimes(inWindowPath, inWindow, inWindow); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(futurePath, future, future); err != nil {
		t.Fatal(err)
	}

	out := runCommand(t, "report", "--codex-home", home, "--claude-home", claudeHome, "--period", "today")
	var payload struct {
		Period   string `json:"period"`
		Sessions []struct {
			ID       string `json:"id"`
			Previews []struct {
				Text string `json:"text"`
			} `json:"previews"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if payload.Period != "today" {
		t.Fatalf("period = %q", payload.Period)
	}
	if len(payload.Sessions) != 1 || payload.Sessions[0].ID != "today-session" {
		t.Fatalf("unexpected sessions: %#v", payload.Sessions)
	}
	if len(payload.Sessions[0].Previews) != 1 || payload.Sessions[0].Previews[0].Text != "today report prompt" {
		t.Fatalf("unexpected previews: %#v", payload.Sessions[0].Previews)
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
	cmdArgs := append([]string{"run", "./cmd/asm"}, args...)
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
		"OPENCODE_HOME="+t.TempDir(),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("command failed: %v\n%s", err, string(out))
	}
	return string(out)
}

func writeSession(t *testing.T, path, id, cwd string) {
	t.Helper()
	writeFile(t, path, `{"timestamp":"2026-06-13T01:00:00Z","type":"session_meta","payload":{"id":`+jsonString(id)+`,"timestamp":"2026-06-13T01:00:00Z","cwd":`+jsonString(cwd)+`}}
`)
}

func writeClaudeSession(t *testing.T, path, id, cwd, title string) {
	t.Helper()
	writeFile(t, path, `{"type":"user","sessionId":`+jsonString(id)+`,"cwd":`+jsonString(cwd)+`,"timestamp":"2026-06-13T01:00:00Z","message":{"role":"user","content":`+jsonString(title)+`}}
`)
}

func writeKimiSession(t *testing.T, home, sessionDir, id, cwd, title string) {
	t.Helper()
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(home, "session_index.jsonl"), `{"sessionId":`+jsonString(id)+`,"sessionDir":`+jsonString(sessionDir)+`,"workDir":`+jsonString(cwd)+`}
`)
	writeFile(t, filepath.Join(sessionDir, "state.json"), `{"createdAt":"2026-06-13T01:00:00Z","updatedAt":"2026-06-13T01:01:00Z","title":`+jsonString(title)+`}
`)
}

func writeOpencodeSession(t *testing.T, home, projectID, id, cwd, title string) {
	t.Helper()
	sessionDir := filepath.Join(home, "storage", "session", projectID)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	projectDir := filepath.Join(home, "storage", "project")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(projectDir, projectID+".json"), `{"id":`+jsonString(projectID)+`,"worktree":`+jsonString(cwd)+`,"time":{"created":1781322000000,"updated":1781322000000}}
`)
	writeFile(t, filepath.Join(sessionDir, id+".json"), `{"id":`+jsonString(id)+`,"projectID":`+jsonString(projectID)+`,"directory":`+jsonString(cwd)+`,"title":`+jsonString(title)+`,"time":{"created":1781322000000,"updated":1781322060000}}
`)
}

func jsonString(value string) string {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(data)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
