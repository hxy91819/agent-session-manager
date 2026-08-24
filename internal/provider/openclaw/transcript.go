package openclaw

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hxy91819/agent-session-manager/internal/session"
)

func (p Provider) ReadTranscript(id string) (session.Transcript, error) {
	id = strings.TrimSpace(id)
	if id == "" || strings.ContainsAny(id, "/\\") || id == "." || id == ".." {
		return session.Transcript{}, fmt.Errorf("%w: invalid openclaw session id %q", session.ErrSessionNotFound, id)
	}
	state, err := p.stateDir()
	if err != nil {
		return session.Transcript{}, err
	}
	files, err := collectSessionIndexes(state, session.DiscoverOptions{})
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return session.Transcript{}, fmt.Errorf("%w: openclaw session %q", session.ErrSessionNotFound, id)
		}
		return session.Transcript{}, err
	}
	for _, file := range files {
		records, e := readSessions(file.Path)
		if e != nil {
			return session.Transcript{}, e
		}
		for key, rec := range records {
			if key != id {
				continue
			}
			s := sessionFromRecord(state, file, key, rec)
			path := strings.TrimSpace(rec.SessionFile)
			if path == "" && rec.SessionID != "" {
				path = rec.SessionID + ".jsonl"
			}
			if path == "" {
				return session.Transcript{Session: s}, nil
			}
			if !filepath.IsAbs(path) {
				path = filepath.Join(state, path)
			}
			msgs, e := readOpenClawMessages(path)
			if e != nil && errors.Is(e, os.ErrNotExist) {
				// Some OpenClaw versions store the relative sessionFile next to
				// sessions.json rather than relative to the state root.
				path = filepath.Join(filepath.Dir(file.Path), filepath.Base(path))
				msgs, e = readOpenClawMessages(path)
			}
			if e != nil {
				if errors.Is(e, os.ErrNotExist) {
					return session.Transcript{Session: s}, nil
				}
				return session.Transcript{}, e
			}
			s.Path = path
			delete(s.Metadata, session.MetadataReportEvidenceStatus)
			delete(s.Metadata, session.MetadataReportEvidenceNote)
			return session.Transcript{Session: s, Messages: msgs}, nil
		}
	}
	return session.Transcript{}, fmt.Errorf("%w: openclaw session %q", session.ErrSessionNotFound, id)
}
func readOpenClawMessages(path string) ([]session.Message, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 8*1024*1024)
	var out []session.Message
	for sc.Scan() {
		var rec struct {
			Role      string          `json:"role"`
			Content   json.RawMessage `json:"content"`
			Message   json.RawMessage `json:"message"`
			Timestamp json.RawMessage `json:"timestamp"`
			Time      json.RawMessage `json:"time"`
		}
		if json.Unmarshal(sc.Bytes(), &rec) != nil {
			continue
		}
		role := rec.Role
		content := rec.Content
		if len(rec.Message) > 0 {
			var nested struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			}
			if json.Unmarshal(rec.Message, &nested) == nil {
				if nested.Role != "" {
					role = nested.Role
				}
				if len(nested.Content) > 0 {
					content = nested.Content
				}
			}
		}
		if role != "user" && role != "assistant" {
			continue
		}
		text := openClawText(content)
		if strings.TrimSpace(text) == "" {
			continue
		}
		at := openClawTime(rec.Timestamp)
		if at.IsZero() {
			at = openClawTime(rec.Time)
		}
		out = append(out, session.Message{Role: role, Text: strings.TrimSpace(text), At: at})
	}
	return out, sc.Err()
}
func openClawText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []struct {
		Type    string `json:"type"`
		Text    string `json:"text"`
		Content string `json:"content"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Text != "" {
			parts = append(parts, b.Text)
		} else if b.Content != "" {
			parts = append(parts, b.Content)
		}
	}
	return strings.Join(parts, "\n")
}
func openClawTime(raw json.RawMessage) time.Time {
	var n int64
	if json.Unmarshal(raw, &n) == nil && n > 0 {
		if n < 100000000000 {
			n *= 1000
		}
		return time.UnixMilli(n)
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if t, e := time.Parse(time.RFC3339Nano, s); e == nil {
			return t
		}
	}
	return time.Time{}
}
