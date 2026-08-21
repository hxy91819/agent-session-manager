package opencode

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver used by opencode.db fixtures

	"github.com/hxy91819/agent-session-manager/internal/session"
	"github.com/hxy91819/agent-session-manager/internal/sessioncache"
	"github.com/hxy91819/agent-session-manager/internal/sessiontest"
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
	if err != nil {
		t.Fatal(err)
	}
	sessiontest.RequireEqual(t, cold, warm)
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

func TestDiscoverReadsSessionsFromSQLiteStore(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	db := createOpencodeDB(t, home)
	created := time.Date(2026, 8, 19, 10, 0, 0, 0, time.UTC)
	updated := created.Add(3 * time.Minute)
	writeOpencodeDBSession(t, db, opencodeDBSessionFixture{
		ID:        "ses_db",
		Directory: repo,
		Title:     "dsh-plugin-install 功能实现",
		Version:   "1.18.19",
		CreatedAt: created,
		UpdatedAt: updated,
	})
	writeOpencodeDBSession(t, db, opencodeDBSessionFixture{
		ID:        "ses_archived",
		Directory: repo,
		Title:     "archived away",
		CreatedAt: created,
		UpdatedAt: updated,
		Archived:  updated.UnixMilli(),
	})
	closeDB(t, db)

	got, err := New(home).Discover(session.DiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1 (archived sessions stay hidden)", len(got))
	}
	s := got[0]
	if s.ID != "ses_db" || s.Provider != Name {
		t.Fatalf("unexpected session id/provider: %#v", s)
	}
	if s.CWD != repo {
		t.Fatalf("CWD = %q", s.CWD)
	}
	if s.Title != "dsh-plugin-install 功能实现" {
		t.Fatalf("Title = %q", s.Title)
	}
	if s.Metadata["title_source"] != "session" {
		t.Fatalf("title_source = %q", s.Metadata["title_source"])
	}
	if s.Metadata["version"] != "1.18.19" {
		t.Fatalf("version = %q", s.Metadata["version"])
	}
	if !s.CreatedAt.Equal(created) {
		t.Fatalf("CreatedAt = %s, want %s", s.CreatedAt, created)
	}
	if !s.UpdatedAt.Equal(updated) {
		t.Fatalf("UpdatedAt = %s, want %s", s.UpdatedAt, updated)
	}
	wantPath := filepath.Join(home, "opencode.db")
	if s.Path != wantPath {
		t.Fatalf("Path = %q, want %q", s.Path, wantPath)
	}
}

func TestDiscoverPrefersSQLiteStoreOverLegacyJSON(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	// The one-time opencode storage migration imports legacy JSON sessions into
	// opencode.db, so when the DB exists it is authoritative: scanning both
	// stores would surface the same session twice with divergent titles.
	db := createOpencodeDB(t, home)
	now := time.Now().UTC()
	writeOpencodeDBSession(t, db, opencodeDBSessionFixture{
		ID:        "ses_migrated",
		Directory: repo,
		Title:     "db title wins",
		CreatedAt: now,
		UpdatedAt: now,
	})
	closeDB(t, db)
	writeOpencodeSession(t, home, "project_one", "ses_migrated", repo, `{
  "id": "ses_migrated",
  "projectID": "project_one",
  "directory": `+quote(repo)+`,
  "title": "stale json title",
  "time": {"created": `+fmt.Sprint(now.UnixMilli())+`, "updated": `+fmt.Sprint(now.UnixMilli())+`}
}`)
	writeOpencodeSession(t, home, "project_one", "ses_json_only", repo, `{
  "id": "ses_json_only",
  "projectID": "project_one",
  "directory": `+quote(repo)+`,
  "title": "json-only leftover",
  "time": {"created": `+fmt.Sprint(now.UnixMilli())+`, "updated": `+fmt.Sprint(now.UnixMilli())+`}
}`)

	got, err := New(home).Discover(session.DiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want exactly the DB session: %#v", len(got), got)
	}
	if got[0].ID != "ses_migrated" || got[0].Title != "db title wins" {
		t.Fatalf("unexpected session: %#v", got[0])
	}
}

