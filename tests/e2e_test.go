package tests

import (
	"database/sql"
	"encoding/json"
	"fmt"
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

	out := runCommand(t, "--codex-home", home, "--claude-home", claudeHome, "--since-days", "0", "--json", "--query", "openclaw")
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

	cmd := runCommand(t, "--codex-home", home, "--claude-home", claudeHome, "--since-days", "0", "--resume", "openclaw-session", "--print-exec")
	if !strings.Contains(cmd, `cd '`+repo+`' && 'codex' 'resume' 'openclaw-session'`) {
		t.Fatalf("unexpected resume command: %s", cmd)
	}

	cmd = runCommand(t, "--codex-home", home, "--codex-profile", "ollama-cloud", "--claude-home", claudeHome, "--since-days", "0", "--resume", "openclaw-session", "--print-exec")
	if !strings.Contains(cmd, `cd '`+repo+`' && 'codex' 'resume' '--profile' 'ollama-cloud' 'openclaw-session'`) {
		t.Fatalf("unexpected profiled resume command: %s", cmd)
	}

	cmd = runCommand(t, "resume", "--codex-home", home, "--claude-home", claudeHome, "--since-days", "0", "--provider", "codex", "--print-exec", "openclaw-session")
	if !strings.Contains(cmd, `cd '`+repo+`' && 'codex' 'resume' 'openclaw-session'`) {
		t.Fatalf("unexpected resume subcommand: %s", cmd)
	}

	cmd = runCommand(t, "resume", "--codex-home", home, "--codex-profile", "ollama-cloud", "--claude-home", claudeHome, "--since-days", "0", "--provider", "codex", "--print-exec", "openclaw-session")
	if !strings.Contains(cmd, `cd '`+repo+`' && 'codex' 'resume' '--profile' 'ollama-cloud' 'openclaw-session'`) {
		t.Fatalf("unexpected profiled resume subcommand: %s", cmd)
	}
}

