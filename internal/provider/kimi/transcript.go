package kimi

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hxy91819/agent-session-manager/internal/session"
)

// ReadTranscript exposes the portion Kimi persists in state.json. Kimi Code's
// supported store does not retain a complete turn log, so this intentionally
// returns the latest prompt rather than fabricating earlier messages.
func (p Provider) ReadTranscript(id string) (session.Transcript, error) {
	id = strings.TrimSpace(id)
	if id == "" || strings.ContainsAny(id, "/\\") || id == "." || id == ".." {
		return session.Transcript{}, fmt.Errorf("%w: invalid kimi session id %q", session.ErrSessionNotFound, id)
	}
	home, err := p.home()
	if err != nil {
		return session.Transcript{}, err
	}
	entries, err := readSessionIndex(filepath.Join(home, "session_index.jsonl"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return session.Transcript{}, fmt.Errorf("%w: kimi session %q", session.ErrSessionNotFound, id)
		}
		return session.Transcript{}, err
	}
	for _, entry := range entries {
		if entry.SessionID != id {
			continue
		}
		sessionDir := entry.SessionDir
		if !filepath.IsAbs(sessionDir) {
			sessionDir = filepath.Join(home, sessionDir)
		}
		statePath := filepath.Join(sessionDir, "state.json")
		state, err := readState(statePath)
		if err != nil {
			return session.Transcript{}, err
		}
		info, err := os.Stat(statePath)
		if err != nil {
			return session.Transcript{}, err
		}
		s := session.Session{ID: id, Provider: Name, CWD: entry.WorkDir, Title: titleFromState(state), CreatedAt: parseTime(state.CreatedAt), UpdatedAt: info.ModTime(), Path: statePath, Metadata: map[string]string{"session_dir": sessionDir, session.MetadataReportEvidenceStatus: session.ReportEvidencePartial, session.MetadataReportEvidenceNote: "Kimi state exposes only the latest prompt; earlier turns are unavailable"}}
		if s.CreatedAt.IsZero() {
			s.CreatedAt = s.UpdatedAt
		}
		if s.Title != "" {
			s.Metadata["title_source"] = "title_or_last_prompt"
		}
		if text := cleanTitle(state.LastPrompt); text != "" {
			at := parseTime(state.UpdatedAt)
			if at.IsZero() {
				at = s.UpdatedAt
			}
			return session.Transcript{Session: s, Messages: []session.Message{{Role: "user", Text: text, At: at}}}, nil
		}
		return session.Transcript{Session: s}, nil
	}
	return session.Transcript{}, fmt.Errorf("%w: kimi session %q", session.ErrSessionNotFound, id)
}
