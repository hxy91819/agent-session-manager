package zcode

import (
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver, registered as "sqlite"

	"github.com/hxy91819/agent-session-manager/internal/cwdstatus"
	"github.com/hxy91819/agent-session-manager/internal/session"
)

const Name = "zcode"

// sessioncache: not required - zcode discovery reads a single SQLite database
// with indexed queries, so there are no per-session files to cache by path,
// size, and mtime.
type Provider struct {
	Home string
}

func New(home string) Provider {
	return Provider{Home: home}
}

func (p Provider) Name() string {
	return Name
}

func (p Provider) Discover(opts session.DiscoverOptions) ([]session.Session, error) {
	home, err := p.home()
	if err != nil {
		return nil, err
	}
	dbPath := filepath.Join(home, "cli", "db", "db.sqlite")
	info, err := os.Stat(dbPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	dbMtime := info.ModTime()

	db, err := openDB(dbPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()

	// Push --since and --limit into SQL so default discovery only scans rows in
	// the active window instead of the full ZCode history. ZCode stores
	// time_updated as a millisecond Unix epoch; --since-days 0 leaves Since zero
	// and the query unbounded, matching the documented all-history mode.
	query := sessionQuery(opts)
	var queryArgs []any
	if !opts.Since.IsZero() {
		queryArgs = append(queryArgs, opts.Since.UnixMilli())
	}
	if opts.LimitFiles > 0 {
		queryArgs = append(queryArgs, opts.LimitFiles)
	}
	rows, err := db.Query(query, queryArgs...)
	if err != nil {
		return nil, fmt.Errorf("zcode query sessions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	type pending struct {
		rec sessionRecord
		mod time.Time
	}
	var pendingSessions []pending
	for rows.Next() {
		var rec sessionRecord
		var archived sql.NullInt64
		if err := rows.Scan(&rec.ID, &rec.Directory, &rec.Title, &rec.TitleSource,
			&rec.TimeCreated, &rec.TimeUpdated, &rec.Path, &rec.Slug,
			&rec.ProjectID, &archived); err != nil {
			return nil, fmt.Errorf("zcode scan session: %w", err)
		}
		if strings.TrimSpace(rec.ID) == "" || strings.TrimSpace(rec.Directory) == "" {
			continue
		}
		updated := unixMillis(rec.TimeUpdated)
		if updated.IsZero() {
			updated = dbMtime
		}
		// Archived sessions are already excluded by the SQL WHERE clause
		// (time_archived IS NULL); the scanned column is retained for schema parity.
		_ = archived
		pendingSessions = append(pendingSessions, pending{rec: rec, mod: updated})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("zcode iterate sessions: %w", err)
	}

	// The session query orders by time_updated DESC and applies --since and
	// --limit at the SQL boundary so default discovery stays cheap. The limit
	// here is a defensive cap in case the DB returns more rows than requested.
	if opts.LimitFiles > 0 && len(pendingSessions) > opts.LimitFiles {
		pendingSessions = pendingSessions[:opts.LimitFiles]
	}

	cwdChecker := cwdstatus.NewChecker()
	sessions := make([]session.Session, 0, len(pendingSessions))
	for _, item := range pendingSessions {
		rec := item.rec
		s := session.Session{
			ID:        strings.TrimSpace(rec.ID),
			Provider:  Name,
			CWD:       strings.TrimSpace(rec.Directory),
			Title:     cleanTitle(rec.Title),
			CreatedAt: unixMillis(rec.TimeCreated),
			UpdatedAt: item.mod,
			Path:      dbPath,
			Metadata:  make(map[string]string),
		}
		if s.CreatedAt.IsZero() {
			s.CreatedAt = item.mod
		}
		if source := strings.TrimSpace(rec.TitleSource); source != "" {
			s.Metadata["title_source"] = source
		}
		if rec.ProjectID.Valid && strings.TrimSpace(rec.ProjectID.String) != "" {
			s.Metadata["zcode_project_id"] = rec.ProjectID.String
		}
		if rec.Slug.Valid && strings.TrimSpace(rec.Slug.String) != "" {
			s.Metadata["zcode_slug"] = rec.Slug.String
		}

		if s.Title == "" {
			if title, ok := firstUserMessageTitle(db, s.ID); ok {
				s.Title = title
				s.Metadata["title_source"] = "first_input"
			}
		}
		if s.Title == "" {
			delete(s.Metadata, "title_source")
		}

		if opts.Preview.Enabled() {
			s.Previews = userMessagePreviews(db, s.ID, opts.Preview)
		} else {
			s.Previews = nil
		}

		cwdChecker.Mark(&s)
		sessions = append(sessions, s)
	}
	return sessions, nil
}

// ResumeCommand emits the proposed `zcode --resume <session-id>` command from
// the original session cwd. ZCode is an Electron desktop app without a CLI or
// documented deep-link resume path today; asm treats zcode as discover-only.
// The emitted command is a future-compatible placeholder so the provider stays
// symmetric with codex/claude/kimi/opencode and grows into a real CLI if one
// ships. Running it will surface a clear "command not found" until then.
func (p Provider) ResumeCommand(s session.Session) session.ExecSpec {
	return session.ExecSpec{
		Dir:  s.CWD,
		Args: []string{"zcode", "--resume", s.ID},
	}
}

func (p Provider) NewCommand(string) session.ExecSpec {
	return session.ExecSpec{UnsupportedReason: "ZCode new session is not supported by asm yet"}
}

func (p Provider) home() (string, error) {
	if p.Home != "" {
		return p.Home, nil
	}
	if home := os.Getenv("ZCODE_HOME"); home != "" {
		return home, nil
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(userHome, ".zcode"), nil
}

func openDB(path string) (*sql.DB, error) {
	// mode=ro keeps discovery read-only so concurrent ZCode app writes are safe.
	dsn := "file:" + path + "?mode=ro"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("zcode open db: %w", err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("zcode ping db: %w", err)
	}
	return db, nil
}

const sessionSelect = `
SELECT id, directory, title, title_source, time_created, time_updated,
       path, slug, project_id, time_archived
FROM session
WHERE time_archived IS NULL
`

// sessionQuery builds the session SELECT with --since and --limit pushed into
// SQL so default discovery only reads rows in the active window. ZCode stores
// time_updated as a millisecond Unix epoch; sinceMillis is 0 when --since is
// unset, leaving the window unbounded.
func sessionQuery(opts session.DiscoverOptions) string {
	q := sessionSelect
	if !opts.Since.IsZero() {
		q += " AND time_updated >= ?"
	}
	q += " ORDER BY time_updated DESC"
	if opts.LimitFiles > 0 {
		q += " LIMIT ?"
	}
	return q
}

type sessionRecord struct {
	ID          string
	Directory   string
	Title       string
	TitleSource string
	TimeCreated int64
	TimeUpdated int64
	Path        sql.NullString
	Slug        sql.NullString
	ProjectID   sql.NullString
}

type messageRow struct {
	ID          string
	TimeCreated int64
	Role        string
}

type partRow struct {
	Type        string
	Text        string
	TimeCreated int64
}

// firstUserMessageTitle returns the earliest user-authored text part for a
// session, matching ZCode's own "first_input" title source semantics.
func firstUserMessageTitle(db *sql.DB, sessionID string) (string, bool) {
	preview, ok := earliestUserPreview(db, sessionID, 1)
	if !ok || len(preview) == 0 {
		return "", false
	}
	return preview[0].Text, true
}

func userMessagePreviews(db *sql.DB, sessionID string, opts session.PreviewOptions) []session.MessagePreview {
	previews, ok := earliestUserPreview(db, sessionID, 0)
	if !ok {
		return nil
	}
	return session.SelectMessagePreviews(previews, opts)
}

// earliestUserPreview collects user message text parts in creation order. When
// limit > 0 only that many rows are scanned before returning; pass 0 for all.
func earliestUserPreview(db *sql.DB, sessionID string, limit int) ([]session.MessagePreview, bool) {
	msgRows, err := db.Query(userMessagesQuery, sessionID)
	if err != nil {
		return nil, false
	}
	defer func() { _ = msgRows.Close() }()

	var messages []messageRow
	for msgRows.Next() {
		var msg messageRow
		if err := msgRows.Scan(&msg.ID, &msg.TimeCreated, &msg.Role); err != nil {
			return nil, false
		}
		if strings.TrimSpace(msg.Role) == "user" {
			messages = append(messages, msg)
		}
	}
	if err := msgRows.Err(); err != nil {
		return nil, false
	}
	if len(messages) == 0 {
		return nil, true
	}

	var previews []session.MessagePreview
	for _, msg := range messages {
		text, at, ok := firstTextPart(db, msg.ID, sessionID)
		if !ok {
			continue
		}
		title := cleanTitle(text)
		if title == "" {
			continue
		}
		messageTime := unixMillis(at)
		if messageTime.IsZero() {
			messageTime = unixMillis(msg.TimeCreated)
		}
		previews = append(previews, session.MessagePreview{
			Text:   title,
			At:     messageTime,
			Source: "zcode:message",
		})
		if limit > 0 && len(previews) >= limit {
			break
		}
	}
	return previews, true
}

// userMessagesQuery selects message id, creation time, and role for a session.
// role lives inside the JSON data column, extracted with json_extract.
const userMessagesQuery = `
SELECT id, time_created, json_extract(data, '$.role') AS role
FROM message
WHERE session_id = ?
ORDER BY time_created ASC
`

func firstTextPart(db *sql.DB, messageID, sessionID string) (text string, createdAt int64, ok bool) {
	row := db.QueryRow(textPartQuery, messageID, sessionID)
	var part partRow
	err := row.Scan(&part.Type, &part.Text, &part.TimeCreated)
	if err != nil {
		return "", 0, false
	}
	if strings.TrimSpace(part.Type) != "" && part.Type != "text" {
		return "", 0, false
	}
	return part.Text, part.TimeCreated, true
}

// textPartQuery picks the earliest text-type part for a user message. Tool and
// reasoning parts are skipped so the preview reflects what the user typed.
const textPartQuery = `
SELECT json_extract(data, '$.type') AS type,
       json_extract(data, '$.text') AS text,
       time_created
FROM part
WHERE message_id = ? AND session_id = ?
  AND (json_extract(data, '$.type') IS NULL
       OR json_extract(data, '$.type') = 'text')
ORDER BY time_created ASC
LIMIT 1
`

func cleanTitle(text string) string {
	text = strings.TrimSpace(text)
	if text == "" || isInjectedContext(text) {
		return ""
	}
	return strings.Join(strings.Fields(text), " ")
}

func isInjectedContext(text string) bool {
	prefixes := []string{
		"# AGENTS.md instructions",
		"# CLAUDE.md instructions",
		"<environment_context",
		"<system-reminder>",
		"<user_action",
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	return false
}

func unixMillis(value int64) time.Time {
	if value <= 0 {
		return time.Time{}
	}
	// ZCode stores millisecond Unix epochs.
	if value < 1_000_000_000_000 {
		return time.Unix(value, 0).UTC()
	}
	return time.UnixMilli(value).UTC()
}
