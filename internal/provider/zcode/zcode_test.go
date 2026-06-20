package zcode

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/hxy91819/agent-session-manager/internal/session"
)

func TestDiscoverReadsSessionsFromSQLite(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	db := createZCodeDB(t, home)
	created := int64(1781882881955)
	updated := int64(1781882911024)
	writeZCodeSession(t, db, zcodeSession{
		ID:          "sess_one",
		Directory:   repo,
		Title:       "提交PR支持zcode",
		TitleSource: "generated",
		TimeCreated: created,
		TimeUpdated: updated,
	})
	closeDB(t, db)

	got, err := New(home).Discover(session.DiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	s := got[0]
	if s.ID != "sess_one" {
		t.Fatalf("ID = %q", s.ID)
	}
	if s.Provider != Name {
		t.Fatalf("Provider = %q", s.Provider)
	}
	if s.CWD != repo {
		t.Fatalf("CWD = %q", s.CWD)
	}
	if s.Title != "提交PR支持zcode" {
		t.Fatalf("Title = %q", s.Title)
	}
	if s.Metadata["title_source"] != "generated" {
		t.Fatalf("title_source = %q", s.Metadata["title_source"])
	}
	if !s.CreatedAt.Equal(time.UnixMilli(created).UTC()) {
		t.Fatalf("CreatedAt = %s, want %s", s.CreatedAt, time.UnixMilli(created).UTC())
	}
	if !s.UpdatedAt.Equal(time.UnixMilli(updated).UTC()) {
		t.Fatalf("UpdatedAt = %s, want %s", s.UpdatedAt, time.UnixMilli(updated).UTC())
	}
	if s.Path == "" {
		t.Fatal("Path should be the sqlite db path")
	}
}

func TestDiscoverUsesFirstUserMessageTitleFallback(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	db := createZCodeDB(t, home)
	created := int64(1781881688636)
	updated := int64(1781881718176)
	sess := writeZCodeSession(t, db, zcodeSession{
		ID:          "sess_fallback",
		Directory:   repo,
		Title:       "",
		TitleSource: "default",
		TimeCreated: created,
		TimeUpdated: updated,
	})
	addUserMessage(t, db, sess, "msg_one", created, "fix openclaw with zcode")
	closeDB(t, db)

	got, err := New(home).Discover(session.DiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].Title != "fix openclaw with zcode" {
		t.Fatalf("Title = %q", got[0].Title)
	}
	if got[0].Metadata["title_source"] != "first_input" {
		t.Fatalf("title_source = %q", got[0].Metadata["title_source"])
	}
}

func TestDiscoverSkipsInjectedContextTitle(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	db := createZCodeDB(t, home)
	created := int64(1781881688636)
	updated := int64(1781881718176)
	sess := writeZCodeSession(t, db, zcodeSession{
		ID:          "sess_injected",
		Directory:   repo,
		Title:       "",
		TitleSource: "default",
		TimeCreated: created,
		TimeUpdated: updated,
	})
	addUserMessage(t, db, sess, "msg_one", created, "# AGENTS.md instructions for /repo\n\n<INSTRUCTIONS>ignore me</INSTRUCTIONS>")
	closeDB(t, db)

	got, err := New(home).Discover(session.DiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].Title != "" {
		t.Fatalf("Title = %q, want empty", got[0].Title)
	}
	if _, ok := got[0].Metadata["title_source"]; ok {
		t.Fatalf("title_source should be absent, got %q", got[0].Metadata["title_source"])
	}
}

func TestDiscoverFiltersBySinceAndLimit(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	db := createZCodeDB(t, home)
	writeZCodeSession(t, db, zcodeSession{
		ID: "sess_old", Directory: repo, Title: "old",
		TitleSource: "generated", TimeCreated: 1781000000000, TimeUpdated: 1781000000000,
	})
	writeZCodeSession(t, db, zcodeSession{
		ID: "sess_new", Directory: repo, Title: "new",
		TitleSource: "generated", TimeCreated: 1782000000000, TimeUpdated: 1782000000000,
	})
	closeDB(t, db)

	since := time.UnixMilli(1781500000000).UTC()
	got, err := New(home).Discover(session.DiscoverOptions{Since: since, LimitFiles: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].ID != "sess_new" {
		t.Fatalf("ID = %q, want sess_new (newest first)", got[0].ID)
	}
}

func TestDiscoverAllHistoryWithLimit(t *testing.T) {
	// Regression: --since-days 0 (zero Since) plus --limit must still return
	// sessions. A bug bound the unset since value to the LIMIT placeholder and
	// returned zero sessions.
	home := t.TempDir()
	repo := t.TempDir()
	db := createZCodeDB(t, home)
	writeZCodeSession(t, db, zcodeSession{
		ID: "sess_old", Directory: repo, Title: "old",
		TitleSource: "generated", TimeCreated: 1781000000000, TimeUpdated: 1781000000000,
	})
	writeZCodeSession(t, db, zcodeSession{
		ID: "sess_new", Directory: repo, Title: "new",
		TitleSource: "generated", TimeCreated: 1782000000000, TimeUpdated: 1782000000000,
	})
	closeDB(t, db)

	got, err := New(home).Discover(session.DiscoverOptions{LimitFiles: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].ID != "sess_new" {
		t.Fatalf("ID = %q, want sess_new (newest first)", got[0].ID)
	}

	// No since and no limit: all history.
	got, err = New(home).Discover(session.DiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
}

func TestDiscoverMarksMissingCWD(t *testing.T) {
	home := t.TempDir()
	missing := filepath.Join(home, "missing")
	db := createZCodeDB(t, home)
	writeZCodeSession(t, db, zcodeSession{
		ID: "sess_missing", Directory: missing, Title: "missing",
		TitleSource: "generated", TimeCreated: 1781880000000, TimeUpdated: 1781880000000,
	})
	closeDB(t, db)

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

func TestDiscoverSkipsArchivedSessions(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	db := createZCodeDB(t, home)
	writeZCodeSession(t, db, zcodeSession{
		ID: "sess_active", Directory: repo, Title: "active",
		TitleSource: "generated", TimeCreated: 1781880000000, TimeUpdated: 1781880000000,
	})
	writeZCodeSession(t, db, zcodeSession{
		ID: "sess_archived", Directory: repo, Title: "archived",
		TitleSource: "generated", TimeCreated: 1781870000000, TimeUpdated: 1781870000000,
		Archived: true,
	})
	closeDB(t, db)

	got, err := New(home).Discover(session.DiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].ID != "sess_active" {
		t.Fatalf("ID = %q, want sess_active", got[0].ID)
	}
}

func TestDiscoverUserPreviews(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	db := createZCodeDB(t, home)
	created := int64(1781881688636)
	updated := int64(1781881718176)
	sess := writeZCodeSession(t, db, zcodeSession{
		ID: "sess_preview", Directory: repo, Title: "has title",
		TitleSource: "generated", TimeCreated: created, TimeUpdated: updated,
	})
	addUserMessage(t, db, sess, "msg_a", created, "first zcode prompt")
	addUserMessage(t, db, sess, "msg_b", updated, "last zcode prompt")
	closeDB(t, db)

	got, err := New(home).Discover(session.DiscoverOptions{
		Preview: session.PreviewOptions{UserMessagesPerEdge: 1, MaxChars: 50},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if len(got[0].Previews) != 2 {
		t.Fatalf("previews = %#v", got[0].Previews)
	}
	var texts []string
	for _, p := range got[0].Previews {
		texts = append(texts, p.Text)
		if p.Source != "zcode:message" {
			t.Fatalf("preview source = %q", p.Source)
		}
	}
	if !contains(texts, "first zcode prompt") || !contains(texts, "last zcode prompt") {
		t.Fatalf("previews = %#v", texts)
	}
}

func TestDiscoverReturnsNothingWhenDBMissing(t *testing.T) {
	home := t.TempDir()
	got, err := New(home).Discover(session.DiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("len = %d, want 0", len(got))
	}
}

func TestDiscoverRespectsProviderHomeOverride(t *testing.T) {
	repo := t.TempDir()
	home1 := t.TempDir()
	db1 := createZCodeDB(t, home1)
	writeZCodeSession(t, db1, zcodeSession{
		ID: "sess_in_home1", Directory: repo, Title: "home1",
		TitleSource: "generated", TimeCreated: 1781880000000, TimeUpdated: 1781880000000,
	})
	closeDB(t, db1)

	home2 := t.TempDir()
	db2 := createZCodeDB(t, home2)
	writeZCodeSession(t, db2, zcodeSession{
		ID: "sess_in_home2", Directory: repo, Title: "home2",
		TitleSource: "generated", TimeCreated: 1781880000000, TimeUpdated: 1781880000000,
	})
	closeDB(t, db2)

	got, err := New(home2).Discover(session.DiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "sess_in_home2" {
		t.Fatalf("unexpected sessions: %#v", got)
	}
}

func TestResumeCommandShape(t *testing.T) {
	spec := New("").ResumeCommand(session.Session{ID: "sess_one", CWD: "/repo"})
	if spec.Dir != "/repo" {
		t.Fatalf("Dir = %q", spec.Dir)
	}
	if strings.Join(spec.Args, " ") != "zcode --resume sess_one" {
		t.Fatalf("Args = %#v", spec.Args)
	}
}

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

type zcodeSession struct {
	ID          string
	Directory   string
	Title       string
	TitleSource string
	TimeCreated int64
	TimeUpdated int64
	Archived    bool
}

func createZCodeDB(t testing.TB, home string) *sql.DB {
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
	if err := db.Ping(); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	applyZCodeSchema(t, db)
	return db
}

func closeDB(t testing.TB, db *sql.DB) {
	t.Helper()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func applyZCodeSchema(t testing.TB, db *sql.DB) {
	t.Helper()
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
  share_url text,
  summary_additions integer,
  summary_deletions integer,
  summary_files integer,
  summary_diffs text,
  revert text,
  permission text,
  time_created integer not null,
  time_updated integer not null,
  time_compacting integer,
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
}

func writeZCodeSession(t testing.TB, db *sql.DB, s zcodeSession) zcodeSession {
	t.Helper()
	var archived any
	if s.Archived {
		archived = s.TimeUpdated
	}
	titleSource := s.TitleSource
	if titleSource == "" {
		titleSource = "default"
	}
	_, err := db.Exec(`INSERT INTO session (id, project_id, slug, directory, title, version, time_created, time_updated, title_source, time_archived)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ID, "proj_"+s.ID, s.ID, s.Directory, s.Title, "1", s.TimeCreated, s.TimeUpdated, titleSource, archived)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func addUserMessage(t testing.TB, db *sql.DB, sess zcodeSession, messageID string, createdAt int64, text string) {
	t.Helper()
	msgData, _ := json.Marshal(map[string]any{
		"role": "user",
		"time": map[string]any{"created": createdAt},
	})
	_, err := db.Exec(`INSERT INTO message (id, session_id, time_created, time_updated, data) VALUES (?, ?, ?, ?, ?)`,
		messageID, sess.ID, createdAt, createdAt, string(msgData))
	if err != nil {
		t.Fatal(err)
	}
	partID := "part_" + messageID
	partData, _ := json.Marshal(map[string]any{
		"type": "text",
		"text": text,
		"time": map[string]any{"start": createdAt, "end": createdAt},
	})
	_, err = db.Exec(`INSERT INTO part (id, message_id, session_id, time_created, time_updated, data) VALUES (?, ?, ?, ?, ?, ?)`,
		partID, messageID, sess.ID, createdAt, createdAt, string(partData))
	if err != nil {
		t.Fatal(err)
	}
}