func TestDiscoverDBFallsBackToProjectWorktreeAndRecordsParent(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	db := createOpencodeDB(t, home)
	now := time.Now().UTC()
	writeOpencodeDBProject(t, db, "proj_worktree", repo)
	writeOpencodeDBSession(t, db, opencodeDBSessionFixture{
		ID:        "ses_parent",
		ProjectID: "proj_worktree",
		Directory: repo,
		Title:     "parent work",
		CreatedAt: now,
		UpdatedAt: now,
	})
	writeOpencodeDBSession(t, db, opencodeDBSessionFixture{
		ID:        "ses_child",
		ProjectID: "proj_worktree",
		ParentID:  "ses_parent",
		Directory: "",
		Title:     "delegated task",
		CreatedAt: now,
		UpdatedAt: now,
	})
	closeDB(t, db)

	got, err := New(home).Discover(session.DiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	byID := make(map[string]session.Session, len(got))
	for _, item := range got {
		byID[item.ID] = item
	}
	child := byID["ses_child"]
	if child.CWD != repo {
		t.Fatalf("child CWD = %q, want project worktree %q", child.CWD, repo)
	}
	if child.Metadata[session.MetadataParentThreadID] != "ses_parent" {
		t.Fatalf("parent_thread_id = %q", child.Metadata[session.MetadataParentThreadID])
	}
	if parent := byID["ses_parent"]; parent.Metadata[session.MetadataParentThreadID] != "" {
		t.Fatalf("root session unexpectedly marked as child: %q", parent.Metadata[session.MetadataParentThreadID])
	}
}

func TestDiscoverDBPlaceholderTitleFallsBackToFirstUserMessage(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	db := createOpencodeDB(t, home)
	base := time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)
	writeOpencodeDBSession(t, db, opencodeDBSessionFixture{
		ID:        "ses_placeholder",
		Directory: repo,
		Title:     "New session - 2026-08-20T01:00:00.000Z",
		CreatedAt: base,
		UpdatedAt: base,
	})
	writeOpencodeDBMessage(t, db, "ses_placeholder", "msg_first", "user", "first prompt: search opencode sessions", base.Add(time.Minute))
	writeOpencodeDBMessage(t, db, "ses_placeholder", "msg_second", "user", "second prompt", base.Add(2*time.Minute))
	// A non-user message before the first real prompt must not become the title.
	writeOpencodeDBMessage(t, db, "ses_placeholder", "msg_assistant", "assistant", "assistant text", base)
	closeDB(t, db)

	got, err := New(home).Discover(session.DiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].Title != "first prompt: search opencode sessions" {
		t.Fatalf("Title = %q", got[0].Title)
	}
	if got[0].Metadata["title_source"] != "first_input" {
		t.Fatalf("title_source = %q", got[0].Metadata["title_source"])
	}
}

