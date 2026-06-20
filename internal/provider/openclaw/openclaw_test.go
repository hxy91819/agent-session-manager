package openclaw

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hxy91819/agent-session-manager/internal/session"
)

func TestDiscoverIndexesOpenClawSessionsJSON(t *testing.T) {
	stateDir := t.TempDir()
	repo := filepath.Join(stateDir, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(stateDir, "agents", "main", "sessions", "sessions.json")
	writeFile(t, path, `{
  "agent:main:main": {
    "sessionId": "native-123",
    "updatedAt": 1781312460000,
    "createdAt": 1781312400000,
    "spawnedCwd": `+jsonString(repo)+`,
    "sessionFile": "/tmp/native.jsonl",
    "kind": "main",
    "displayName": "OpenClaw Main"
  }
}`)

	got, err := New(stateDir).Discover(session.DiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	s := got[0]
	if s.ID != "agent:main:main" || s.Provider != Name || s.CWD != repo || s.Title != "OpenClaw Main" {
		t.Fatalf("unexpected session: %#v", s)
	}
	if s.Metadata["native_session_id"] != "native-123" || s.Metadata["agent_id"] != "main" || s.Metadata["kind"] != "main" {
		t.Fatalf("metadata = %#v", s.Metadata)
	}
	if s.Metadata["resume_unsupported"] != "OpenClaw resume is not supported by asm yet" {
		t.Fatalf("resume_unsupported = %q", s.Metadata["resume_unsupported"])
	}
	if s.UpdatedAt.UTC().Format(time.RFC3339) != "2026-06-13T01:01:00Z" {
		t.Fatalf("UpdatedAt = %s", s.UpdatedAt.UTC().Format(time.RFC3339))
	}
}

func TestDiscoverFallsBackToStateDirAndMarksMissingCWD(t *testing.T) {
	stateDir := t.TempDir()
	path := filepath.Join(stateDir, "agents", "agent-a", "sessions", "sessions.json")
	writeFile(t, path, `{
  "agent:agent-a:main": {
    "sessionId": "native-abc",
    "updatedAt": 1781312460000,
    "origin": {"label": "origin title"}
  }
}`)

	got, err := New(stateDir).Discover(session.DiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].CWD != "" {
		t.Fatalf("CWD = %q", got[0].CWD)
	}
	if got[0].Metadata["cwd_missing"] != "true" {
		t.Fatalf("metadata = %#v", got[0].Metadata)
	}
	if got[0].Title != "origin title" {
		t.Fatalf("Title = %q", got[0].Title)
	}
}

func TestDiscoverDoesNotUseAgentWorkspaceAsSessionCWD(t *testing.T) {
	stateDir := t.TempDir()
	workspace := filepath.Join(stateDir, "agents", "agent-a", "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(stateDir, "agents", "agent-a", "sessions", "sessions.json")
	writeFile(t, path, `{"agent:agent-a:main":{"sessionId":"native","updatedAt":1781312460000}}`)

	got, err := New(stateDir).Discover(session.DiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].CWD != "" || got[0].Metadata["cwd_missing"] != "true" {
		t.Fatalf("unexpected cwd fallback: %#v", got[0])
	}
}

func TestDiscoverDoesNotUseSpawnedWorkspaceDirAsSessionCWD(t *testing.T) {
	stateDir := t.TempDir()
	workspace := filepath.Join(stateDir, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(stateDir, "agents", "agent-a", "sessions", "sessions.json")
	writeFile(t, path, `{"agent:agent-a:main":{"sessionId":"native","updatedAt":1781312460000,"spawnedWorkspaceDir":`+jsonString(workspace)+`}}`)

	got, err := New(stateDir).Discover(session.DiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].CWD != "" || got[0].Metadata["cwd_missing"] != "true" || got[0].Metadata["spawned_workspace_dir"] != workspace {
		t.Fatalf("unexpected session: %#v", got[0])
	}
}

func TestDiscoverLimitsSessionIndexFilesBeforeParsing(t *testing.T) {
	stateDir := t.TempDir()
	oldPath := filepath.Join(stateDir, "agents", "old", "sessions", "sessions.json")
	newPath := filepath.Join(stateDir, "agents", "new", "sessions", "sessions.json")
	writeFile(t, oldPath, `{"agent:old:main":{"sessionId":"old","updatedAt":1781312400000,"displayName":"old"}}`)
	writeFile(t, newPath, `{
  "agent:new:one": {"sessionId":"new-one","updatedAt":1781312460000,"displayName":"new one"},
  "agent:new:two": {"sessionId":"new-two","updatedAt":1781312470000,"displayName":"new two"}
}`)
	base := time.Date(2026, 6, 13, 1, 0, 0, 0, time.UTC)
	if err := os.Chtimes(oldPath, base, base); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(newPath, base.Add(time.Hour), base.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	got, err := New(stateDir).Discover(session.DiscoverOptions{LimitFiles: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want both sessions from newest index file: %#v", len(got), got)
	}
	for _, item := range got {
		if strings.HasPrefix(item.ID, "agent:old") {
			t.Fatalf("old index was parsed despite LimitFiles=1: %#v", got)
		}
	}
}

func TestResumeCommandIsUnsupported(t *testing.T) {
	spec := New("").ResumeCommand(session.Session{ID: "agent:main:main"})

	if spec.UnsupportedReason != "OpenClaw resume is not supported by asm yet" {
		t.Fatalf("UnsupportedReason = %q", spec.UnsupportedReason)
	}
	if len(spec.Args) != 0 {
		t.Fatalf("Args = %#v", spec.Args)
	}
}

func TestNewCommandIsUnsupported(t *testing.T) {
	spec := New("").NewCommand("/repo")

	if spec.UnsupportedReason != "OpenClaw new session is not supported by asm yet" {
		t.Fatalf("UnsupportedReason = %q", spec.UnsupportedReason)
	}
	if len(spec.Args) != 0 {
		t.Fatalf("Args = %#v", spec.Args)
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

func TestStateDirUsesOpenClawHomeDotDir(t *testing.T) {
	t.Setenv("OPENCLAW_STATE_DIR", "")
	home := t.TempDir()
	t.Setenv("OPENCLAW_HOME", home)

	got, err := New("").stateDir()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(got, filepath.Join(home, ".openclaw")) {
		t.Fatalf("stateDir = %q", got)
	}
}

func jsonString(value string) string {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(data)
}
