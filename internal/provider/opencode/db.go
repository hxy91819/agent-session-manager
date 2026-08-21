package opencode

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver, registered as "sqlite"

	"github.com/hxy91819/agent-session-manager/internal/cwdstatus"
	"github.com/hxy91819/agent-session-manager/internal/session"
)

// Modern opencode (v1.18+) stores sessions in a drizzle-managed SQLite
// database at <home>/opencode.db and no longer writes storage/session JSON
// files. The one-time migration imports legacy JSON sessions into the DB, so
// when the DB exists it is authoritative: scanning the legacy tree in
// addition would surface migrated sessions twice with stale JSON shadows.
//
// sessioncache: not required for this path - discovery reads a single SQLite
// database with indexed queries instead of per-session files. The legacy JSON
// path in opencode.go keeps using sessioncache.

func discoverFromDB(dbPath string, dbMtime time.Time, opts session.DiscoverOptions) ([]session.Session, error) {
	db, err := openDB(dbPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()

	// Push --since and --limit into SQL so default discovery only scans rows
	// in the active window. opencode stores time_updated as a millisecond Unix
	// epoch; --since-days 0 leaves Since zero and the query unbounded.
	query := dbSessionQuery(opts)
	var queryArgs []any
	if !opts.Since.IsZero() {
		queryArgs = append(queryArgs, opts.Since.UnixMilli())
	}
	if opts.LimitFiles > 0 {
		queryArgs = append(queryArgs, opts.LimitFiles)
	}
	rows, err := db.Query(query, queryArgs...)
	if err != nil {
		return nil, fmt.Errorf("opencode query sessions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	type pending struct {
		rec dbSessionRecord
		mod time.Time
	}
	var pendingSessions []pending
	for rows.Next() {
		var rec dbSessionRecord
		if err := rows.Scan(&rec.ID, &rec.Directory, &rec.Title, &rec.Version,
			&rec.ProjectID, &rec.ParentID, &rec.TimeCreated, &rec.TimeUpdated,
			&rec.Worktree); err != nil {
			return nil, fmt.Errorf("opencode scan session: %w", err)
		}
		if strings.TrimSpace(rec.ID) == "" {
			continue
		}
		updated := unixMillis(rec.TimeUpdated)
		if updated.IsZero() {
			updated = dbMtime
		}
		pendingSessions = append(pendingSessions, pending{rec: rec, mod: updated})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("opencode iterate sessions: %w", err)
	}
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
		if s.CWD == "" && rec.Worktree.Valid {
			s.CWD = strings.TrimSpace(rec.Worktree.String)
		}
		if strings.TrimSpace(rec.ProjectID) != "" {
			s.Metadata["project_id"] = strings.TrimSpace(rec.ProjectID)
		}
		if strings.TrimSpace(rec.Version) != "" {
			s.Metadata["version"] = strings.TrimSpace(rec.Version)
		}
		if rec.ParentID.Valid && strings.TrimSpace(rec.ParentID.String) != "" {
			// opencode persists subagent children explicitly; preserve the
			// relation so reports can deduplicate delegated work, mirroring the
			// zcode provider contract.
			s.Metadata[session.MetadataParentThreadID] = strings.TrimSpace(rec.ParentID.String)
		}

		placeholder := isPlaceholderTitle(s.Title)
		if s.Title != "" {
			s.Metadata["title_source"] = "session"
		}
		if s.Title == "" || placeholder {
			if title, ok := dbFirstUserMessageTitle(db, s.ID); ok {
				s.Title = title
				s.Metadata["title_source"] = "first_input"
			} else if s.Title == "" {
				delete(s.Metadata, "title_source")
			}
		}

		if opts.Preview.Enabled() {
			s.Previews = dbUserMessagePreviews(db, s.ID, opts.Preview)
		}

		cwdChecker.Mark(&s)
		s.Title = session.NormalizeTitle(s.Title)
		sessions = append(sessions, s)
	}
	return sessions, nil
}

func openDB(path string) (*sql.DB, error) {
	// mode=ro keeps discovery read-only so concurrent opencode writes are safe.
	dsn := "file:" + path + "?mode=ro"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opencode open db: %w", err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("opencode ping db: %w", err)
	}
	return db, nil
}

