package codex

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hxy91819/agent-session-manager/internal/session"
)

func (p Provider) ReadTranscript(id string) (session.Transcript, error) {
	id = strings.TrimSpace(id)
	if id == "" || strings.ContainsAny(id, "/\\") || id == "." || id == ".." {
		return session.Transcript{}, fmt.Errorf("%w: invalid codex session id %q", session.ErrSessionNotFound, id)
	}
	homes, e := p.homes()
	if e != nil {
		return session.Transcript{}, e
	}
	var best session.Transcript
	var bestMod int64
	for _, home := range homes {
		e = filepath.WalkDir(filepath.Join(home, "sessions"), func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil || d.IsDir() || filepath.Ext(path) != ".jsonl" {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			tr, ok, err := readCodexTranscriptFile(path, id, info.ModTime())
			if err != nil {
				return err
			}
			if ok && (best.Session.ID == "" || info.ModTime().UnixNano() > bestMod) {
				best = tr
				bestMod = info.ModTime().UnixNano()
			}
			return nil
		})
		if e != nil {
			return session.Transcript{}, e
		}
	}
	if best.Session.ID == "" {
		return session.Transcript{}, fmt.Errorf("%w: codex session %q", session.ErrSessionNotFound, id)
	}
	return best, nil
}
func readCodexTranscriptFile(path, id string, modTime time.Time) (session.Transcript, bool, error) {
	base, _, err := func() (session.Session, bool, error) {
		f, e := os.Open(path)
		if e != nil {
			return session.Session{}, false, e
		}
		defer func() { _ = f.Close() }()
		s, st, e := parseSessionInto(f, session.Session{})
		if e != nil {
			return session.Session{}, false, e
		}
		return s, st, nil
	}()
	if err != nil {
		return session.Transcript{}, false, err
	}
	if base.ID != id {
		return session.Transcript{}, false, nil
	}
	base.Provider = Name
	base.Path = path
	base.UpdatedAt = modTime
	if base.CreatedAt.IsZero() {
		base.CreatedAt = modTime
	}
	var msgs []session.Message
	f, e := os.Open(path)
	if e != nil {
		return session.Transcript{}, false, e
	}
	defer func() { _ = f.Close() }()
	stopped := false
	parent := base.Metadata[session.MetadataParentThreadID]
	_, e = readCodexRecords(f, func(line []byte) bool {
		if stopped {
			return false
		}
		var rec rawRecord
		if json.Unmarshal(line, &rec) != nil {
			return true
		}
		if rec.Type == "session_meta" && parent != "" {
			var meta sessionMeta
			if json.Unmarshal(rec.Payload, &meta) == nil && meta.ID == parent {
				stopped = true
				return false
			}
		}
		if rec.Type != "response_item" {
			return true
		}
		var msg responseMessage
		if json.Unmarshal(rec.Payload, &msg) != nil || msg.Type != "message" || (msg.Role != "user" && msg.Role != "assistant") {
			return true
		}
		text := codexTranscriptText(msg.Content, msg.Role)
		if text != "" {
			msgs = append(msgs, session.Message{Role: msg.Role, Text: text, At: parseTime(rec.Timestamp)})
		}
		return true
	})
	if e != nil {
		return session.Transcript{}, false, e
	}
	return session.Transcript{Session: base, Messages: msgs}, true, nil
}
func codexTranscriptText(content []messageContent, role string) string {
	var parts []string
	for _, item := range content {
		if item.Type != "" && item.Type != "input_text" && item.Type != "output_text" && item.Type != "text" {
			continue
		}
		text := item.Text
		if text == "" {
			text = item.InputText
		}
		if strings.TrimSpace(text) != "" {
			parts = append(parts, text)
		}
	}
	text := strings.TrimSpace(strings.Join(parts, "\n"))
	if role == "user" {
		if request, wrapped := extractWrappedUserRequest(text); wrapped {
			text = request
		}
		if text == "" || isInjectedUserContext(text) {
			return ""
		}
	}
	return text
}
