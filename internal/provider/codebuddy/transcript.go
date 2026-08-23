package codebuddy

import (
	"encoding/json"
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
		return session.Transcript{}, fmt.Errorf("%w: invalid codebuddy session id %q", session.ErrSessionNotFound, id)
	}
	home, err := p.home()
	if err != nil {
		return session.Transcript{}, err
	}
	files, err := collectJSONL(filepath.Join(home, "projects"), session.DiscoverOptions{})
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return session.Transcript{}, fmt.Errorf("%w: codebuddy session %q", session.ErrSessionNotFound, id)
		}
		return session.Transcript{}, err
	}
	for _, file := range files {
		s, e := parseSessionFile(file.Path)
		if e != nil || s.ID != id {
			continue
		}
		s.Provider = Name
		s.Path = file.Path
		s.UpdatedAt = file.ModTime
		if s.CreatedAt.IsZero() {
			s.CreatedAt = s.UpdatedAt
		}
		msgs, e := readCodeBuddyMessages(file.Path)
		if e != nil {
			return session.Transcript{}, e
		}
		return session.Transcript{Session: s, Messages: msgs}, nil
	}
	return session.Transcript{}, fmt.Errorf("%w: codebuddy session %q", session.ErrSessionNotFound, id)
}
func readCodeBuddyMessages(path string) ([]session.Message, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer func() { _ = f.Close() }()
	var out []session.Message
	_, e = readCodeBuddyRecords(f, func(line []byte) bool {
		var rec rawRecord
		if json.Unmarshal(line, &rec) != nil {
			return true
		}
		msg := parseMessage(rec)
		role := messageRole(rec, msg)
		if role != "user" && role != "assistant" {
			return true
		}
		text := strings.TrimSpace(messageText(msg.Content))
		if text != "" {
			out = append(out, session.Message{Role: role, Text: text, At: rec.Timestamp.Time()})
		}
		return true
	})
	return out, e
}
