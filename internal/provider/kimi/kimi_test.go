package kimi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hxy91819/agent-session-manager/internal/session"
)

func TestDiscoverReadsIndexAndState(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	sessionDir := filepath.Join(home, "sessions", "wd_repo", "ses_one")
	writeKimiSession(t, home, sessionDir, "ses_one", repo, `{
  "createdAt": "2026-06-13T01:00:00.000Z",
  "updatedAt": "2026-06-13T01:10:00.000Z",
  "title": "Kimi title",
  "lastPrompt": "fallback prompt"
}`)

	got, err := New(home).Discover(session.DiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].ID != "ses_one" {
		t.Fatalf("ID = %q", got[0].ID)
	}
	if got[0].Provider != Name {
		t.Fatalf("Provider = %q", got[0].Provider)
	}
	if got[0].CWD != repo {
		t.Fatalf("CWD = %q", got[0].CWD)
	}
	if got[0].Title != "Kimi title" {
		t.Fatalf("Title = %q", got[0].Title)
	}
	if got[0].Metadata["title_source"] != "title" {
		t.Fatalf("title_source = %q", got[0].Metadata["title_source"])
	}
	if got[0].Metadata["session_dir"] != sessionDir {
		t.Fatalf("session_dir = %q", got[0].Metadata["session_dir"])
	}
	if got[0].CreatedAt.Format(time.RFC3339) != "2026-06-13T01:00:00Z" {
		t.Fatalf("CreatedAt = %s", got[0].CreatedAt.Format(time.RFC3339))
	}
}

func TestDiscoverUsesLastPromptTitleFallback(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	sessionDir := filepath.Join(home, "sessions", "wd_repo", "ses_one")
	writeKimiSession(t, home, sessionDir, "ses_one", repo, `{"lastPrompt":"latest\nprompt"}`)

	got, err := New(home).Discover(session.DiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].Title != "latest prompt" {
		t.Fatalf("Title = %q", got[0].Title)
	}
	if got[0].Metadata["title_source"] != "last_prompt" {
		t.Fatalf("title_source = %q", got[0].Metadata["title_source"])
	}
}

func TestDiscoverFiltersAndLimitsByStateModTime(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	oldDir := filepath.Join(home, "sessions", "wd_repo", "ses_old")
	newDir := filepath.Join(home, "sessions", "wd_repo", "ses_new")
	writeKimiSession(t, home, oldDir, "ses_old", repo, `{"title":"old"}`)
	writeKimiSession(t, home, newDir, "ses_new", repo, `{"title":"new"}`)

	since := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	oldTime := since.Add(-time.Hour)
	newTime := since.Add(time.Hour)
	if err := os.Chtimes(filepath.Join(oldDir, "state.json"), oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(newDir, "state.json"), newTime, newTime); err != nil {
		t.Fatal(err)
	}

	got, err := New(home).Discover(session.DiscoverOptions{Since: since, LimitFiles: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].ID != "ses_new" || got[0].Title != "new" {
		t.Fatalf("unexpected session: %#v", got[0])
	}
	if !got[0].UpdatedAt.Equal(newTime) {
		t.Fatalf("UpdatedAt = %s, want %s", got[0].UpdatedAt, newTime)
	}
}

func TestDiscoverMarksMissingCWD(t *testing.T) {
	home := t.TempDir()
	missing := filepath.Join(home, "missing")
	sessionDir := filepath.Join(home, "sessions", "wd_repo", "ses_one")
	writeKimiSession(t, home, sessionDir, "ses_one", missing, `{"title":"missing"}`)

	got, err := New(home).Discover(session.DiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].Metadata["cwd_missing"] != "true" {
		t.Fatalf("cwd_missing = %q", got[0].Metadata["cwd_missing"])
	}
}

func TestResumeCommandUsesKimiSessionFromSessionCWD(t *testing.T) {
	spec := New("").ResumeCommand(session.Session{ID: "ses_one", CWD: "/repo"})

	if spec.Dir != "/repo" {
		t.Fatalf("Dir = %q", spec.Dir)
	}
	if strings.Join(spec.Args, " ") != "kimi --session ses_one" {
		t.Fatalf("Args = %#v", spec.Args)
	}
}

func writeKimiSession(t *testing.T, home, sessionDir, id, cwd, state string) {
	t.Helper()
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	indexPath := filepath.Join(home, "session_index.jsonl")
	line := `{"sessionId":"` + id + `","sessionDir":"` + sessionDir + `","workDir":"` + cwd + `"}`
	f, err := os.OpenFile(indexPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(line + "\n"); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(sessionDir, "state.json"), state)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
