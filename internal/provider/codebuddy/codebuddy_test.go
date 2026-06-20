package codebuddy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hxy91819/agent-session-manager/internal/session"
)

func TestMain(m *testing.M) {
	cacheDir, err := os.MkdirTemp("", "asm-codebuddy-cache-*")
	if err != nil {
		panic(err)
	}
	if err := os.Setenv("XDG_CACHE_HOME", cacheDir); err != nil {
		panic(err)
	}
	code := m.Run()
	_ = os.RemoveAll(cacheDir)
	os.Exit(code)
}

func TestParseSessionPrefersAITitleThenSummaryThenUser(t *testing.T) {
	input := strings.NewReader(`{"sessionId":"sid","cwd":"/repo","timestamp":"2026-06-13T01:00:00Z","role":"user","content":"first user title"}
{"sessionId":"sid","cwd":"/repo","timestamp":"2026-06-13T01:01:00Z","summary":"summary title"}
{"sessionId":"sid","cwd":"/repo","timestamp":"2026-06-13T01:02:00Z","ai-title":"AI title","message":{"role":"assistant","model":"codebuddy-model","content":"ok"}}
`)

	got, err := parseSession(input)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "sid" || got.CWD != "/repo" {
		t.Fatalf("unexpected session identity: %#v", got)
	}
	if got.Title != "AI title" || got.Metadata["title_source"] != "ai-title" {
		t.Fatalf("unexpected title metadata: %#v", got)
	}
	if got.Metadata["model"] != "codebuddy-model" {
		t.Fatalf("model = %q", got.Metadata["model"])
	}
}

func TestParseSessionUsesSummaryFallback(t *testing.T) {
	got, err := parseSession(strings.NewReader(`{"sessionId":"sid","cwd":"/repo","summary":"summary title","role":"user","content":"user title"}
`))
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "summary title" || got.Metadata["title_source"] != "summary" {
		t.Fatalf("unexpected title: %#v", got)
	}
}

func TestParseSessionUsesLastUserFallback(t *testing.T) {
	got, err := parseSession(strings.NewReader(`{"sessionId":"sid","cwd":"/repo","role":"user","content":"first title"}
{"sessionId":"sid","cwd":"/repo","role":"user","content":[{"type":"text","text":"last title"}]}
`))
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "last title" || got.Metadata["title_source"] != "user" {
		t.Fatalf("unexpected title: %#v", got)
	}
}

func TestDiscoverIndexesProjectsAndMarksCWD(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	sessionPath := filepath.Join(home, "projects", "encoded", "sid.jsonl")
	writeFile(t, sessionPath, `{"sessionId":"sid","cwd":`+jsonString(repo)+`,"timestamp":"2026-06-13T01:00:00Z","ai-title":"CodeBuddy title","model":"codebuddy-v1"}
`)

	got, err := New(home).Discover(session.DiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].ID != "sid" || got[0].Provider != Name || got[0].CWD != repo || got[0].Title != "CodeBuddy title" {
		t.Fatalf("unexpected session: %#v", got[0])
	}
	if got[0].Metadata["source_home"] != home || got[0].Metadata["model"] != "codebuddy-v1" {
		t.Fatalf("metadata = %#v", got[0].Metadata)
	}
}

func TestDiscoverKeepsSessionsWithMissingCWD(t *testing.T) {
	home := t.TempDir()
	sessionPath := filepath.Join(home, "projects", "encoded", "sid.jsonl")
	writeFile(t, sessionPath, `{"sessionId":"sid","timestamp":"2026-06-13T01:00:00Z","ai-title":"CodeBuddy title"}
`)

	got, err := New(home).Discover(session.DiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].ID != "sid" || got[0].CWD != "" || got[0].Metadata["cwd_missing"] != "true" {
		t.Fatalf("unexpected session: %#v", got[0])
	}
}

func TestResumeCommandUsesCodeBuddyResumeFromSessionCWD(t *testing.T) {
	spec := New("").ResumeCommand(session.Session{ID: "sid", CWD: "/repo"})

	if spec.Dir != "/repo" {
		t.Fatalf("Dir = %q", spec.Dir)
	}
	if strings.Join(spec.Args, " ") != "codebuddy --resume sid" {
		t.Fatalf("Args = %#v", spec.Args)
	}
}

func TestNewCommandUsesProjectCWD(t *testing.T) {
	spec := New("").NewCommand("/repo")

	if spec.Dir != "/repo" {
		t.Fatalf("Dir = %q", spec.Dir)
	}
	if strings.Join(spec.Args, " ") != "codebuddy" {
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

func jsonString(value string) string {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(data)
}
