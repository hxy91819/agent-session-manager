package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"session-manager/internal/session"
)

func TestParseSessionPrefersLatestTurnContextCWD(t *testing.T) {
	input := strings.NewReader(`{"timestamp":"2026-06-13T01:00:00Z","type":"session_meta","payload":{"id":"sid","timestamp":"2026-06-13T01:00:00Z","cwd":"/old"}}
{"timestamp":"2026-06-13T01:01:00Z","type":"turn_context","payload":{"cwd":"/new","model":"gpt-5"}}
`)

	got, err := parseSession(input)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "sid" {
		t.Fatalf("ID = %q", got.ID)
	}
	if got.CWD != "/new" {
		t.Fatalf("CWD = %q, want /new", got.CWD)
	}
	if got.Metadata["model"] != "gpt-5" {
		t.Fatalf("model metadata = %q", got.Metadata["model"])
	}
}

func TestParseSessionExtractsLastHumanUserTitle(t *testing.T) {
	input := strings.NewReader(`{"timestamp":"2026-06-13T01:00:00Z","type":"session_meta","payload":{"id":"sid","timestamp":"2026-06-13T01:00:00Z","cwd":"/repo"}}
{"timestamp":"2026-06-13T01:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"# AGENTS.md instructions for /repo\n\n<INSTRUCTIONS>ignore me</INSTRUCTIONS>"}]}}
{"timestamp":"2026-06-13T01:00:02Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"first real prompt"}]}}
{"timestamp":"2026-06-13T01:00:03Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"latest\nreal   prompt"}]}}
`)

	got, err := parseSession(input)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "latest real prompt" {
		t.Fatalf("Title = %q", got.Title)
	}
}

func TestParseSessionSkipsInjectedUserContexts(t *testing.T) {
	input := strings.NewReader(`{"timestamp":"2026-06-13T01:00:00Z","type":"session_meta","payload":{"id":"sid","timestamp":"2026-06-13T01:00:00Z","cwd":"/repo"}}
{"timestamp":"2026-06-13T01:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context>\n  <cwd>/repo</cwd>\n</environment_context>"}]}}
`)

	got, err := parseSession(input)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "" {
		t.Fatalf("Title = %q, want empty", got.Title)
	}
}

func TestDiscoverReadsHistoryTitleAndLimitsFiles(t *testing.T) {
	home := t.TempDir()
	sessionDir := filepath.Join(home, "sessions", "2026", "06", "13")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}

	writeSession(t, filepath.Join(sessionDir, "old.jsonl"), "old", "/repo/old")
	writeSession(t, filepath.Join(sessionDir, "new.jsonl"), "new", "/repo/new")
	writeFile(t, filepath.Join(home, "history.jsonl"), `{"session_id":"old","text":"old title"}
{"session_id":"new","text":"new title"}
`)

	oldTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	newTime := oldTime.Add(time.Hour)
	if err := os.Chtimes(filepath.Join(sessionDir, "old.jsonl"), oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(sessionDir, "new.jsonl"), newTime, newTime); err != nil {
		t.Fatal(err)
	}

	got, err := New(home).Discover(session.DiscoverOptions{LimitFiles: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].ID != "new" || got[0].Title != "new title" {
		t.Fatalf("unexpected session: %#v", got[0])
	}
}

func TestDiscoverSkipsSessionDateDirectoriesBeforeSince(t *testing.T) {
	home := t.TempDir()
	oldDir := filepath.Join(home, "sessions", "2025", "01", "01")
	newDir := filepath.Join(home, "sessions", "2026", "06", "13")
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(newDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeSession(t, filepath.Join(oldDir, "old.jsonl"), "old", "/repo/old")
	writeSession(t, filepath.Join(newDir, "new.jsonl"), "new", "/repo/new")

	got, err := New(home).Discover(session.DiscoverOptions{
		Since: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "new" {
		t.Fatalf("got %#v", got)
	}
}

func TestShouldSkipSessionDateDir(t *testing.T) {
	root := filepath.Join("root", "sessions")
	since := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	if !shouldSkipSessionDateDir(root, filepath.Join(root, "2026", "05", "31"), since) {
		t.Fatal("expected older day to be skipped")
	}
	if shouldSkipSessionDateDir(root, filepath.Join(root, "2026", "06", "01"), since) {
		t.Fatal("expected since day to be kept")
	}
	if shouldSkipSessionDateDir(root, filepath.Join(root, "2026", "06"), since) {
		t.Fatal("expected month directory to be kept")
	}
}

func TestResumeCommandUsesSessionCWD(t *testing.T) {
	spec := New("").ResumeCommand(session.Session{ID: "sid", CWD: "/repo"})

	if spec.Dir != "/repo" {
		t.Fatalf("Dir = %q", spec.Dir)
	}
	if strings.Join(spec.Args, " ") != "codex resume sid" {
		t.Fatalf("Args = %#v", spec.Args)
	}
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