func TestCLIKeepsCodexSubagentSeparateFromInheritedParentHistory(t *testing.T) {
	home := t.TempDir()
	claudeHome := t.TempDir()
	parentRepo := t.TempDir()
	childRepo := t.TempDir()
	childWorktree := t.TempDir()
	sessionDir := filepath.Join(home, "sessions", "2026", "06", "13")

	parentPath := filepath.Join(sessionDir, "parent.jsonl")
	childPath := filepath.Join(sessionDir, "child.jsonl")
	writeFile(t, parentPath, `{"timestamp":"2026-06-13T01:00:00Z","type":"session_meta","payload":{"id":"parent","timestamp":"2026-06-13T01:00:00Z","cwd":`+jsonString(parentRepo)+`}}
{"timestamp":"2026-06-13T01:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"parent user work"}]}}
`)
	// Codex subagent rollouts start with child-owned records, then replay parent
	// history. This ordering locks in both the identity and context boundaries.
	writeFile(t, childPath, `{"timestamp":"2026-06-13T02:00:00Z","type":"session_meta","payload":{"id":"child","parent_thread_id":"parent","timestamp":"2026-06-13T02:00:00Z","cwd":`+jsonString(childRepo)+`}}
{"timestamp":"2026-06-13T02:00:01Z","type":"session_meta","payload":{"id":"child","parent_thread_id":"parent","timestamp":"2026-06-13T02:00:00Z","cwd":`+jsonString(childRepo)+`}}
{"timestamp":"2026-06-13T02:00:02Z","type":"turn_context","payload":{"cwd":`+jsonString(childWorktree)+`,"model":"gpt-5"}}
{"timestamp":"2026-06-13T02:00:03Z","type":"session_meta","payload":{"id":"parent","timestamp":"2026-06-13T01:00:00Z","cwd":`+jsonString(parentRepo)+`}}
{"timestamp":"2026-06-13T02:00:04Z","type":"turn_context","payload":{"cwd":`+jsonString(parentRepo)+`,"model":"gpt-4"}}
{"timestamp":"2026-06-13T02:00:05Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"inherited parent user work"}]}}
`)
	writeFile(t, filepath.Join(home, "session_index.jsonl"), `{"id":"parent","thread_name":"Parent thread","updated_at":"2026-06-13T01:00:00Z"}
{"id":"child","thread_name":"Child subagent","updated_at":"2026-06-13T02:00:00Z"}
`)
	parentTime := time.Date(2026, 6, 13, 1, 0, 0, 0, time.UTC)
	if err := os.Chtimes(parentPath, parentTime, parentTime); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(childPath, parentTime.Add(time.Hour), parentTime.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	out := runCommand(t, "--codex-home", home, "--claude-home", claudeHome, "--since-days", "0", "--json")
	type sessionJSON struct {
		ID       string            `json:"id"`
		CWD      string            `json:"cwd"`
		Title    string            `json:"title"`
		Path     string            `json:"path"`
		Metadata map[string]string `json:"metadata"`
	}
	var payload struct {
		Sessions []sessionJSON `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if len(payload.Sessions) != 2 {
		t.Fatalf("sessions = %#v, want separate parent and child sessions", payload.Sessions)
	}
	byID := make(map[string]sessionJSON, len(payload.Sessions))
	for _, item := range payload.Sessions {
		byID[item.ID] = item
	}
	if got := byID["parent"]; got.CWD != parentRepo || got.Title != "Parent thread" || got.Path != parentPath {
		t.Fatalf("parent = %#v", got)
	}
	if got := byID["child"]; got.CWD != childWorktree || got.Title != "Child subagent" || got.Path != childPath || got.Metadata["parent_thread_id"] != "parent" || got.Metadata["model"] != "gpt-5" {
		t.Fatalf("child = %#v", got)
	}

	cmd := runCommand(t, "--codex-home", home, "--claude-home", claudeHome, "--since-days", "0", "--resume", "child", "--print-exec")
	if !strings.Contains(cmd, `cd '`+childWorktree+`' && 'codex' 'resume' 'child'`) {
		t.Fatalf("unexpected child resume command: %s", cmd)
	}

	out = runCommand(t, "report", "--codex-home", home, "--claude-home", claudeHome, "--start", "2026-06-13", "--end", "2026-06-14")
	var reportPayload struct {
		Totals struct {
			Sessions int `json:"sessions"`
		} `json:"totals"`
		Sessions []struct {
			ID string `json:"id"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(out), &reportPayload); err != nil {
		t.Fatalf("invalid report JSON: %v\n%s", err, out)
	}
	if reportPayload.Totals.Sessions != 1 || len(reportPayload.Sessions) != 1 || reportPayload.Sessions[0].ID != "parent" {
		t.Fatalf("report should count only parent user work: %#v", reportPayload)
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

func TestCLIIndexesKiroAndPrintsResumeCommand(t *testing.T) {
	providerArgs := []string{
		"--codex-home", t.TempDir(),
		"--claude-home", t.TempDir(),
		"--kimi-home", t.TempDir(),
		"--opencode-home", t.TempDir(),
		"--codebuddy-home", t.TempDir(),
		"--cursor-home", t.TempDir(),
		"--openclaw-home", t.TempDir(),
		"--zcode-home", t.TempDir(),
	}
	kiroHome := t.TempDir()
	repo := t.TempDir()
	writeKiroSession(t, kiroHome, "ses_kiro", repo, "fix openclaw with kiro")
	runKiroCommand := func(extra ...string) string {
		args := append(append([]string{}, providerArgs...), "--kiro-home", kiroHome)
		return runCommand(t, append(args, extra...)...)
	}

	out := runKiroCommand("--since-days", "0", "--json", "--query", "kiro")
	var payload struct {
		Projects []struct {
			CWD   string `json:"cwd"`
			Count int    `json:"count"`
		} `json:"projects"`
		Sessions []struct {
			ID       string            `json:"id"`
			Provider string            `json:"provider"`
			CWD      string            `json:"cwd"`
			Title    string            `json:"title"`
			Metadata map[string]string `json:"metadata"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if len(payload.Sessions) != 1 || payload.Sessions[0].ID != "ses_kiro" {
		t.Fatalf("unexpected sessions: %#v", payload.Sessions)
	}
	item := payload.Sessions[0]
	if item.Provider != "kiro" || item.CWD != repo || item.Title != "fix openclaw with kiro" {
		t.Fatalf("unexpected Kiro session: %#v", item)
	}
	if item.Metadata["session_created_reason"] != "user" {
		t.Fatalf("metadata = %#v", item.Metadata)
	}
	if len(payload.Projects) != 1 || payload.Projects[0].CWD != repo || payload.Projects[0].Count != 1 {
		t.Fatalf("unexpected projects: %#v", payload.Projects)
	}

	cmd := runKiroCommand("--since-days", "0", "--resume", "ses_kiro", "--print-exec")
	if !strings.Contains(cmd, `cd '`+repo+`' && 'kiro-cli' 'chat' '--resume-id' 'ses_kiro'`) {
		t.Fatalf("unexpected resume command: %s", cmd)
	}

	reportArgs := append([]string{"report"}, providerArgs...)
	reportArgs = append(reportArgs, "--kiro-home", kiroHome, "--start", "2026-06-13", "--end", "2026-06-14")
	out = runCommand(t, reportArgs...)
	var report struct {
		Totals struct {
			Sessions int `json:"sessions"`
		} `json:"totals"`
		Sessions []struct {
			ID       string `json:"id"`
			Provider string `json:"provider"`
			Evidence []struct {
				Text string `json:"text"`
			} `json:"evidence"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("invalid report JSON: %v\n%s", err, out)
	}
	if report.Totals.Sessions != 1 || len(report.Sessions) != 1 || report.Sessions[0].ID != "ses_kiro" || report.Sessions[0].Provider != "kiro" {
		t.Fatalf("unexpected Kiro report: %#v", report)
	}
	if len(report.Sessions[0].Evidence) != 1 || report.Sessions[0].Evidence[0].Text != "fix openclaw with kiro" {
		t.Fatalf("unexpected Kiro evidence: %#v", report.Sessions[0].Evidence)
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

	out := runCommand(t, "--codex-home", codexHome, "--claude-home", claudeHome, "--kimi-home", kimiHome, "--opencode-home", opencodeHome, "--zcode-home", zcodeHome, "--since-days", "0", "--json", "--query", "zcode")
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

	cmd := runCommand(t, "--codex-home", codexHome, "--claude-home", claudeHome, "--kimi-home", kimiHome, "--opencode-home", opencodeHome, "--zcode-home", zcodeHome, "--since-days", "0", "--resume", "ses_zcode", "--print-exec")
	if !strings.Contains(cmd, `cd '`+repo+`' && 'zcode' '--resume' 'ses_zcode'`) {
		t.Fatalf("unexpected resume command: %s", cmd)
	}
}

func TestCLIKeepsHealthyProviderResultsWhenAnotherProviderFails(t *testing.T) {
	codexHome := t.TempDir()
	claudeHome := t.TempDir()
	zcodeHome := t.TempDir()
	repo := t.TempDir()
	sessionPath := filepath.Join(codexHome, "sessions", "2026", "06", "13", "healthy.jsonl")
	writeFile(t, sessionPath, `{"timestamp":"2026-06-13T01:00:00Z","type":"session_meta","payload":{"id":"healthy-codex","timestamp":"2026-06-13T01:00:00Z","cwd":`+jsonString(repo)+`}}
{"timestamp":"2026-06-13T01:01:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"healthy provider work"}]}}
`)
	writeFile(t, filepath.Join(zcodeHome, "cli", "db", "db.sqlite"), "not a sqlite database")

	assertPartialResult := func(t *testing.T, out string) {
		t.Helper()
		var payload struct {
			Sessions []struct {
				ID       string `json:"id"`
				Provider string `json:"provider"`
			} `json:"sessions"`
			ProviderErrors []struct {
				Provider string `json:"provider"`
				Error    string `json:"error"`
			} `json:"provider_errors"`
		}
		if err := json.Unmarshal([]byte(out), &payload); err != nil {
			t.Fatalf("invalid JSON: %v\n%s", err, out)
		}
		if len(payload.Sessions) != 1 ||
			payload.Sessions[0].ID != "healthy-codex" ||
			payload.Sessions[0].Provider != "codex" {
			t.Fatalf("healthy session was hidden: %#v", payload.Sessions)
		}
		if len(payload.ProviderErrors) != 1 ||
			payload.ProviderErrors[0].Provider != "zcode" ||
			!strings.Contains(payload.ProviderErrors[0].Error, "not a database") {
			t.Fatalf("provider errors = %#v", payload.ProviderErrors)
		}
	}

	out, err := runCommandAllowError(t,
		"--codex-home", codexHome,
		"--claude-home", claudeHome,
		"--zcode-home", zcodeHome,
		"--since-days", "0",
		"--json")
	if err != nil {
		t.Fatalf("partial JSON discovery failed: %v\n%s", err, out)
	}
	assertPartialResult(t, out)

	out, err = runCommandAllowError(t,
		"report",
		"--codex-home", codexHome,
		"--claude-home", claudeHome,
		"--zcode-home", zcodeHome,
		"--start", "2026-06-13",
		"--end", "2026-06-14")
	if err != nil {
		t.Fatalf("partial report discovery failed: %v\n%s", err, out)
	}
	assertPartialResult(t, out)

	out, err = runCommandAllowError(t,
		"resume",
		"--codex-home", codexHome,
		"--zcode-home", zcodeHome,
		"--provider", "codex",
		"--since-days", "0",
		"--print-exec",
		"healthy-codex")
	if err != nil {
		t.Fatalf("targeted healthy resume failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "'codex' 'resume' 'healthy-codex'") {
		t.Fatalf("unexpected targeted resume command: %s", out)
	}

	out, err = runCommandAllowError(t,
		"resume",
		"--codex-home", codexHome,
		"--zcode-home", zcodeHome,
		"--since-days", "0",
		"--print-exec",
		"healthy-codex")
	if err == nil ||
		!strings.Contains(out, "cannot safely resolve unqualified session") ||
		!strings.Contains(out, "pass --provider") {
		t.Fatalf("unqualified resume ignored incomplete discovery: err=%v output=%q", err, out)
	}

	out, err = runCommandAllowError(t,
		"resume",
		"--zcode-home", zcodeHome,
		"--provider", "zcode",
		"--since-days", "0",
		"--print-exec",
		"unavailable-zcode")
	if err == nil || !strings.Contains(out, "zcode discover:") || !strings.Contains(out, "not a database") {
		t.Fatalf("targeted failing resume did not surface provider error: err=%v output=%q", err, out)
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

	out := runCommand(t, "--codex-home", codexHome, "--claude-home", claudeHome, "--kimi-home", kimiHome, "--opencode-home", opencodeHome, "--codebuddy-home", codebuddyHome, "--cursor-home", cursorHome, "--openclaw-home", openclawHome, "--since-days", "0", "--json", "--query", "indexed")
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

	out, err := runCommandAllowError(t, "--codex-home", codexHome, "--claude-home", claudeHome, "--kimi-home", kimiHome, "--opencode-home", opencodeHome, "--codebuddy-home", codebuddyHome, "--cursor-home", cursorHome, "--openclaw-home", openclawHome, "--since-days", "0", "--resume", "agent:main:main", "--print-exec")
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

func TestCLIReportAppliesLimitAfterWindowEvidenceSelection(t *testing.T) {
	codexHome := t.TempDir()
	claudeHome := t.TempDir()
	repo := t.TempDir()
	sessionDir := filepath.Join(codexHome, "sessions", "2026", "07", "10")
	start := time.Date(2026, 7, 10, 0, 0, 0, 0, time.Local)
	end := start.AddDate(0, 0, 1)

	writeReportSession := func(id, text string, at time.Time) {
		t.Helper()
		path := filepath.Join(sessionDir, id+".jsonl")
		writeFile(t, path,
			`{"timestamp":`+jsonString(at.Format(time.RFC3339Nano))+`,"type":"session_meta","payload":{"id":`+jsonString(id)+`,"timestamp":`+jsonString(at.Format(time.RFC3339Nano))+`,"cwd":`+jsonString(repo)+`}}`+"\n"+
				`{"timestamp":`+jsonString(at.Add(time.Second).Format(time.RFC3339Nano))+`,"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":`+jsonString(text)+`}]}}`+"\n")
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatal(err)
		}
	}

	writeReportSession("window-old", "older in-window work", start.Add(9*time.Hour))
	writeReportSession("window-new", "newer in-window work", start.Add(10*time.Hour))
	writeReportSession("after-window", "work after the requested window", end.Add(time.Hour))

	out := runCommand(t, "report",
		"--codex-home", codexHome,
		"--claude-home", claudeHome,
		"--start", start.Format("2006-01-02"),
		"--end", end.Format("2006-01-02"),
		"--limit", "1")
	var payload struct {
		Totals struct {
			Sessions int `json:"sessions"`
		} `json:"totals"`
		Sessions []struct {
			ID string `json:"id"`
		} `json:"sessions"`
		Coverage map[string]struct {
			Status           string `json:"status"`
			Truncated        bool   `json:"truncated"`
			MatchedSessions  int    `json:"matched_sessions"`
			IncludedSessions int    `json:"included_sessions"`
		} `json:"coverage"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("invalid report JSON: %v\n%s", err, out)
	}
	if payload.Totals.Sessions != 1 || len(payload.Sessions) != 1 || payload.Sessions[0].ID != "window-new" {
		t.Fatalf("post-window activity consumed the report limit: %#v", payload)
	}
	coverage := payload.Coverage["codex"]
	if coverage.Status != "partial" || !coverage.Truncated ||
		coverage.MatchedSessions != 2 || coverage.IncludedSessions != 1 {
		t.Fatalf("codex limit coverage = %#v", coverage)
	}
}

func TestCLIReportAppliesLimitAfterWindowEvidenceSelectionForZCode(t *testing.T) {
	codexHome := t.TempDir()
	claudeHome := t.TempDir()
	zcodeHome := t.TempDir()
	repo := t.TempDir()
	start := time.Date(2026, 7, 10, 0, 0, 0, 0, time.Local)
	end := start.AddDate(0, 0, 1)

	writeZCodeSessions(t, zcodeHome, []zcodeSessionFixture{
		{
			ID:        "window-old",
			CWD:       repo,
			Title:     "older in-window work",
			CreatedAt: start.Add(9 * time.Hour).UnixMilli(),
			UpdatedAt: start.Add(9 * time.Hour).UnixMilli(),
		},
		{
			ID:        "window-new",
			CWD:       repo,
			Title:     "newer in-window work",
			CreatedAt: start.Add(10 * time.Hour).UnixMilli(),
			UpdatedAt: start.Add(10 * time.Hour).UnixMilli(),
		},
		{
			ID:        "after-window",
			CWD:       repo,
			Title:     "work after the requested window",
			CreatedAt: end.Add(time.Hour).UnixMilli(),
			UpdatedAt: end.Add(time.Hour).UnixMilli(),
		},
	})

	out := runCommand(t, "report",
		"--codex-home", codexHome,
		"--claude-home", claudeHome,
		"--zcode-home", zcodeHome,
		"--start", start.Format("2006-01-02"),
		"--end", end.Format("2006-01-02"),
		"--limit", "1")
	var payload struct {
		Totals struct {
			Sessions int `json:"sessions"`
		} `json:"totals"`
		Sessions []struct {
			ID       string `json:"id"`
			Provider string `json:"provider"`
		} `json:"sessions"`
		Coverage map[string]struct {
			Status           string `json:"status"`
			Truncated        bool   `json:"truncated"`
			MatchedSessions  int    `json:"matched_sessions"`
			IncludedSessions int    `json:"included_sessions"`
		} `json:"coverage"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("invalid report JSON: %v\n%s", err, out)
	}
	if payload.Totals.Sessions != 1 || len(payload.Sessions) != 1 ||
		payload.Sessions[0].ID != "window-new" || payload.Sessions[0].Provider != "zcode" {
		t.Fatalf("post-window ZCode activity consumed the report limit: %#v", payload)
	}
	coverage := payload.Coverage["zcode"]
	if coverage.Status != "partial" || !coverage.Truncated ||
		coverage.MatchedSessions != 2 || coverage.IncludedSessions != 1 {
		t.Fatalf("zcode limit coverage = %#v", coverage)
	}
}

func TestCLIReportPreservesProviderErrorsWhenApplyingLimit(t *testing.T) {
	codexHome := t.TempDir()
	claudeHome := t.TempDir()
	zcodeHome := t.TempDir()
	repo := t.TempDir()
	sessionDir := filepath.Join(codexHome, "sessions", "2026", "07", "10")
	start := time.Date(2026, 7, 10, 0, 0, 0, 0, time.Local)
	end := start.AddDate(0, 0, 1)

	writeReportSession := func(id, text string, at time.Time) {
		t.Helper()
		path := filepath.Join(sessionDir, id+".jsonl")
		writeFile(t, path,
			`{"timestamp":`+jsonString(at.Format(time.RFC3339Nano))+`,"type":"session_meta","payload":{"id":`+jsonString(id)+`,"timestamp":`+jsonString(at.Format(time.RFC3339Nano))+`,"cwd":`+jsonString(repo)+`}}`+"\n"+
				`{"timestamp":`+jsonString(at.Add(time.Second).Format(time.RFC3339Nano))+`,"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":`+jsonString(text)+`}]}}`+"\n")
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatal(err)
		}
	}
	writeReportSession("window-old", "older healthy work", start.Add(9*time.Hour))
	writeReportSession("window-new", "newer healthy work", start.Add(10*time.Hour))
	writeFile(t, filepath.Join(zcodeHome, "cli", "db", "db.sqlite"), "not a sqlite database")

	out, err := runCommandAllowError(t, "report",
		"--codex-home", codexHome,
		"--claude-home", claudeHome,
		"--zcode-home", zcodeHome,
		"--start", start.Format("2006-01-02"),
		"--end", end.Format("2006-01-02"),
		"--limit", "1")
	if err != nil {
		t.Fatalf("partial report discovery failed: %v\n%s", err, out)
	}
	var payload struct {
		Sessions []struct {
			ID       string `json:"id"`
			Provider string `json:"provider"`
		} `json:"sessions"`
		ProviderErrors []struct {
			Provider string `json:"provider"`
			Error    string `json:"error"`
		} `json:"provider_errors"`
		Coverage map[string]struct {
			Status           string `json:"status"`
			Truncated        bool   `json:"truncated"`
			MatchedSessions  int    `json:"matched_sessions"`
			IncludedSessions int    `json:"included_sessions"`
		} `json:"coverage"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("invalid report JSON: %v\n%s", err, out)
	}
	if len(payload.Sessions) != 1 ||
		payload.Sessions[0].ID != "window-new" ||
		payload.Sessions[0].Provider != "codex" {
		t.Fatalf("healthy sessions were not limited after evidence selection: %#v", payload.Sessions)
	}
	coverage := payload.Coverage["codex"]
	if coverage.Status != "partial" || !coverage.Truncated ||
		coverage.MatchedSessions != 2 || coverage.IncludedSessions != 1 {
		t.Fatalf("codex limit coverage = %#v", coverage)
	}
	if len(payload.ProviderErrors) != 1 ||
		payload.ProviderErrors[0].Provider != "zcode" ||
		!strings.Contains(payload.ProviderErrors[0].Error, "not a database") {
		t.Fatalf("provider errors = %#v", payload.ProviderErrors)
	}
}

func TestCLIReportExcludesInjectedCodexContexts(t *testing.T) {
	codexHome := t.TempDir()
	claudeHome := t.TempDir()
	repo := t.TempDir()
	sessionDir := filepath.Join(codexHome, "sessions", "2026", "07", "23")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}

	now := time.Now().In(time.Local)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	yesterday := today.AddDate(0, 0, -1)
	at := yesterday.Add(time.Hour)
	recommendedPlugins := "<recommended_plugins>\n- Slack\n- Teams\n</recommended_plugins>"
	browserWrappedRequest := `<in-app-browser-context source="ambient-ui-state">
This block is automatically supplied ambient UI state, not part of the user's request.
# In app browser:
- Current URL: http://localhost:5173/
</in-app-browser-context>

## My request for Codex:
当前用户系统是完善的吗？`
	fileWrappedRequest := `# Files mentioned by the user:

## design.png: /tmp/design.png

## My request for Codex:
实现附件里的设计`
	annotationWrappedRequest := `# Response annotations:

<response-annotations>
[{"text":"earlier response","annotation":"please clarify"}]
</response-annotations>

## My request for Codex:
解释这个取舍`
	annotationOnlyRequest := `# Response annotations:

<response-annotations>
[{"text":"earlier response","annotation":"只解释选中的错误"}]
</response-annotations>

## My request for Codex:`
	heartbeat := "<heartbeat><automation_id>monitor-pr</automation_id></heartbeat>"

	mixedPath := filepath.Join(sessionDir, "mixed.jsonl")
	writeFile(t, mixedPath, `{"timestamp":`+jsonString(at.Format(time.RFC3339Nano))+`,"type":"session_meta","payload":{"id":"mixed","timestamp":`+jsonString(at.Format(time.RFC3339Nano))+`,"cwd":`+jsonString(repo)+`}}
{"timestamp":`+jsonString(at.Add(time.Second).Format(time.RFC3339Nano))+`,"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":`+jsonString(recommendedPlugins)+`},{"type":"input_text","text":"# AGENTS.md instructions for /repo\nignore"},{"type":"input_text","text":"<environment_context>\n<cwd>/repo</cwd>\n</environment_context>"}]}}
{"timestamp":`+jsonString(at.Add(2*time.Second).Format(time.RFC3339Nano))+`,"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":`+jsonString(browserWrappedRequest)+`}]}}
{"timestamp":`+jsonString(at.Add(3*time.Second).Format(time.RFC3339Nano))+`,"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":`+jsonString(fileWrappedRequest)+`}]}}
{"timestamp":`+jsonString(at.Add(4*time.Second).Format(time.RFC3339Nano))+`,"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":`+jsonString(annotationWrappedRequest)+`}]}}
{"timestamp":`+jsonString(at.Add(5*time.Second).Format(time.RFC3339Nano))+`,"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":`+jsonString(annotationOnlyRequest)+`}]}}
{"timestamp":`+jsonString(at.Add(6*time.Second).Format(time.RFC3339Nano))+`,"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":`+jsonString(heartbeat)+`}]}}
`)

	injectedOnlyPath := filepath.Join(sessionDir, "injected-only.jsonl")
	writeFile(t, injectedOnlyPath, `{"timestamp":`+jsonString(at.Format(time.RFC3339Nano))+`,"type":"session_meta","payload":{"id":"injected-only","timestamp":`+jsonString(at.Format(time.RFC3339Nano))+`,"cwd":`+jsonString(repo)+`}}
{"timestamp":`+jsonString(at.Add(time.Second).Format(time.RFC3339Nano))+`,"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":`+jsonString(recommendedPlugins)+`}]}}
`)
	if err := os.Chtimes(mixedPath, at, at); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(injectedOnlyPath, at, at); err != nil {
		t.Fatal(err)
	}

	out := runCommand(t, "report", "--codex-home", codexHome, "--claude-home", claudeHome,
		"--period", "yesterday")
	var payload struct {
		Totals struct {
			Sessions           int `json:"sessions"`
			UnverifiedSessions int `json:"unverified_sessions"`
		} `json:"totals"`
		Sessions []struct {
			ID            string `json:"id"`
			EvidenceCount int    `json:"evidence_count"`
			Evidence      []struct {
				Text string `json:"text"`
			} `json:"evidence"`
		} `json:"sessions"`
		UnverifiedSessions []struct {
			ID              string `json:"id"`
			ReasonCode      string `json:"reason_code"`
			MayHideUserWork bool   `json:"may_hide_user_work"`
		} `json:"unverified_sessions"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if payload.Totals.Sessions != 1 || payload.Totals.UnverifiedSessions != 1 {
		t.Fatalf("unexpected totals: %#v", payload.Totals)
	}
	if len(payload.Sessions) != 1 || payload.Sessions[0].ID != "mixed" {
		t.Fatalf("sessions = %#v, want only mixed", payload.Sessions)
	}
	if payload.Sessions[0].EvidenceCount != 4 ||
		len(payload.Sessions[0].Evidence) != 4 {
		t.Fatalf("evidence = %#v count=%d", payload.Sessions[0].Evidence, payload.Sessions[0].EvidenceCount)
	}
	wantEvidence := []string{
		"当前用户系统是完善的吗？",
		"实现附件里的设计",
		"please clarify 解释这个取舍",
		"只解释选中的错误",
	}
	for i, want := range wantEvidence {
		if payload.Sessions[0].Evidence[i].Text != want {
			t.Fatalf("evidence[%d].Text = %q, want %q", i, payload.Sessions[0].Evidence[i].Text, want)
		}
	}
	if len(payload.UnverifiedSessions) != 1 || payload.UnverifiedSessions[0].ID != "injected-only" {
		t.Fatalf("unverified sessions = %#v", payload.UnverifiedSessions)
	}
	if payload.UnverifiedSessions[0].ReasonCode != "updated_without_in_window_user_message" ||
		payload.UnverifiedSessions[0].MayHideUserWork {
		t.Fatalf("ordinary transcript activity was mislabeled as missing work: %#v", payload.UnverifiedSessions[0])
	}
	for _, injected := range []string{"recommended_plugins", "in-app-browser-context", "<heartbeat>"} {
		if strings.Contains(out, injected) {
			t.Fatalf("report leaked injected context %q: %s", injected, out)
		}
	}
}

func TestCLIReportLongLivedSessionIsStrictlyPartitionedByDay(t *testing.T) {
	home := t.TempDir()
	claudeHome := t.TempDir()
	repo := t.TempDir()
	sessionDir := filepath.Join(home, "sessions", "2026", "06", "17")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	loc := time.Local
	dayOne := time.Date(2026, 6, 17, 0, 0, 0, 0, loc)
	dayTwo := dayOne.AddDate(0, 0, 1)
	dayThree := dayTwo.AddDate(0, 0, 1)
	sessionPath := filepath.Join(sessionDir, "long-lived.jsonl")
	writeFile(t, sessionPath, `{"timestamp":"`+dayOne.Add(time.Hour).Format(time.RFC3339Nano)+`","type":"session_meta","payload":{"id":"long-lived","timestamp":"`+dayOne.Add(time.Hour).Format(time.RFC3339Nano)+`","cwd":`+jsonString(repo)+`}}
{"timestamp":"`+dayOne.Add(2*time.Hour).Format(time.RFC3339Nano)+`","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"day one work"}]}}
{"timestamp":"`+dayTwo.Add(2*time.Hour).Format(time.RFC3339Nano)+`","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"day two work"}]}}
{"timestamp":"`+dayThree.Add(2*time.Hour).Format(time.RFC3339Nano)+`","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"day three work"}]}}
`)
	if err := os.Chtimes(sessionPath, dayThree.Add(3*time.Hour), dayThree.Add(3*time.Hour)); err != nil {
		t.Fatal(err)
	}

	for i, tc := range []struct {
		start time.Time
		want  string
	}{
		{start: dayOne, want: "day one work"},
		{start: dayTwo, want: "day two work"},
		{start: dayThree, want: "day three work"},
	} {
		end := tc.start.AddDate(0, 0, 1)
		out := runCommand(t, "report", "--codex-home", home, "--claude-home", claudeHome,
			"--start", tc.start.Format("2006-01-02"), "--end", end.Format("2006-01-02"))
		var payload struct {
			Sessions []struct {
				ID       string `json:"id"`
				Title    string `json:"title"`
				Evidence []struct {
					Text string `json:"text"`
				} `json:"evidence"`
			} `json:"sessions"`
		}
		if err := json.Unmarshal([]byte(out), &payload); err != nil {
			t.Fatalf("day %d invalid JSON: %v\n%s", i+1, err, out)
		}
		if len(payload.Sessions) != 1 || payload.Sessions[0].ID != "long-lived" {
			t.Fatalf("day %d sessions = %#v", i+1, payload.Sessions)
		}
		if payload.Sessions[0].Title != "" {
			t.Fatalf("day %d leaked session title %q", i+1, payload.Sessions[0].Title)
		}
		if len(payload.Sessions[0].Evidence) != 1 || payload.Sessions[0].Evidence[0].Text != tc.want {
			t.Fatalf("day %d evidence = %#v, want %q", i+1, payload.Sessions[0].Evidence, tc.want)
		}
	}
}

type generatedSessionPayload struct {
	ID       string            `json:"id"`
	Provider string            `json:"provider"`
	Metadata map[string]string `json:"metadata"`
}

func TestCLIExcludesNonInteractiveCodexExecByDefault(t *testing.T) {
	codexHome := t.TempDir()
	claudeHome := t.TempDir()
	repo := t.TempDir()

	now := time.Now().In(time.Local)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	yesterday := today.AddDate(0, 0, -1)
	inWindow := yesterday.Add(time.Hour)
	sessionPath := filepath.Join(codexHome, "sessions", "2026", "07", "23", "generated.jsonl")
	writeFile(t, sessionPath, `{"timestamp":"`+inWindow.Format(time.RFC3339Nano)+`","type":"session_meta","payload":{"id":"generated","timestamp":"`+inWindow.Format(time.RFC3339Nano)+`","cwd":`+jsonString(repo)+`,"source":"exec","originator":"codex_exec"}}
{"timestamp":"`+inWindow.Add(time.Second).Format(time.RFC3339Nano)+`","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"generate automated daily report"}]}}
`)
	if err := os.Chtimes(sessionPath, inWindow, inWindow); err != nil {
		t.Fatal(err)
	}

	var payload struct {
		Totals struct {
			Sessions int `json:"sessions"`
		} `json:"totals"`
		Sessions []generatedSessionPayload `json:"sessions"`
	}

	out := runCommand(t, "--codex-home", codexHome, "--claude-home", claudeHome,
		"--since-days", "0", "--json")
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if len(payload.Sessions) != 0 {
		t.Fatalf("default discovery should exclude non-interactive sessions: %#v", payload.Sessions)
	}

	out = runCommand(t, "--codex-home", codexHome, "--claude-home", claudeHome,
		"--since-days", "0", "--json", "--include-non-interactive")
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	assertGeneratedSession(t, payload.Sessions)

	out = runCommand(t, "report", "--codex-home", codexHome, "--claude-home", claudeHome,
		"--period", "yesterday")
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if payload.Totals.Sessions != 0 || len(payload.Sessions) != 0 {
		t.Fatalf("default report should exclude non-interactive sessions: %#v", payload)
	}

	out = runCommand(t, "report", "--codex-home", codexHome, "--claude-home", claudeHome,
		"--period", "yesterday", "--include-non-interactive")
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if payload.Totals.Sessions != 1 {
		t.Fatalf("report totals = %#v, want one session", payload.Totals)
	}
	assertGeneratedSession(t, payload.Sessions)
}

func assertGeneratedSession(t *testing.T, sessions []generatedSessionPayload) {
	t.Helper()
	if len(sessions) != 1 || sessions[0].ID != "generated" || sessions[0].Provider != "codex" {
		t.Fatalf("included sessions = %#v, want generated Codex exec session", sessions)
	}
	if sessions[0].Metadata["interaction_mode"] != "non_interactive" {
		t.Fatalf("metadata = %#v", sessions[0].Metadata)
	}
}

func TestCLIReportKeepsCodeBuddyCLISessionByDefault(t *testing.T) {
	codexHome := t.TempDir()
	claudeHome := t.TempDir()
	codebuddyHome := t.TempDir()
	repo := t.TempDir()

	now := time.Now().In(time.Local)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	yesterday := today.AddDate(0, 0, -1)
	inWindow := yesterday.Add(time.Hour)
	millis := inWindow.UnixMilli()
	sessionPath := filepath.Join(codebuddyHome, "projects", "repo", "generated.jsonl")
	writeFile(t, sessionPath, `{"sessionId":"generated","cwd":`+jsonString(repo)+`,"timestamp":`+fmt.Sprintf("%d", millis)+`,"providerData":{"agent":"cli"},"role":"user","content":[{"type":"input_text","text":"generate automated daily report"}]}
`)
	if err := os.Chtimes(sessionPath, inWindow, inWindow); err != nil {
		t.Fatal(err)
	}

	var payload struct {
		Totals struct {
			Sessions int `json:"sessions"`
		} `json:"totals"`
		Sessions []generatedSessionPayload `json:"sessions"`
	}

	out := runCommand(t, "report", "--codex-home", codexHome, "--claude-home", claudeHome,
		"--codebuddy-home", codebuddyHome, "--period", "yesterday")
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if payload.Totals.Sessions != 1 || len(payload.Sessions) != 1 || payload.Sessions[0].Provider != "codebuddy" {
		t.Fatalf("default report should keep CodeBuddy CLI session: %#v", payload)
	}
	if payload.Sessions[0].Metadata["agent"] != "cli" || payload.Sessions[0].Metadata["interaction_mode"] != "" {
		t.Fatalf("agent metadata must not imply non-interactive mode: %#v", payload.Sessions[0].Metadata)
	}
}

func TestCLIReportKeepsAmbiguousOneTurnCursorSession(t *testing.T) {
	codexHome := t.TempDir()
	claudeHome := t.TempDir()
	cursorHome := t.TempDir()
	repo := t.TempDir()

	now := time.Now().In(time.Local)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	yesterday := today.AddDate(0, 0, -1)
	inWindow := yesterday.Add(time.Hour)
	projectDir := filepath.Join(cursorHome, "projects", "repo")
	chatID := "generated"
	sessionPath := filepath.Join(projectDir, "agent-transcripts", chatID, chatID+".jsonl")
	writeFile(t, filepath.Join(projectDir, "worker.log"), "workspacePath="+repo+"\n")
	writeFile(t, sessionPath, `{"role":"user","timestamp":"`+inWindow.Format(time.RFC3339Nano)+`","message":{"content":[{"type":"text","text":"<timestamp>Wednesday, Jun 24, 2026, 2:27 AM (UTC)</timestamp>\n<user_query>\ngenerate automated daily report\n</user_query>"}]}}
{"role":"assistant","timestamp":"`+inWindow.Add(time.Second).Format(time.RFC3339Nano)+`","message":{"content":[{"type":"text","text":"ok"}]}}
{"type":"turn_ended","status":"success"}
`)
	if err := os.Chtimes(sessionPath, inWindow, inWindow); err != nil {
		t.Fatal(err)
	}

	var payload struct {
		Totals struct {
			Sessions int `json:"sessions"`
		} `json:"totals"`
		Sessions []generatedSessionPayload `json:"sessions"`
	}

	out := runCommand(t, "report", "--codex-home", codexHome, "--claude-home", claudeHome,
		"--cursor-home", cursorHome, "--period", "yesterday")
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if payload.Totals.Sessions != 1 || len(payload.Sessions) != 1 || payload.Sessions[0].Provider != "cursor" {
		t.Fatalf("default report should keep an ambiguous one-turn Cursor session: %#v", payload)
	}
	if payload.Sessions[0].Metadata["interaction_mode"] != "" {
		t.Fatalf("ambiguous Cursor session must remain interactive: %#v", payload.Sessions[0].Metadata)
	}
}

func TestCLIReportOversizedCrossDaySessionKeepsPerDayEdges(t *testing.T) {
	home := t.TempDir()
	claudeHome := t.TempDir()
	repo := t.TempDir()
	sessionDir := filepath.Join(home, "sessions", "2026", "06", "20")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	loc := time.Local
	dayOne := time.Date(2026, 6, 20, 0, 0, 0, 0, loc)
	dayTwo := dayOne.AddDate(0, 0, 1)
	sessionPath := filepath.Join(sessionDir, "oversized-cross-day.jsonl")
	var rollout strings.Builder
	rollout.WriteString(`{"timestamp":"` + dayOne.Add(time.Hour).Format(time.RFC3339Nano) + `","type":"session_meta","payload":{"id":"oversized-cross-day","timestamp":"` + dayOne.Add(time.Hour).Format(time.RFC3339Nano) + `","cwd":` + jsonString(repo) + "}}\n")
	for dayIndex, day := range []time.Time{dayOne, dayTwo} {
		for messageIndex := 1; messageIndex <= 5; messageIndex++ {
			at := day.Add(time.Duration(messageIndex+1) * time.Hour)
			text := fmt.Sprintf("day %d message %d", dayIndex+1, messageIndex)
			rollout.WriteString(`{"timestamp":"` + at.Format(time.RFC3339Nano) + `","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":` + jsonString(text) + "}]}}\n")
		}
		if dayIndex == 0 {
			// A single tool result can exceed Scanner's token limit even though
			// the surrounding user messages are small and valid report evidence.
			rollout.WriteString(`{"timestamp":"` + day.Add(23*time.Hour).Format(time.RFC3339Nano) + `","type":"response_item","payload":{"type":"custom_tool_call_output","output":` + jsonString(strings.Repeat("x", 8*1024*1024)) + "}}\n")
		}
	}
	writeFile(t, sessionPath, rollout.String())
	if err := os.Chtimes(sessionPath, dayTwo.Add(12*time.Hour), dayTwo.Add(12*time.Hour)); err != nil {
		t.Fatal(err)
	}

	want := []string{"message 1", "message 2", "message 4", "message 5"}
	for dayIndex, day := range []time.Time{dayOne, dayTwo} {
		end := day.AddDate(0, 0, 1)
		out := runCommand(t, "report", "--codex-home", home, "--claude-home", claudeHome,
			"--start", day.Format("2006-01-02"), "--end", end.Format("2006-01-02"))
		var payload struct {
			Sessions []struct {
				ID       string            `json:"id"`
				Metadata map[string]string `json:"metadata"`
				Evidence []struct {
					Text string `json:"text"`
				} `json:"evidence"`
			} `json:"sessions"`
		}
		if err := json.Unmarshal([]byte(out), &payload); err != nil {
			t.Fatalf("day %d invalid JSON: %v\n%s", dayIndex+1, err, out)
		}
		if len(payload.Sessions) != 1 || payload.Sessions[0].ID != "oversized-cross-day" {
			t.Fatalf("day %d sessions = %#v", dayIndex+1, payload.Sessions)
		}
		var evidence []string
		for _, item := range payload.Sessions[0].Evidence {
			evidence = append(evidence, item.Text)
		}
		var expected []string
		for _, suffix := range want {
			expected = append(expected, fmt.Sprintf("day %d %s", dayIndex+1, suffix))
		}
		if strings.Join(evidence, "|") != strings.Join(expected, "|") {
			t.Fatalf("day %d evidence = %#v, want %#v", dayIndex+1, evidence, expected)
		}
		if payload.Sessions[0].Metadata["report_evidence_status"] != "" {
			t.Fatalf("day %d known oversized tool output should not reduce coverage: %#v", dayIndex+1, payload.Sessions[0].Metadata)
		}
	}
}

func TestCLIReportOversizedSessionKeepsPerDayEdgesAcrossSiblingProviders(t *testing.T) {
	for _, provider := range []string{"claude", "codebuddy", "cursor"} {
		t.Run(provider, func(t *testing.T) {
			codexHome := t.TempDir()
			claudeHome := t.TempDir()
			codebuddyHome := t.TempDir()
			cursorHome := t.TempDir()
			repo := t.TempDir()
			loc := time.Local
			dayOne := time.Date(2026, 6, 20, 0, 0, 0, 0, loc)
			dayTwo := dayOne.AddDate(0, 0, 1)
			sessionID := provider + "-oversized"
			var transcript strings.Builder

			writeUser := func(dayIndex, messageIndex int, day time.Time) {
				at := day.Add(time.Duration(messageIndex+1) * time.Hour)
				text := fmt.Sprintf("day %d message %d", dayIndex+1, messageIndex)
				switch provider {
				case "claude":
					transcript.WriteString(`{"type":"user","sessionId":` + jsonString(sessionID) + `,"cwd":` + jsonString(repo) + `,"timestamp":"` + at.Format(time.RFC3339Nano) + `","message":{"role":"user","content":` + jsonString(text) + "}}\n")
				case "codebuddy":
					transcript.WriteString(`{"sessionId":` + jsonString(sessionID) + `,"cwd":` + jsonString(repo) + `,"timestamp":"` + at.Format(time.RFC3339Nano) + `","role":"user","content":` + jsonString(text) + "}\n")
				case "cursor":
					transcript.WriteString(`{"timestamp":"` + at.Format(time.RFC3339Nano) + `","role":"user","content":` + jsonString(text) + "}\n")
				}
			}
			for dayIndex, day := range []time.Time{dayOne, dayTwo} {
				for messageIndex := 1; messageIndex <= 5; messageIndex++ {
					writeUser(dayIndex, messageIndex, day)
				}
				if dayIndex == 0 {
					at := day.Add(23 * time.Hour).Format(time.RFC3339Nano)
					largeOutput := jsonString(strings.Repeat("x", 8*1024*1024))
					switch provider {
					case "claude":
						transcript.WriteString(`{"type":"assistant","sessionId":` + jsonString(sessionID) + `,"cwd":` + jsonString(repo) + `,"timestamp":"` + at + `","message":{"role":"assistant","content":` + largeOutput + "}}\n")
					case "codebuddy":
						transcript.WriteString(`{"sessionId":` + jsonString(sessionID) + `,"cwd":` + jsonString(repo) + `,"timestamp":"` + at + `","role":"assistant","content":` + largeOutput + "}\n")
					case "cursor":
						transcript.WriteString(`{"timestamp":"` + at + `","role":"assistant","content":` + largeOutput + "}\n")
					}
				}
			}

			var sessionPath string
			switch provider {
			case "claude":
				sessionPath = filepath.Join(claudeHome, "projects", "-repo", sessionID+".jsonl")
			case "codebuddy":
				sessionPath = filepath.Join(codebuddyHome, "projects", "repo", sessionID+".jsonl")
			case "cursor":
				projectDir := filepath.Join(cursorHome, "projects", "repo")
				writeFile(t, filepath.Join(projectDir, "worker.log"), "workspacePath="+repo+"\n")
				sessionPath = filepath.Join(projectDir, "agent-transcripts", sessionID, sessionID+".jsonl")
			}
			writeFile(t, sessionPath, transcript.String())
			if err := os.Chtimes(sessionPath, dayTwo.Add(12*time.Hour), dayTwo.Add(12*time.Hour)); err != nil {
				t.Fatal(err)
			}

			want := []string{"message 1", "message 2", "message 4", "message 5"}
			for dayIndex, day := range []time.Time{dayOne, dayTwo} {
				end := day.AddDate(0, 0, 1)
				out := runCommand(t, "report",
					"--codex-home", codexHome,
					"--claude-home", claudeHome,
					"--codebuddy-home", codebuddyHome,
					"--cursor-home", cursorHome,
					"--start", day.Format("2006-01-02"),
					"--end", end.Format("2006-01-02"))
				var payload struct {
					Sessions []struct {
						ID       string            `json:"id"`
						Provider string            `json:"provider"`
						Metadata map[string]string `json:"metadata"`
						Evidence []struct {
							Text string `json:"text"`
						} `json:"evidence"`
					} `json:"sessions"`
				}
				if err := json.Unmarshal([]byte(out), &payload); err != nil {
					t.Fatalf("day %d invalid JSON: %v\n%s", dayIndex+1, err, out)
				}
				if len(payload.Sessions) != 1 || payload.Sessions[0].ID != sessionID || payload.Sessions[0].Provider != provider {
					t.Fatalf("day %d sessions = %#v", dayIndex+1, payload.Sessions)
				}
				var evidence []string
				for _, item := range payload.Sessions[0].Evidence {
					evidence = append(evidence, item.Text)
				}
				var expected []string
				for _, suffix := range want {
					expected = append(expected, fmt.Sprintf("day %d %s", dayIndex+1, suffix))
				}
				if strings.Join(evidence, "|") != strings.Join(expected, "|") {
					t.Fatalf("day %d evidence = %#v, want %#v", dayIndex+1, evidence, expected)
				}
				if payload.Sessions[0].Metadata["report_evidence_status"] != "" {
					t.Fatalf("day %d known oversized assistant output should not reduce coverage: %#v", dayIndex+1, payload.Sessions[0].Metadata)
				}
			}
		})
	}
}

func TestCLIReportRecoversOversizedUserMessageEdgesAcrossJSONLProviders(t *testing.T) {
	for _, provider := range []string{"codex", "claude", "codebuddy", "cursor"} {
		t.Run(provider, func(t *testing.T) {
			codexHome := t.TempDir()
			claudeHome := t.TempDir()
			codebuddyHome := t.TempDir()
			cursorHome := t.TempDir()
			repo := t.TempDir()
			at := time.Date(2026, 6, 20, 9, 0, 0, 0, time.Local)
			sessionID := provider + "-large-user"
			largeText := "HEAD-user-request-" +
				strings.Repeat("x", 8*1024*1024) +
				"-TAIL-user-decision"

			var sessionPath string
			switch provider {
			case "codex":
				sessionPath = filepath.Join(
					codexHome,
					"sessions",
					"2026",
					"06",
					"20",
					sessionID+".jsonl",
				)
				writeFile(t, sessionPath,
					`{"timestamp":`+jsonString(at.Format(time.RFC3339Nano))+`,"type":"session_meta","payload":{"id":`+jsonString(sessionID)+`,"timestamp":`+jsonString(at.Format(time.RFC3339Nano))+`,"cwd":`+jsonString(repo)+`}}`+"\n"+
						`{"timestamp":`+jsonString(at.Add(time.Second).Format(time.RFC3339Nano))+`,"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":`+jsonString(largeText)+`}]}}`+"\n")
			case "claude":
				sessionPath = filepath.Join(
					claudeHome,
					"projects",
					"-repo",
					sessionID+".jsonl",
				)
				writeFile(t, sessionPath,
					`{"type":"user","sessionId":`+jsonString(sessionID)+`,"cwd":`+jsonString(repo)+`,"timestamp":`+jsonString(at.Format(time.RFC3339Nano))+`,"message":{"role":"user","content":`+jsonString(largeText)+`}}`+"\n")
			case "codebuddy":
				sessionPath = filepath.Join(
					codebuddyHome,
					"projects",
					"repo",
					sessionID+".jsonl",
				)
				writeFile(t, sessionPath,
					`{"sessionId":`+jsonString(sessionID)+`,"cwd":`+jsonString(repo)+`,"timestamp":`+jsonString(at.Format(time.RFC3339Nano))+`,"role":"user","content":`+jsonString(largeText)+`}`+"\n")
			case "cursor":
				projectDir := filepath.Join(cursorHome, "projects", "repo")
				writeFile(t, filepath.Join(projectDir, "worker.log"), "workspacePath="+repo+"\n")
				sessionPath = filepath.Join(
					projectDir,
					"agent-transcripts",
					sessionID,
					sessionID+".jsonl",
				)
				writeFile(t, sessionPath,
					`{"role":"user","timestamp":`+jsonString(at.Format(time.RFC3339Nano))+`,"content":`+jsonString(largeText)+`}`+"\n")
			}
			if err := os.Chtimes(sessionPath, at, at); err != nil {
				t.Fatal(err)
			}

			out := runCommand(t, "report",
				"--codex-home", codexHome,
				"--claude-home", claudeHome,
				"--codebuddy-home", codebuddyHome,
				"--cursor-home", cursorHome,
				"--start", "2026-06-20",
				"--end", "2026-06-21",
				"--preview-max-chars", "120")
			var payload struct {
				Sessions []struct {
					ID       string            `json:"id"`
					Provider string            `json:"provider"`
					Metadata map[string]string `json:"metadata"`
					Evidence []struct {
						Text string    `json:"text"`
						At   time.Time `json:"at"`
					} `json:"evidence"`
				} `json:"sessions"`
			}
			if err := json.Unmarshal([]byte(out), &payload); err != nil {
				t.Fatalf("invalid report JSON: %v\n%s", err, out)
			}
			if len(payload.Sessions) != 1 ||
				payload.Sessions[0].ID != sessionID ||
				payload.Sessions[0].Provider != provider ||
				len(payload.Sessions[0].Evidence) != 1 {
				t.Fatalf("sessions = %#v, want one recovered %s session", payload.Sessions, provider)
			}
			evidence := payload.Sessions[0].Evidence[0]
			if !strings.Contains(evidence.Text, "HEAD-user-request") ||
				!strings.Contains(evidence.Text, "TAIL-user-decision") {
				t.Fatalf("evidence = %q, want both oversized message edges", evidence.Text)
			}
			if evidence.At.IsZero() {
				t.Fatalf("evidence timestamp was not recovered: %#v", evidence)
			}
			if payload.Sessions[0].Metadata["report_evidence_status"] != "" {
				t.Fatalf("recovered oversized user message should not reduce coverage: %#v", payload.Sessions[0].Metadata)
			}
		})
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
		"KIRO_HOME="+t.TempDir(),
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

func writeKiroSession(t *testing.T, home, id, cwd, title string) {
	t.Helper()
	sessionsDir := filepath.Join(home, "sessions", "cli")
	writeFile(t, filepath.Join(sessionsDir, id+".json"), `{"session_id":`+jsonString(id)+`,"cwd":`+jsonString(cwd)+`,"created_at":"2026-06-13T01:00:00Z","updated_at":"2026-06-13T01:01:00Z","title":`+jsonString(title)+`,"session_created_reason":"user","session_state":{"version":"1"}}
`)
	writeFile(t, filepath.Join(sessionsDir, id+".jsonl"), `{"kind":"Prompt","version":"v1","data":{"message_id":"msg-kiro","content":[{"kind":"text","data":`+jsonString(title)+`}],"meta":{"timestamp":1781312400}}}
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

type zcodeSessionFixture struct {
	ID        string
	CWD       string
	Title     string
	CreatedAt int64
	UpdatedAt int64
}

func writeZCodeSession(t *testing.T, home, id, cwd, title string) {
	t.Helper()
	writeZCodeSessions(t, home, []zcodeSessionFixture{{
		ID:        id,
		CWD:       cwd,
		Title:     title,
		CreatedAt: 1781322000000,
		UpdatedAt: 1781322060000,
	}})
}

func writeZCodeSessions(t *testing.T, home string, sessions []zcodeSessionFixture) {
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

	for _, item := range sessions {
		if _, err := db.Exec(`INSERT INTO session (id, project_id, slug, directory, title, version, time_created, time_updated, title_source)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			item.ID, "proj_"+item.ID, item.ID, item.CWD, item.Title, "1", item.CreatedAt, item.UpdatedAt, "generated"); err != nil {
			t.Fatal(err)
		}

		msgData, _ := json.Marshal(map[string]any{"role": "user", "time": map[string]any{"created": item.CreatedAt}})
		if _, err := db.Exec(`INSERT INTO message (id, session_id, time_created, time_updated, data) VALUES (?, ?, ?, ?, ?)`,
			"msg_"+item.ID, item.ID, item.CreatedAt, item.CreatedAt, string(msgData)); err != nil {
			t.Fatal(err)
		}
		partData, _ := json.Marshal(map[string]any{"type": "text", "text": item.Title})
		if _, err := db.Exec(`INSERT INTO part (id, message_id, session_id, time_created, time_updated, data) VALUES (?, ?, ?, ?, ?, ?)`,
			"part_"+item.ID, "msg_"+item.ID, item.ID, item.CreatedAt, item.CreatedAt, string(partData)); err != nil {
			t.Fatal(err)
		}
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
