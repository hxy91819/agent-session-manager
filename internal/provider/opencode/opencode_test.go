package opencode

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hxy91819/agent-session-manager/internal/session"
	"github.com/hxy91819/agent-session-manager/internal/sessioncache"
)

func TestMain(m *testing.M) {
	cacheDir, err := os.MkdirTemp("", "asm-opencode-cache-*")
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

func TestDiscoverReadsSessionStorage(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	writeOpencodeSession(t, home, "project_one", "ses_one", repo, `{
  "id": "ses_one",
  "version": "1.1.11",
  "projectID": "project_one",
  "directory": `+quote(repo)+`,
  "title": "opencode title",
  "time": {"created": 1781312400000, "updated": 1781312460000}
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
	if got[0].Title != "opencode title" {
		t.Fatalf("Title = %q", got[0].Title)
	}
	if got[0].Metadata["title_source"] != "session" {
		t.Fatalf("title_source = %q", got[0].Metadata["title_source"])
	}
	if got[0].Metadata["project_id"] != "project_one" {
		t.Fatalf("project_id = %q", got[0].Metadata["project_id"])
	}
	if got[0].Metadata["version"] != "1.1.11" {
		t.Fatalf("version = %q", got[0].Metadata["version"])
	}
	if got[0].CreatedAt.Format(time.RFC3339) != "2026-06-13T01:00:00Z" {
		t.Fatalf("CreatedAt = %s", got[0].CreatedAt.Format(time.RFC3339))
	}
}

func TestDiscoverFallsBackToProjectWorktreeAndMessageTitle(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	projectID := "project_one"
	writeOpencodeProject(t, home, projectID, repo)
	writeOpencodeSession(t, home, projectID, "ses_one", "", `{
  "id": "ses_one",
  "projectID": "`+projectID+`",
  "title": "",
  "time": {"created": 1781322000}
}`)
	writeOpencodeMessage(t, home, "ses_one", "msg_ignored", "user", "# AGENTS.md instructions\nignore")
	writeOpencodeMessage(t, home, "ses_one", "msg_user", "user", "fallback\nprompt")

	got, err := New(home).Discover(session.DiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].CWD != repo {
		t.Fatalf("CWD = %q", got[0].CWD)
	}
	if got[0].Title != "fallback prompt" {
		t.Fatalf("Title = %q", got[0].Title)
	}
	if got[0].Metadata["title_source"] != "message" {
		t.Fatalf("title_source = %q", got[0].Metadata["title_source"])
	}
}

func TestDiscoverReadsUserPreviews(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	writeOpencodeSession(t, home, "project_one", "ses_one", repo, `{
  "id": "ses_one",
  "projectID": "project_one",
  "directory": `+quote(repo)+`,
  "title": "opencode title"
}`)
	messageIDs := []string{"msg_ignored", "msg_one", "msg_two", "msg_three", "msg_four", "msg_five"}
	texts := []string{
		"# AGENTS.md instructions\nignore",
		"first prompt",
		"second prompt",
		"third prompt",
		"fourth prompt",
		"fifth prompt",
	}
	base := time.Date(2026, 6, 13, 1, 0, 0, 0, time.UTC)
	for i, id := range messageIDs {
		writeOpencodeMessageAt(t, home, "ses_one", id, "user", texts[i], base.Add(time.Duration(i)*time.Minute))
		path := filepath.Join(home, "storage", "message", "ses_one", id+".json")
		// Deliberately reverse filesystem times: preview order must follow the
		// original message timestamps, not copy/sync mtimes.
		modTime := base.Add(time.Duration(len(messageIDs)-i) * time.Minute)
		if err := os.Chtimes(path, modTime, modTime); err != nil {
			t.Fatal(err)
		}
	}

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

func TestDiscoverDoesNotUseMessageModTimeAsReportEvidence(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	writeOpencodeSession(t, home, "project_one", "ses_one", repo, `{
  "id": "ses_one",
  "projectID": "project_one",
  "directory": `+quote(repo)+`,
  "title": "opencode title"
}`)
	writeOpencodeMessage(t, home, "ses_one", "msg_old", "user", "old prompt")
	start := time.Date(2026, 6, 17, 0, 0, 0, 0, time.UTC)
	messagePath := filepath.Join(home, "storage", "message", "ses_one", "msg_old.json")
	if err := os.Chtimes(messagePath, start.Add(time.Hour), start.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	got, err := New(home).Discover(session.DiscoverOptions{
		Preview: session.PreviewOptions{
			UserMessagesPerEdge: 2,
			MaxChars:            500,
			Since:               start,
			Before:              start.AddDate(0, 0, 1),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if len(got[0].Previews) != 0 {
		t.Fatalf("mtime-only previews = %#v, want none", got[0].Previews)
	}
	if got[0].Metadata[session.MetadataReportEvidenceStatus] != session.ReportEvidencePartial || got[0].Metadata[session.MetadataReportEvidenceNote] == "" {
		t.Fatalf("report evidence metadata = %#v", got[0].Metadata)
	}
}

func TestDiscoverFiltersAndLimitsBySessionModTime(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	writeOpencodeSession(t, home, "project_one", "ses_old", repo, `{"id":"ses_old","directory":`+quote(repo)+`,"title":"old"}`)
	writeOpencodeSession(t, home, "project_one", "ses_new", repo, `{"id":"ses_new","directory":`+quote(repo)+`,"title":"new"}`)

	since := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	oldTime := since.Add(-time.Hour)
	newTime := since.Add(time.Hour)
	if err := os.Chtimes(filepath.Join(home, "storage", "session", "project_one", "ses_old.json"), oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(home, "storage", "session", "project_one", "ses_new.json"), newTime, newTime); err != nil {
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
	writeOpencodeSession(t, home, "project_one", "ses_one", missing, `{"id":"ses_one","directory":`+quote(missing)+`,"title":"missing"}`)

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

func TestDiscoverRefreshesProjectAndMessageFallbacksWhenUsingCache(t *testing.T) {
	home := t.TempDir()
	projectID := "project_one"
	sessionID := "ses_one"
	writeOpencodeSession(t, home, projectID, sessionID, "", `{
  "id": "`+sessionID+`",
  "projectID": "`+projectID+`",
  "title": ""
}`)
	provider := Provider{
		Home:      home,
		CachePath: filepath.Join(t.TempDir(), "cache.json"),
	}

	got, err := provider.Discover(session.DiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no sessions before project worktree exists: %#v", got)
	}

	repo := t.TempDir()
	writeOpencodeProject(t, home, projectID, repo)
	writeOpencodeMessage(t, home, sessionID, "msg_user", "user", "fallback from message")
	got, err = provider.Discover(session.DiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].CWD != repo {
		t.Fatalf("CWD = %q", got[0].CWD)
	}
	if got[0].Title != "fallback from message" {
		t.Fatalf("Title = %q", got[0].Title)
	}
	if got[0].Metadata["title_source"] != "message" {
		t.Fatalf("title_source = %q", got[0].Metadata["title_source"])
	}
}

func TestDiscoverCacheLifecycleAndBoundedScanPreservesHistory(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "repo-created-after-warm")
	projectID := "project"
	path := filepath.Join(home, "storage", "session", projectID, "current.json")
	writeOpencodeSession(t, home, projectID, "current", repo, `{"id":"current","projectID":"project","directory":`+quote(repo)+`,"title":"before primary change"}`)
	cachePath := filepath.Join(t.TempDir(), "cache.json")
	provider := Provider{Home: home, CachePath: cachePath}

	cold, err := provider.Discover(session.DiscoverOptions{})
	if err != nil || len(cold) != 1 || cold[0].Title != "before primary change" || cold[0].Metadata["cwd_missing"] != "true" {
		t.Fatalf("cold = %#v err=%v", cold, err)
	}
	warm, err := provider.Discover(session.DiscoverOptions{})
	if err != nil || !reflect.DeepEqual(cold, warm) {
		t.Fatalf("warm = %#v err=%v, want %#v", warm, err, cold)
	}
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	refreshed, err := provider.Discover(session.DiscoverOptions{})
	if err != nil || refreshed[0].Metadata["cwd_missing"] != "" {
		t.Fatalf("cwd refresh = %#v err=%v", refreshed, err)
	}
	writeOpencodeSession(t, home, projectID, "current", repo, `{"id":"current","projectID":"project","directory":`+quote(repo)+`,"title":"after primary file changed and grew"}`)
	invalidated, err := provider.Discover(session.DiscoverOptions{})
	if err != nil || invalidated[0].Title != "after primary file changed and grew" {
		t.Fatalf("invalidated = %#v err=%v", invalidated, err)
	}
	if err := os.WriteFile(cachePath, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	recovered, err := provider.Discover(session.DiscoverOptions{})
	if err != nil || len(recovered) != 1 || recovered[0].Title != "after primary file changed and grew" {
		t.Fatalf("corrupt-cache recovery = %#v err=%v", recovered, err)
	}

	oldPath := filepath.Join(home, "storage", "session", projectID, "old.json")
	writeOpencodeSession(t, home, projectID, "old", repo, `{"id":"old","projectID":"project","directory":`+quote(repo)+`,"title":"old title"}`)
	oldTime := time.Now().AddDate(0, 0, -60)
	if err := os.Chtimes(oldPath, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Discover(session.DiscoverOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Discover(session.DiscoverOptions{Since: time.Now().AddDate(0, 0, -30)}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(oldPath)
	if err != nil {
		t.Fatal(err)
	}
	_, ok := sessioncache.Load(cachePath).Get(sessioncache.FileIdentity{
		Provider: Name, Path: oldPath, Size: info.Size(), ModTime: info.ModTime(),
	})
	if !ok {
		t.Fatal("bounded scan pruned the historical cache entry")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

func TestResumeCommandUsesOpencodeSessionFromSessionCWD(t *testing.T) {
	spec := New("").ResumeCommand(session.Session{ID: "ses_one", CWD: "/repo"})

	if spec.Dir != "/repo" {
		t.Fatalf("Dir = %q", spec.Dir)
	}
	if strings.Join(spec.Args, " ") != "opencode -s ses_one" {
		t.Fatalf("Args = %#v", spec.Args)
	}
}

func TestNewCommandUsesProjectCWD(t *testing.T) {
	spec := New("").NewCommand("/repo")

	if spec.Dir != "/repo" {
		t.Fatalf("Dir = %q", spec.Dir)
	}
	if strings.Join(spec.Args, " ") != "opencode" {
		t.Fatalf("Args = %#v", spec.Args)
	}
}

func writeOpencodeProject(t *testing.T, home, projectID, cwd string) {
	t.Helper()
	projectDir := filepath.Join(home, "storage", "project")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(projectDir, projectID+".json"), `{"id":`+quote(projectID)+`,"worktree":`+quote(cwd)+`}`)
}

func writeOpencodeSession(t *testing.T, home, projectID, id, cwd, content string) {
	t.Helper()
	sessionDir := filepath.Join(home, "storage", "session", projectID)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if content == "" {
		content = `{"id":` + quote(id) + `,"projectID":` + quote(projectID) + `,"directory":` + quote(cwd) + `}`
	}
	writeFile(t, filepath.Join(sessionDir, id+".json"), content)
}

func writeOpencodeMessage(t *testing.T, home, sessionID, messageID, role, text string) {
	t.Helper()
	messageDir := filepath.Join(home, "storage", "message", sessionID)
	partDir := filepath.Join(home, "storage", "part", messageID)
	if err := os.MkdirAll(messageDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(partDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(messageDir, messageID+".json"), `{"id":"`+messageID+`","sessionID":"`+sessionID+`","role":"`+role+`"}`)
	writeFile(t, filepath.Join(partDir, "part_one.json"), `{"type":"text","text":`+quote(text)+`}`)
}

func writeOpencodeMessageAt(t *testing.T, home, sessionID, messageID, role, text string, at time.Time) {
	t.Helper()
	messageDir := filepath.Join(home, "storage", "message", sessionID)
	partDir := filepath.Join(home, "storage", "part", messageID)
	if err := os.MkdirAll(messageDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(partDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(messageDir, messageID+".json"), `{"id":"`+messageID+`","sessionID":"`+sessionID+`","role":"`+role+`","time":{"created":`+fmt.Sprint(at.UnixMilli())+`}}`)
	writeFile(t, filepath.Join(partDir, "part_one.json"), `{"type":"text","text":`+quote(text)+`}`)
}

func quote(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	return `"` + replacer.Replace(value) + `"`
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
