package tests

import (
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestCLIIndexesSearchesAndPrintsResumeCommand(t *testing.T) {
	home := t.TempDir()
	claudeHome := t.TempDir()
	repo := t.TempDir()
	helperRepo := t.TempDir()
	sessionDir := filepath.Join(home, "sessions", "2026", "06", "13")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}

	writeSession(t, filepath.Join(sessionDir, "openclaw.jsonl"), "openclaw-session", repo)
	writeSession(t, filepath.Join(sessionDir, "helper.jsonl"), "helper-session", helperRepo)
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
	if len(payload.Projects) != 1 || payload.Projects[0].CWD != repo || payload.Projects[0].Count != 1 {
		t.Fatalf("unexpected projects: %#v", payload.Projects)
	}

	cmd := runCommand(t, "--codex-home", home, "--claude-home", claudeHome, "--resume", "openclaw-session", "--print-exec")
	if !strings.Contains(cmd, `cd '`+repo+`' && 'codex' 'resume' 'openclaw-session'`) {
		t.Fatalf("unexpected resume command: %s", cmd)
	}

	cmd = runCommand(t, "--codex-home", home, "--codex-profile", "ollama-cloud", "--claude-home", claudeHome, "--resume", "openclaw-session", "--print-exec")
	if !strings.Contains(cmd, `cd '`+repo+`' && 'codex' 'resume' '--profile' 'ollama-cloud' 'openclaw-session'`) {
		t.Fatalf("unexpected profiled resume command: %s", cmd)
	}

	cmd = runCommand(t, "resume", "--codex-home", home, "--claude-home", claudeHome, "--provider", "codex", "--print-exec", "openclaw-session")
	if !strings.Contains(cmd, `cd '`+repo+`' && 'codex' 'resume' 'openclaw-session'`) {
		t.Fatalf("unexpected resume subcommand: %s", cmd)
	}

	cmd = runCommand(t, "resume", "--codex-home", home, "--codex-profile", "ollama-cloud", "--claude-home", claudeHome, "--provider", "codex", "--print-exec", "openclaw-session")
	if !strings.Contains(cmd, `cd '`+repo+`' && 'codex' 'resume' '--profile' 'ollama-cloud' 'openclaw-session'`) {
		t.Fatalf("unexpected profiled resume subcommand: %s", cmd)
	}
}

func TestCLIIndexesClaudeAndPrintsResumeCommand(t *testing.T) {
	codexHome := t.TempDir()
	claudeHome := t.TempDir()
	repo := t.TempDir()
	claudeDir := filepath.Join(claudeHome, "projects", "-data-code-openclaw-openclaw")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeClaudeSession(t, filepath.Join(claudeDir, "claude-session.jsonl"), "claude-session", repo, "fix openclaw with claude")

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
	if !strings.Contains(cmd, `cd '`+repo+`' && 'claude' '--resume' 'claude-session'`) {
		t.Fatalf("unexpected resume command: %s", cmd)
	}
}

func TestCLIIndexesKimiAndPrintsResumeCommand(t *testing.T) {
	codexHome := t.TempDir()
	claudeHome := t.TempDir()
	kimiHome := t.TempDir()
	repo := t.TempDir()
	kimiDir := filepath.Join(kimiHome, "sessions", "wd_openclaw", "ses_kimi")
	writeKimiSession(t, kimiHome, kimiDir, "ses_kimi", repo, "fix openclaw with kimi")

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
	if !strings.Contains(cmd, `cd '`+repo+`' && 'kimi' '--session' 'ses_kimi'`) {
		t.Fatalf("unexpected resume command: %s", cmd)
	}
}

func TestCLIIndexesOpencodeAndPrintsResumeCommand(t *testing.T) {
	codexHome := t.TempDir()
	claudeHome := t.TempDir()
	kimiHome := t.TempDir()
	opencodeHome := t.TempDir()
	repo := t.TempDir()
	writeOpencodeSession(t, opencodeHome, "project_one", "ses_opencode", repo, "fix openclaw with opencode")

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
	if !strings.Contains(cmd, `cd '`+repo+`' && 'opencode' '-s' 'ses_opencode'`) {
		t.Fatalf("unexpected resume command: %s", cmd)
	}
}