func TestDiscoverDBSkipsSyntheticTextParts(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	db := createOpencodeDB(t, home)
	base := time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)
	writeOpencodeDBSession(t, db, opencodeDBSessionFixture{
		ID: "ses_synthetic", Directory: repo,
		Title:     "New session - 2026-08-20T01:00:00.000Z",
		CreatedAt: base, UpdatedAt: base,
	})
	writeOpencodeDBMessageWithSyntheticPart(t, db, "ses_synthetic", "msg_first", base.Add(time.Minute), "injected context", "real user prompt")
	closeDB(t, db)

	got, err := New(home).Discover(session.DiscoverOptions{
		Preview: session.PreviewOptions{UserMessagesPerEdge: 1, MaxChars: 500},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Title != "real user prompt" {
		t.Fatalf("synthetic part became title: %#v", got)
	}
	if len(got[0].Previews) != 1 || got[0].Previews[0].Text != "real user prompt" {
		t.Fatalf("synthetic part became preview: %#v", got[0].Previews)
	}
}

func TestDiscoverDBPlaceholderTitleSurvivesWithoutUserMessages(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	db := createOpencodeDB(t, home)
	base := time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)
	writeOpencodeDBSession(t, db, opencodeDBSessionFixture{
		ID:        "ses_empty_chat",
		Directory: repo,
		Title:     "New session - 2026-08-20T01:00:00.000Z",
		CreatedAt: base,
		UpdatedAt: base,
	})
	closeDB(t, db)

	got, err := New(home).Discover(session.DiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Title != "New session - 2026-08-20T01:00:00.000Z" {
		t.Fatalf("placeholder title should remain visible when nothing better exists: %#v", got)
	}
}

func TestDiscoverDBReadsUserPreviews(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	db := createOpencodeDB(t, home)
	base := time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)
	writeOpencodeDBSession(t, db, opencodeDBSessionFixture{
		ID:        "ses_preview",
		Directory: repo,
		Title:     "preview subject",
		CreatedAt: base,
		UpdatedAt: base,
	})
	texts := []string{"first prompt", "second prompt", "third prompt", "fourth prompt", "fifth prompt"}
	for i, text := range texts {
		writeOpencodeDBMessage(t, db, "ses_preview", fmt.Sprintf("msg_%d", i), "user", text, base.Add(time.Duration(i)*time.Minute))
	}
	writeOpencodeDBMessage(t, db, "ses_preview", "msg_tool", "assistant", "assistant reply", base.Add(9*time.Minute))
	closeDB(t, db)

	got, err := New(home).Discover(session.DiscoverOptions{
		Preview: session.PreviewOptions{UserMessagesPerEdge: 2, MaxChars: 500},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"first prompt", "second prompt", "fourth prompt", "fifth prompt"}
	if texts := previewTexts(got[0].Previews); strings.Join(texts, "|") != strings.Join(want, "|") {
		t.Fatalf("previews = %#v, want %#v", texts, want)
	}
	for _, preview := range got[0].Previews {
		if preview.Source != "opencode:message" {
			t.Fatalf("preview source = %q", preview.Source)
		}
	}
}

func TestDiscoverDBFiltersBySinceAndLimit(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	db := createOpencodeDB(t, home)
	base := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	writeOpencodeDBSession(t, db, opencodeDBSessionFixture{
		ID: "ses_old", Directory: repo, Title: "old",
		CreatedAt: base, UpdatedAt: base,
	})
	writeOpencodeDBSession(t, db, opencodeDBSessionFixture{
		ID: "ses_new", Directory: repo, Title: "new",
		CreatedAt: base.AddDate(0, 0, 10), UpdatedAt: base.AddDate(0, 0, 10),
	})
	closeDB(t, db)

	got, err := New(home).Discover(session.DiscoverOptions{
		Since:      base.AddDate(0, 0, 5),
		LimitFiles: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "ses_new" {
		t.Fatalf("since/limit = %#v", got)
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

// The schema mirrors the core columns of opencode's drizzle-managed
// opencode.db (v1.18+); aux indexes/FKs are irrelevant to discovery.
const opencodeDBSchema = `
CREATE TABLE session (
  id text primary key,
  project_id text not null,
  parent_id text,
  slug text not null,
  directory text not null,
  title text not null,
  version text not null,
  time_created integer not null,
  time_updated integer not null,
  time_archived integer
);
CREATE TABLE project (
  id text primary key,
  worktree text not null
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

type opencodeDBSessionFixture struct {
	ID        string
	ProjectID string
	ParentID  string
	Directory string
	Title     string
	Version   string
	CreatedAt time.Time
	UpdatedAt time.Time
	Archived  int64
}

func createOpencodeDB(t testing.TB, home string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(home, "opencode.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(opencodeDBSchema); err != nil {
		t.Fatal(err)
	}
	return db
}

func closeDB(t testing.TB, db *sql.DB) {
	t.Helper()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func writeOpencodeDBSession(t testing.TB, db *sql.DB, item opencodeDBSessionFixture) {
	t.Helper()
	projectID := item.ProjectID
	if projectID == "" {
		projectID = "proj_" + item.ID
	}
	var archived any
	if item.Archived > 0 {
		archived = item.Archived
	}
	if _, err := db.Exec(`INSERT INTO session (id, project_id, parent_id, slug, directory, title, version, time_created, time_updated, time_archived)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		item.ID, projectID, nilIfEmpty(item.ParentID), item.ID, item.Directory, item.Title, item.Version,
		item.CreatedAt.UnixMilli(), item.UpdatedAt.UnixMilli(), archived); err != nil {
		t.Fatal(err)
	}
}

func writeOpencodeDBProject(t testing.TB, db *sql.DB, id, worktree string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO project (id, worktree) VALUES (?, ?)`, id, worktree); err != nil {
		t.Fatal(err)
	}
}

func writeOpencodeDBMessage(t testing.TB, db *sql.DB, sessionID, messageID, role, text string, at time.Time) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO message (id, session_id, time_created, time_updated, data) VALUES (?, ?, ?, ?, ?)`,
		messageID, sessionID, at.UnixMilli(), at.UnixMilli(),
		fmt.Sprintf(`{"role":%q,"time":{"created":%d}}`, role, at.UnixMilli())); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO part (id, message_id, session_id, time_created, time_updated, data) VALUES (?, ?, ?, ?, ?, ?)`,
		"part_"+messageID, messageID, sessionID, at.UnixMilli(), at.UnixMilli(),
		fmt.Sprintf(`{"type":"text","text":%q}`, text)); err != nil {
		t.Fatal(err)
	}
}

func writeOpencodeDBMessageWithSyntheticPart(t testing.TB, db *sql.DB, sessionID, messageID string, at time.Time, syntheticText, userText string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO message (id, session_id, time_created, time_updated, data) VALUES (?, ?, ?, ?, ?)`,
		messageID, sessionID, at.UnixMilli(), at.UnixMilli(),
		fmt.Sprintf(`{"role":"user","time":{"created":%d}}`, at.UnixMilli())); err != nil {
		t.Fatal(err)
	}
	for _, part := range []struct {
		id        string
		text      string
		synthetic bool
	}{
		{id: "part_" + messageID + "_synthetic", text: syntheticText, synthetic: true},
		{id: "part_" + messageID + "_user", text: userText},
	} {
		data := fmt.Sprintf(`{"type":"text","text":%q,"synthetic":%t}`, part.text, part.synthetic)
		if _, err := db.Exec(`INSERT INTO part (id, message_id, session_id, time_created, time_updated, data) VALUES (?, ?, ?, ?, ?, ?)`,
			part.id, messageID, sessionID, at.UnixMilli(), at.UnixMilli(), data); err != nil {
			t.Fatal(err)
		}
	}
}

func nilIfEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
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
