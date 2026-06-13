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
