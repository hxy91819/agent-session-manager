package opencode

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hxy91819/agent-session-manager/internal/session"
)

func (p Provider) ReadTranscript(id string) (session.Transcript, error) {
	id = strings.TrimSpace(id)
	if id == "" || strings.ContainsAny(id, "/\\") || id == "." || id == ".." {
		return session.Transcript{}, fmt.Errorf("%w: invalid opencode session id %q", session.ErrSessionNotFound, id)
	}
	home, err := p.home()
	if err != nil {
		return session.Transcript{}, err
	}
	dbPath := filepath.Join(home, "opencode.db")
	if info, e := os.Stat(dbPath); e == nil && !info.IsDir() {
		return readOpencodeDBTranscript(dbPath, id)
	}
	storage := filepath.Join(home, "storage")
	files, e := collectSessionJSON(filepath.Join(storage, "session"), session.DiscoverOptions{})
	if e != nil {
		if errors.Is(e, os.ErrNotExist) {
			return session.Transcript{}, fmt.Errorf("%w: opencode session %q", session.ErrSessionNotFound, id)
		}
		return session.Transcript{}, e
	}
	for _, file := range files {
		rec, e := readSession(file.Path)
		if e != nil || rec.ID != id {
			continue
		}
		s := sessionFromRecord(file, rec)
		if s.CWD == "" && s.Metadata["project_id"] != "" {
			if project, e := readProject(filepath.Join(storage, "project", s.Metadata["project_id"]+".json")); e == nil {
				s.CWD = project.Worktree
			}
		}
		msgs, e := readLegacyOpencodeMessages(storage, id)
		if e != nil {
			return session.Transcript{}, e
		}
		return session.Transcript{Session: s, Messages: msgs}, nil
	}
	return session.Transcript{}, fmt.Errorf("%w: opencode session %q", session.ErrSessionNotFound, id)
}
func readLegacyOpencodeMessages(storage, id string) ([]session.Message, error) {
	files, e := collectMessageJSON(filepath.Join(storage, "message", id))
	if e != nil {
		return nil, e
	}
	var out []session.Message
	for _, file := range files {
		rec, e := readMessage(file.Path)
		if e != nil || (rec.Role != "user" && rec.Role != "assistant") {
			continue
		}
		text := titleFromMessageParts(filepath.Join(storage, "part", rec.ID))
		if text != "" {
			at := unixMillis(rec.Time.Created)
			if at.IsZero() {
				at = unixMillis(rec.Time.Updated)
			}
			out = append(out, session.Message{Role: rec.Role, Text: text, At: at})
		}
	}
	return out, nil
}
func readOpencodeDBTranscript(path, id string) (session.Transcript, error) {
	db, e := openDB(path)
	if e != nil {
		return session.Transcript{}, e
	}
	defer func() { _ = db.Close() }()
	row := db.QueryRow(dbSessionSelect+" AND s.id = ?", id)
	var rec dbSessionRecord
	if e = row.Scan(&rec.ID, &rec.Directory, &rec.Title, &rec.Version, &rec.ProjectID, &rec.ParentID, &rec.TimeCreated, &rec.TimeUpdated, &rec.Worktree); e != nil {
		if errors.Is(e, sql.ErrNoRows) {
			return session.Transcript{}, fmt.Errorf("%w: opencode session %q", session.ErrSessionNotFound, id)
		}
		return session.Transcript{}, e
	}
	s := session.Session{ID: rec.ID, Provider: Name, CWD: strings.TrimSpace(rec.Directory), Title: cleanTitle(rec.Title), CreatedAt: unixMillis(rec.TimeCreated), UpdatedAt: unixMillis(rec.TimeUpdated), Path: path, Metadata: map[string]string{}}
	if s.CWD == "" && rec.Worktree.Valid {
		s.CWD = rec.Worktree.String
	}
	if rec.ProjectID != "" {
		s.Metadata["project_id"] = rec.ProjectID
	}
	if rec.Version != "" {
		s.Metadata["version"] = rec.Version
	}
	if rec.ParentID.Valid && rec.ParentID.String != "" {
		s.Metadata[session.MetadataParentThreadID] = rec.ParentID.String
	}
	if s.Title != "" {
		s.Metadata["title_source"] = "session"
	}
	if s.Title == "" || isPlaceholderTitle(s.Title) {
		if title, ok := dbFirstUserMessageTitle(db, id); ok {
			s.Title = title
			s.Metadata["title_source"] = "first_input"
		}
	}
	rows, e := db.Query(`SELECT id, json_extract(data, '$.role'), time_created FROM message WHERE session_id = ? ORDER BY time_created ASC`, id)
	if e != nil {
		return session.Transcript{}, e
	}
	defer func() { _ = rows.Close() }()
	var out []session.Message
	for rows.Next() {
		var mid, role string
		var at int64
		if e := rows.Scan(&mid, &role, &at); e != nil {
			return session.Transcript{}, e
		}
		if role != "user" && role != "assistant" {
			continue
		}
		text, ok := dbFirstTextPart(db, mid, id)
		if ok && strings.TrimSpace(text) != "" {
			out = append(out, session.Message{Role: role, Text: cleanTitle(text), At: unixMillis(at)})
		}
	}
	if e = rows.Err(); e != nil {
		return session.Transcript{}, e
	}
	if s.CreatedAt.IsZero() {
		s.CreatedAt = s.UpdatedAt
	}
	if s.UpdatedAt.IsZero() {
		s.UpdatedAt = s.CreatedAt
	}
	return session.Transcript{Session: s, Messages: out}, nil
}