// dbSessionSelect joins project once so an empty session.directory can fall
// back to the project worktree without per-session extra queries. Archived
// sessions are excluded: they are hidden history, not resumable work.
const dbSessionSelect = `
SELECT s.id, s.directory, s.title, s.version, s.project_id, s.parent_id,
       s.time_created, s.time_updated, p.worktree
FROM session s
LEFT JOIN project p ON p.id = s.project_id
WHERE s.time_archived IS NULL
`

func dbSessionQuery(opts session.DiscoverOptions) string {
	q := dbSessionSelect
	if !opts.Since.IsZero() {
		q += " AND s.time_updated >= ?"
	}
	q += " ORDER BY s.time_updated DESC"
	if opts.LimitFiles > 0 {
		q += " LIMIT ?"
	}
	return q
}

type dbSessionRecord struct {
	ID          string
	Directory   string
	Title       string
	Version     string
	ProjectID   string
	ParentID    sql.NullString
	TimeCreated int64
	TimeUpdated int64
	Worktree    sql.NullString
}

// isPlaceholderTitle matches the auto label opencode assigns before its
// summarizer renames a session ("New session - 2026-08-20T11:52:02.819Z").
// Short sessions keep this label forever, so it is treated like an empty
// title for the first-user-message fallback.
func isPlaceholderTitle(title string) bool {
	return strings.HasPrefix(title, "New session - ")
}

func dbFirstUserMessageTitle(db *sql.DB, sessionID string) (string, bool) {
	previews := collectDBUserPreviews(db, sessionID, 1)
	if len(previews) == 0 {
		return "", false
	}
	return previews[0].Text, true
}

func dbUserMessagePreviews(db *sql.DB, sessionID string, opts session.PreviewOptions) []session.MessagePreview {
	return session.SelectMessagePreviews(collectDBUserPreviews(db, sessionID, 0), opts)
}

// collectDBUserPreviews returns user message texts in creation order using
// producer-persisted timestamps; limit > 0 stops after that many previews.
func collectDBUserPreviews(db *sql.DB, sessionID string, limit int) []session.MessagePreview {
	rows, err := db.Query(dbUserMessagesQuery, sessionID)
	if err != nil {
		return nil
	}
	type dbMessage struct {
		id          string
		timeCreated int64
	}
	var messages []dbMessage
	for rows.Next() {
		var msg dbMessage
		if err := rows.Scan(&msg.id, &msg.timeCreated); err != nil {
			_ = rows.Close()
			return nil
		}
		messages = append(messages, msg)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil
	}
	_ = rows.Close()

	var previews []session.MessagePreview
	for _, msg := range messages {
		text, ok := dbFirstTextPart(db, msg.id, sessionID)
		if !ok {
			continue
		}
		title := cleanTitle(text)
		if title == "" {
			continue
		}
		at := unixMillis(msg.timeCreated)
		if at.IsZero() {
			// Schema timestamps are NOT NULL, so a zero value means the row is
			// outside the expected epoch range and cannot evidence work time.
			continue
		}
		previews = append(previews, session.MessagePreview{
			Text:   title,
			At:     at,
			Source: "opencode:message",
		})
		if limit > 0 && len(previews) >= limit {
			break
		}
	}
	return previews
}

// dbUserMessagesQuery selects user message ids ordered by creation time.
// role lives inside the JSON data column, extracted with json_extract.
const dbUserMessagesQuery = `
SELECT id, time_created
FROM message
WHERE session_id = ? AND json_extract(data, '$.role') = 'user'
ORDER BY time_created ASC
`

func dbFirstTextPart(db *sql.DB, messageID, sessionID string) (string, bool) {
	row := db.QueryRow(dbTextPartQuery, messageID, sessionID)
	var text sql.NullString
	if err := row.Scan(&text); err != nil || !text.Valid {
		return "", false
	}
	return text.String, true
}

// dbTextPartQuery picks the earliest text-type part for a user message so
// tool and reasoning output never becomes the preview of a user prompt.
const dbTextPartQuery = `
SELECT json_extract(data, '$.text')
FROM part
WHERE message_id = ? AND session_id = ?
  AND json_extract(data, '$.type') = 'text'
ORDER BY time_created ASC
LIMIT 1
`