func TestCLIIndexesCodeBuddyAndPrintsResumeCommand(t *testing.T) {
	codexHome := t.TempDir()
	claudeHome := t.TempDir()
	kimiHome := t.TempDir()
	opencodeHome := t.TempDir()
	codebuddyHome := t.TempDir()
	repo := t.TempDir()
	writeCodeBuddySession(t, codebuddyHome, "ses_codebuddy", repo, "fix openclaw with codebuddy")

	out := runCommand(t, "--codex-home", codexHome, "--claude-home", claudeHome, "--kimi-home", kimiHome, "--opencode-home", opencodeHome, "--codebuddy-home", codebuddyHome, "--json", "--query", "codebuddy")
	var payload struct {
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
	if len(payload.Sessions) != 1 || payload.Sessions[0].ID != "ses_codebuddy" || payload.Sessions[0].Provider != "codebuddy" {
		t.Fatalf("unexpected sessions: %#v", payload.Sessions)
	}

	cmd := runCommand(t, "--codex-home", codexHome, "--claude-home", claudeHome, "--kimi-home", kimiHome, "--opencode-home", opencodeHome, "--codebuddy-home", codebuddyHome, "--resume", "ses_codebuddy", "--print-exec")
	if !strings.Contains(cmd, `cd '`+repo+`' && 'codebuddy' '--resume' 'ses_codebuddy'`) {
		t.Fatalf("unexpected resume command: %s", cmd)
	}
}

func TestCLIIndexesZCodeAndPrintsResumeCommand(t *testing.T) {
	codexHome := t.TempDir()
	claudeHome := t.TempDir()
	kimiHome := t.TempDir()
	opencodeHome := t.TempDir()
	zcodeHome := t.TempDir()
	repo := t.TempDir()
	writeZCodeSession(t, zcodeHome, "ses_zcode", repo, "fix openclaw with zcode")

	out := runCommand(t, "--codex-home", codexHome, "--claude-home", claudeHome, "--kimi-home", kimiHome, "--opencode-home", opencodeHome, "--zcode-home", zcodeHome, "--json", "--query", "zcode")
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
	if len(payload.Sessions) != 1 || payload.Sessions[0].ID != "ses_zcode" {
		t.Fatalf("unexpected sessions: %#v", payload.Sessions)
	}
	if payload.Sessions[0].Provider != "zcode" {
		t.Fatalf("provider = %q, want zcode", payload.Sessions[0].Provider)
	}
	if payload.Sessions[0].Title != "fix openclaw with zcode" {
		t.Fatalf("title = %q", payload.Sessions[0].Title)
	}
	if len(payload.Projects) != 1 || payload.Projects[0].CWD != repo || payload.Projects[0].Count != 1 {
		t.Fatalf("unexpected projects: %#v", payload.Projects)
	}

	cmd := runCommand(t, "--codex-home", codexHome, "--claude-home", claudeHome, "--kimi-home", kimiHome, "--opencode-home", opencodeHome, "--zcode-home", zcodeHome, "--resume", "ses_zcode", "--print-exec")
	if !strings.Contains(cmd, `cd '`+repo+`' && 'zcode' '--resume' 'ses_zcode'`) {
		t.Fatalf("unexpected resume command: %s", cmd)
	}
}

func TestCLIIndexesCursorAndPrintsResumeCommand(t *testing.T) {
	codexHome := t.TempDir()
	claudeHome := t.TempDir()
	kimiHome := t.TempDir()
	opencodeHome := t.TempDir()
	codebuddyHome := t.TempDir()
	cursorHome := t.TempDir()
	repo := filepath.Join(cursorHome, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	writeCursorSession(t, cursorHome, "cursor-chat", repo, "fix openclaw with cursor")

	out := runCommand(t, "--codex-home", codexHome, "--claude-home", claudeHome, "--kimi-home", kimiHome, "--opencode-home", opencodeHome, "--codebuddy-home", codebuddyHome, "--cursor-home", cursorHome, "--json", "--query", "cursor")
	var payload struct {
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
	if len(payload.Sessions) != 1 || payload.Sessions[0].ID != "cursor-chat" || payload.Sessions[0].Provider != "cursor" || payload.Sessions[0].CWD != repo {
		t.Fatalf("unexpected sessions: %#v", payload.Sessions)
	}

	cmd := runCommand(t, "--codex-home", codexHome, "--claude-home", claudeHome, "--kimi-home", kimiHome, "--opencode-home", opencodeHome, "--codebuddy-home", codebuddyHome, "--cursor-home", cursorHome, "--resume", "cursor-chat", "--print-exec")
	if !strings.Contains(cmd, `'cursor-agent' '--resume' 'cursor-chat'`) {
		t.Fatalf("unexpected resume command: %s", cmd)
	}
}

func TestCLIIndexesOpenClawAndRejectsResume(t *testing.T) {
	codexHome := t.TempDir()
	claudeHome := t.TempDir()
	kimiHome := t.TempDir()
	opencodeHome := t.TempDir()
	codebuddyHome := t.TempDir()
	cursorHome := t.TempDir()
	openclawHome := t.TempDir()
	writeOpenClawSession(t, openclawHome, "agent:main:main", "native-openclaw", "OpenClaw indexed session")

	out := runCommand(t, "--codex-home", codexHome, "--claude-home", claudeHome, "--kimi-home", kimiHome, "--opencode-home", opencodeHome, "--codebuddy-home", codebuddyHome, "--cursor-home", cursorHome, "--openclaw-home", openclawHome, "--json", "--query", "indexed")
	var payload struct {
		Sessions []struct {
			ID       string            `json:"id"`
			Provider string            `json:"provider"`
			Title    string            `json:"title"`
			Metadata map[string]string `json:"metadata"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if len(payload.Sessions) != 1 || payload.Sessions[0].ID != "agent:main:main" || payload.Sessions[0].Provider != "openclaw" {
		t.Fatalf("unexpected sessions: %#v", payload.Sessions)
	}
	if payload.Sessions[0].Metadata["native_session_id"] != "native-openclaw" {
		t.Fatalf("metadata = %#v", payload.Sessions[0].Metadata)
	}

	out, err := runCommandAllowError(t, "--codex-home", codexHome, "--claude-home", claudeHome, "--kimi-home", kimiHome, "--opencode-home", opencodeHome, "--codebuddy-home", codebuddyHome, "--cursor-home", cursorHome, "--openclaw-home", openclawHome, "--resume", "agent:main:main", "--print-exec")
	if err == nil {
		t.Fatalf("expected unsupported resume error, got output: %s", out)
	}
	if !strings.Contains(out, "OpenClaw resume is not supported by asm yet") {
		t.Fatalf("unexpected unsupported resume output: %s", out)
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
	repo := t.TempDir()
	writeFile(t, inWindowPath, `{"timestamp":"`+yesterday.Add(-time.Hour).Format(time.RFC3339Nano)+`","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"stale report prompt"}]}}
{"timestamp":"`+ts(time.Hour)+`","type":"session_meta","payload":{"id":"report-session","timestamp":"`+ts(time.Hour)+`","cwd":`+jsonString(repo)+`}}
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
	repo := t.TempDir()
	futureRepo := t.TempDir()
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
	writeFile(t, inWindowPath, `{"timestamp":"`+inWindow.Format(time.RFC3339Nano)+`","type":"session_meta","payload":{"id":"today-session","timestamp":"`+inWindow.Format(time.RFC3339Nano)+`","cwd":`+jsonString(repo)+`}}
{"timestamp":"`+inWindow.Format(time.RFC3339Nano)+`","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"today report prompt"}]}}
`)
	writeFile(t, futurePath, `{"timestamp":"`+future.Format(time.RFC3339Nano)+`","type":"session_meta","payload":{"id":"future-session","timestamp":"`+future.Format(time.RFC3339Nano)+`","cwd":`+jsonString(futureRepo)+`}}
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
			Evidence []struct {
				Text string `json:"text"`
			} `json:"evidence"`
			EvidenceCount int `json:"evidence_count"`
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

func TestCLIReportCustomRangeIncludesWindowedPreviews(t *testing.T) {
	home := t.TempDir()
	claudeHome := t.TempDir()
	repo := t.TempDir()
	sessionDir := filepath.Join(home, "sessions", "2026", "06", "18")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}

	loc := time.Local
	start := time.Date(2026, 6, 18, 9, 0, 0, 0, loc)
	end := time.Date(2026, 6, 18, 12, 0, 0, 0, loc)
	sessionPath := filepath.Join(sessionDir, "custom-range.jsonl")
	writeFile(t, sessionPath, `{"timestamp":"`+start.Add(time.Hour).Format(time.RFC3339Nano)+`","type":"session_meta","payload":{"id":"custom-range-session","timestamp":"`+start.Add(time.Hour).Format(time.RFC3339Nano)+`","cwd":`+jsonString(repo)+`}}
{"timestamp":"`+start.Add(-time.Second).Format(time.RFC3339Nano)+`","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"before custom range"}]}}
{"timestamp":"`+start.Format(time.RFC3339Nano)+`","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"inside custom start"}]}}
{"timestamp":"`+end.Add(-time.Second).Format(time.RFC3339Nano)+`","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"inside custom end"}]}}
{"timestamp":"`+end.Format(time.RFC3339Nano)+`","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"outside custom end"}]}}
`)
	if err := os.Chtimes(sessionPath, start.Add(time.Hour), start.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	out := runCommand(t, "report", "--codex-home", home, "--claude-home", claudeHome, "--start", "2026-06-18 09:00", "--end", "2026-06-18 12:00")
	var payload struct {
		Period   string `json:"period"`
		Sessions []struct {
			ID       string `json:"id"`
			Previews []struct {
				Text string `json:"text"`
			} `json:"previews"`
			Evidence []struct {
				Text string `json:"text"`
			} `json:"evidence"`
			EvidenceCount int `json:"evidence_count"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if payload.Period != "custom" {
		t.Fatalf("period = %q", payload.Period)
	}
	if len(payload.Sessions) != 1 || payload.Sessions[0].ID != "custom-range-session" {
		t.Fatalf("unexpected sessions: %#v", payload.Sessions)
	}
	var previews []string
	for _, preview := range payload.Sessions[0].Previews {
		previews = append(previews, preview.Text)
	}
	want := []string{"inside custom start", "inside custom end"}
	if strings.Join(previews, "|") != strings.Join(want, "|") {
		t.Fatalf("previews = %#v, want %#v", previews, want)
	}
	var evidence []string
	for _, item := range payload.Sessions[0].Evidence {
		evidence = append(evidence, item.Text)
	}
	if payload.Sessions[0].EvidenceCount != len(want) || strings.Join(evidence, "|") != strings.Join(want, "|") {
		t.Fatalf("evidence = %#v count=%d, want %#v", evidence, payload.Sessions[0].EvidenceCount, want)
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
	out, err := runCommandAllowError(t, args...)
	if err != nil {
		t.Fatalf("command failed: %v\n%s", err, out)
	}
	return out
}

func runCommandAllowError(t *testing.T, args ...string) (string, error) {
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
		"CODEBUDDY_HOME="+t.TempDir(),
		"CURSOR_HOME="+t.TempDir(),
		"OPENCLAW_STATE_DIR="+t.TempDir(),
		"ASM_CODEX_EXTRA_HOMES=",
		"ASM_CLAUDE_EXTRA_HOMES=",
		"ZCODE_HOME="+t.TempDir(),
	)
	out, err := cmd.CombinedOutput()
	return string(out), err
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

func writeCodeBuddySession(t *testing.T, home, id, cwd, title string) {
	t.Helper()
	writeFile(t, filepath.Join(home, "projects", "repo", id+".jsonl"), `{"sessionId":`+jsonString(id)+`,"cwd":`+jsonString(cwd)+`,"timestamp":"2026-06-13T01:00:00Z","ai-title":`+jsonString(title)+`,"model":"codebuddy"}
`)
}

func writeCursorSession(t *testing.T, home, id, cwd, title string) {
	t.Helper()
	projectKey := "project-" + id
	writeFile(t, filepath.Join(home, "projects", projectKey, "worker.log"), `[info] Getting tree structure for workspacePath=`+cwd+`
`)
	writeFile(t, filepath.Join(home, "projects", projectKey, "agent-transcripts", id, id+".jsonl"), `{"role":"user","message":{"content":[{"type":"text","text":`+jsonString(title)+`}]}}
`)
}

func writeOpenClawSession(t *testing.T, stateDir, id, nativeID, title string) {
	t.Helper()
	writeFile(t, filepath.Join(stateDir, "agents", "main", "sessions", "sessions.json"), `{
  `+jsonString(id)+`: {
    "sessionId": `+jsonString(nativeID)+`,
    "updatedAt": 1781312460000,
    "displayName": `+jsonString(title)+`
	  }
	}`)
}

func writeZCodeSession(t *testing.T, home, id, cwd, title string) {
	t.Helper()
	dbDir := filepath.Join(home, "cli", "db")
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dbDir, "db.sqlite")
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	schema := `
CREATE TABLE session (
  id text primary key,
  project_id text not null,
  workspace_id text,
  parent_id text,
  slug text not null,
  directory text not null,
  path text,
  title text not null,
  version text not null,
  time_created integer not null,
  time_updated integer not null,
  time_archived integer,
  title_source text not null default 'default'
);
CREATE TABLE message (
  id text primary key,
  session_id text not null,
  time_created integer not null,
  time_updated integer not null,
  data text not null
);
CREATE TABLE part (
  id text primary key,
  message_id text not null,
  session_id text not null,
  time_created integer not null,
  time_updated integer not null,
  data text not null
);
`
	if _, err := db.Exec(schema); err != nil {
		t.Fatal(err)
	}

	created := int64(1781322000000)
	updated := int64(1781322060000)
	if _, err := db.Exec(`INSERT INTO session (id, project_id, slug, directory, title, version, time_created, time_updated, title_source)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, "proj_"+id, id, cwd, title, "1", created, updated, "generated"); err != nil {
		t.Fatal(err)
	}

	msgData, _ := json.Marshal(map[string]any{"role": "user", "time": map[string]any{"created": created}})
	if _, err := db.Exec(`INSERT INTO message (id, session_id, time_created, time_updated, data) VALUES (?, ?, ?, ?, ?)`,
		"msg_"+id, id, created, created, string(msgData)); err != nil {
		t.Fatal(err)
	}
	partData, _ := json.Marshal(map[string]any{"type": "text", "text": title})
	if _, err := db.Exec(`INSERT INTO part (id, message_id, session_id, time_created, time_updated, data) VALUES (?, ?, ?, ?, ?, ?)`,
		"part_"+id, "msg_"+id, id, created, created, string(partData)); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
