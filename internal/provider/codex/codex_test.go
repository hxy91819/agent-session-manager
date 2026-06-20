package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hxy91819/agent-session-manager/internal/session"
)

func TestMain(m *testing.M) {
	cacheDir, err := os.MkdirTemp("", "asm-codex-cache-*")
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
{"timestamp":"2026-06-13T01:00:02Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<skill>\n<name>autoreview</name>\n</skill>"}]}}
{"timestamp":"2026-06-13T01:00:03Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"The following is the Codex agent history added since your last approval assessment."}]}}
{"timestamp":"2026-06-13T01:00:04Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"The following is the Codex agent history whose request action you are assessing."}]}}
`)

	got, err := parseSession(input)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "" {
		t.Fatalf("Title = %q, want empty", got.Title)
	}
}

func TestDiscoverReadsUserPreviews(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	sessionDir := filepath.Join(home, "sessions", "2026", "06", "13")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(sessionDir, "session.jsonl"), `{"timestamp":"2026-06-13T01:00:00Z","type":"session_meta","payload":{"id":"sid","timestamp":"2026-06-13T01:00:00Z","cwd":"`+repo+`"}}
{"timestamp":"2026-06-13T01:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"# AGENTS.md instructions for /repo\nignore"}]}}
{"timestamp":"2026-06-13T01:00:02Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"first prompt"}]}}
{"timestamp":"2026-06-13T01:00:03Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"second prompt with extra words"}]}}
{"timestamp":"2026-06-13T01:00:04Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"third prompt"}]}}
{"timestamp":"2026-06-13T01:00:05Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fourth prompt"}]}}
{"timestamp":"2026-06-13T01:00:06Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fifth prompt"}]}}
`)

	got, err := New(home).Discover(session.DiscoverOptions{
		Preview: session.PreviewOptions{UserMessagesPerEdge: 2, MaxChars: 20},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	want := []string{"first prompt", "second prompt with e", "fourth prompt", "fifth prompt"}
	if texts := previewTexts(got[0].Previews); strings.Join(texts, "|") != strings.Join(want, "|") {
		t.Fatalf("previews = %#v, want %#v", texts, want)
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

func TestDiscoverPrefersSessionIndexTitle(t *testing.T) {
	home := t.TempDir()
	sessionDir := filepath.Join(home, "sessions", "2026", "06", "13")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}

	writeFile(t, filepath.Join(sessionDir, "session.jsonl"), `{"timestamp":"2026-06-13T01:00:00Z","type":"session_meta","payload":{"id":"sid","timestamp":"2026-06-13T01:00:00Z","cwd":"/repo"}}
{"timestamp":"2026-06-13T01:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"rollout title"}]}}
`)
	writeFile(t, filepath.Join(home, "history.jsonl"), `{"session_id":"sid","text":"history title"}
`)
	writeFile(t, filepath.Join(home, "session_index.jsonl"), `{"id":"sid","thread_name":"older native title","updated_at":"2026-06-13T01:00:00Z"}
{"id":"sid","thread_name":"native title","updated_at":"2026-06-13T01:01:00Z"}
`)

	got, err := New(home).Discover(session.DiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].Title != "native title" {
		t.Fatalf("Title = %q", got[0].Title)
	}
	if got[0].Metadata["title_source"] != "session_index" {
		t.Fatalf("title_source = %q", got[0].Metadata["title_source"])
	}
}

func TestReadSessionIndexTitlesIgnoresEmptyNames(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "session_index.jsonl")
	writeFile(t, path, `{"id":"sid","thread_name":"native title","updated_at":"2026-06-13T01:00:00Z"}
{"id":"sid","thread_name":"  ","updated_at":"2026-06-13T01:01:00Z"}
{"id":"other","thread_name":"other title","updated_at":"2026-06-13T01:02:00Z"}
`)

	got := readSessionIndexTitles(path)
	if got["sid"] != "native title" {
		t.Fatalf("sid title = %q", got["sid"])
	}
	if got["other"] != "other title" {
		t.Fatalf("other title = %q", got["other"])
	}
}

func TestDiscoverMarksMissingCWD(t *testing.T) {
	home := t.TempDir()
	sessionDir := filepath.Join(home, "sessions", "2026", "06", "13")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}

	missing := filepath.Join(home, "missing-repo")
	writeSession(t, filepath.Join(sessionDir, "session.jsonl"), "sid", missing)

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

func TestDiscoverRefreshesCWDStatusWhenUsingCache(t *testing.T) {
	home := t.TempDir()
	sessionDir := filepath.Join(home, "sessions", "2026", "06", "13")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}

	repo := filepath.Join(home, "repo")
	writeSession(t, filepath.Join(sessionDir, "session.jsonl"), "sid", repo)
	provider := Provider{
		Home:      home,
		CachePath: filepath.Join(t.TempDir(), "cache.json"),
	}

	got, err := provider.Discover(session.DiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Metadata["cwd_missing"] != "true" {
		t.Fatalf("first discovery did not mark missing cwd: %#v", got)
	}

	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err = provider.Discover(session.DiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].Metadata["cwd_missing"] != "" || got[0].Metadata["cwd_error"] != "" {
		t.Fatalf("cached discovery kept stale cwd metadata: %#v", got[0].Metadata)
	}
}

func TestDiscoverFiltersByFileModTimeNotDateDirectory(t *testing.T) {
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

	since := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	recent := since.Add(time.Hour)
	stale := since.Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(oldDir, "old.jsonl"), recent, recent); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(newDir, "new.jsonl"), stale, stale); err != nil {
		t.Fatal(err)
	}

	got, err := New(home).Discover(session.DiscoverOptions{
		Since: since,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "old" {
		t.Fatalf("got %#v", got)
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

func TestNewCommandUsesProjectCWD(t *testing.T) {
	spec := New("").NewCommand("/repo")

	if spec.Dir != "/repo" {
		t.Fatalf("Dir = %q", spec.Dir)
	}
	if strings.Join(spec.Args, " ") != "codex" {
		t.Fatalf("Args = %#v", spec.Args)
	}
}

func writeSession(t *testing.T, path, id, cwd string) {
	t.Helper()
	writeFile(t, path, `{"timestamp":"2026-06-13T01:00:00Z","type":"session_meta","payload":{"id":"`+id+`","timestamp":"2026-06-13T01:00:00Z","cwd":"`+cwd+`"}}
`)
}

func previewTexts(previews []session.MessagePreview) []string {
	out := make([]string, 0, len(previews))
	for _, preview := range previews {
		out = append(out, preview.Text)
	}
	return out
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
