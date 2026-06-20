package claude

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hxy91819/agent-session-manager/internal/session"
)

func TestMain(m *testing.M) {
	cacheDir, err := os.MkdirTemp("", "asm-claude-cache-*")
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

func TestParseSessionExtractsClaudeFields(t *testing.T) {
	input := strings.NewReader(`{"type":"user","sessionId":"sid","cwd":"/repo","timestamp":"2026-06-13T01:00:00Z","gitBranch":"main","message":{"role":"user","content":[{"type":"text","text":"first prompt"}]}}
{"type":"assistant","sessionId":"sid","cwd":"/repo","timestamp":"2026-06-13T01:01:00Z","message":{"role":"assistant","model":"claude-sonnet-4","content":[]}}
{"type":"user","sessionId":"sid","cwd":"/repo","timestamp":"2026-06-13T01:02:00Z","message":{"role":"user","content":"latest   user prompt"}}
{"type":"summary","sessionId":"sid","summary":"Native Claude Title"}
`)

	got, err := parseSession(input)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "sid" {
		t.Fatalf("ID = %q", got.ID)
	}
	if got.CWD != "/repo" {
		t.Fatalf("CWD = %q", got.CWD)
	}
	if got.Title != "Native Claude Title" {
		t.Fatalf("Title = %q", got.Title)
	}
	if got.Metadata["title_source"] != "summary" {
		t.Fatalf("title_source = %q", got.Metadata["title_source"])
	}
	if got.Metadata["model"] != "claude-sonnet-4" {
		t.Fatalf("model = %q", got.Metadata["model"])
	}
	if got.Metadata["git_branch"] != "main" {
		t.Fatalf("git_branch = %q", got.Metadata["git_branch"])
	}
	if got.CreatedAt.Format(time.RFC3339) != "2026-06-13T01:00:00Z" {
		t.Fatalf("CreatedAt = %s", got.CreatedAt.Format(time.RFC3339))
	}
	if got.UpdatedAt.Format(time.RFC3339) != "2026-06-13T01:02:00Z" {
		t.Fatalf("UpdatedAt = %s", got.UpdatedAt.Format(time.RFC3339))
	}
}

func TestParseSessionUsesLastHumanUserTitle(t *testing.T) {
	input := strings.NewReader(`{"type":"user","sessionId":"sid","cwd":"/repo","timestamp":"2026-06-13T01:00:00Z","isMeta":true,"message":{"role":"user","content":"ignored meta"}}
{"type":"user","sessionId":"sid","cwd":"/repo","timestamp":"2026-06-13T01:01:00Z","message":{"role":"user","content":"<system-reminder>ignore me</system-reminder>"}}
{"type":"user","sessionId":"sid","cwd":"/repo","timestamp":"2026-06-13T01:02:00Z","message":{"role":"user","content":[{"type":"text","text":"real\nprompt"}]}}
`)

	got, err := parseSession(input)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "real prompt" {
		t.Fatalf("Title = %q", got.Title)
	}
	if got.Metadata["title_source"] != "user" {
		t.Fatalf("title_source = %q", got.Metadata["title_source"])
	}
}

func TestDiscoverReadsUserPreviews(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	projectDir := filepath.Join(home, "projects", "-repo")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(projectDir, "session.jsonl"), `{"type":"user","sessionId":"sid","cwd":"`+repo+`","timestamp":"2026-06-13T01:00:00Z","isMeta":true,"message":{"role":"user","content":"ignored meta"}}
{"type":"user","sessionId":"sid","cwd":"`+repo+`","timestamp":"2026-06-13T01:00:01Z","message":{"role":"user","content":"<system-reminder>ignore me</system-reminder>"}}
{"type":"user","sessionId":"sid","cwd":"`+repo+`","timestamp":"2026-06-13T01:00:02Z","message":{"role":"user","content":"first prompt"}}
{"type":"user","sessionId":"sid","cwd":"`+repo+`","timestamp":"2026-06-13T01:00:03Z","message":{"role":"user","content":"second prompt"}}
{"type":"user","sessionId":"sid","cwd":"`+repo+`","timestamp":"2026-06-13T01:00:04Z","message":{"role":"user","content":"third prompt"}}
{"type":"user","sessionId":"sid","cwd":"`+repo+`","timestamp":"2026-06-13T01:00:05Z","message":{"role":"user","content":"fourth prompt"}}
{"type":"user","sessionId":"sid","cwd":"`+repo+`","timestamp":"2026-06-13T01:00:06Z","message":{"role":"user","content":"fifth prompt"}}
`)

	got, err := New(home).Discover(session.DiscoverOptions{
		Preview: session.PreviewOptions{UserMessagesPerEdge: 2, MaxChars: 500},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	want := []string{"first prompt", "second prompt", "fourth prompt", "fifth prompt"}
	if texts := previewTexts(got[0].Previews); strings.Join(texts, "|") != strings.Join(want, "|") {
		t.Fatalf("previews = %#v, want %#v", texts, want)
	}
}

func TestDiscoverFiltersAndLimitsByFileModTime(t *testing.T) {
	home := t.TempDir()
	projectDir := filepath.Join(home, "projects", "-repo")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	oldPath := filepath.Join(projectDir, "old.jsonl")
	newPath := filepath.Join(projectDir, "new.jsonl")
	writeClaudeSession(t, oldPath, "old", repo, "old title")
	writeClaudeSession(t, newPath, "new", repo, "new title")

	since := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	oldTime := since.Add(-time.Hour)
	newTime := since.Add(time.Hour)
	if err := os.Chtimes(oldPath, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(newPath, newTime, newTime); err != nil {
		t.Fatal(err)
	}

	got, err := New(home).Discover(session.DiscoverOptions{Since: since, LimitFiles: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].ID != "new" || got[0].Title != "new title" {
		t.Fatalf("unexpected session: %#v", got[0])
	}
	if !got[0].UpdatedAt.Equal(newTime) {
		t.Fatalf("UpdatedAt = %s, want %s", got[0].UpdatedAt, newTime)
	}
}

func TestDiscoverDeduplicatesSessionIDByNewestFile(t *testing.T) {
	home := t.TempDir()
	projectA := filepath.Join(home, "projects", "-repo-a")
	projectB := filepath.Join(home, "projects", "-repo-b")
	if err := os.MkdirAll(projectA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(projectB, 0o755); err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	oldPath := filepath.Join(projectA, "sid.jsonl")
	newPath := filepath.Join(projectB, "sid-copy.jsonl")
	writeClaudeSession(t, oldPath, "sid", repo, "old title")
	writeClaudeSession(t, newPath, "sid", repo, "new title")

	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(oldPath, base, base); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(newPath, base.Add(time.Hour), base.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	got, err := New(home).Discover(session.DiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].ID != "sid" || got[0].Title != "new title" || got[0].Path != newPath {
		t.Fatalf("unexpected session: %#v", got[0])
	}
}

func TestDiscoverMarksMissingCWD(t *testing.T) {
	home := t.TempDir()
	projectDir := filepath.Join(home, "projects", "-repo")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(home, "missing")
	writeClaudeSession(t, filepath.Join(projectDir, "session.jsonl"), "sid", missing, "title")

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
	projectDir := filepath.Join(home, "projects", "-repo")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(home, "repo")
	writeClaudeSession(t, filepath.Join(projectDir, "session.jsonl"), "sid", repo, "title")
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

func TestResumeCommandUsesClaudeResumeFromSessionCWD(t *testing.T) {
	spec := New("").ResumeCommand(session.Session{ID: "sid", CWD: "/repo"})

	if spec.Dir != "/repo" {
		t.Fatalf("Dir = %q", spec.Dir)
	}
	if strings.Join(spec.Args, " ") != "claude --resume sid" {
		t.Fatalf("Args = %#v", spec.Args)
	}
}

func TestNewCommandUsesProjectCWD(t *testing.T) {
	spec := New("").NewCommand("/repo")

	if spec.Dir != "/repo" {
		t.Fatalf("Dir = %q", spec.Dir)
	}
	if strings.Join(spec.Args, " ") != "claude" {
		t.Fatalf("Args = %#v", spec.Args)
	}
}

func writeClaudeSession(t *testing.T, path, id, cwd, title string) {
	t.Helper()
	writeFile(t, path, `{"type":"user","sessionId":"`+id+`","cwd":"`+cwd+`","timestamp":"2026-06-13T01:00:00Z","message":{"role":"user","content":"`+title+`"}}
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
