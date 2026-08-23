package dsh

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
		return session.Transcript{}, fmt.Errorf("%w: invalid dsh session id %q", session.ErrSessionNotFound, id)
	}
	home, err := p.home()
	if err != nil {
		return session.Transcript{}, err
	}
	files, err := collectLogFiles(filepath.Join(home, "sessions"), session.DiscoverOptions{})
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return session.Transcript{}, fmt.Errorf("%w: dsh session %q", session.ErrSessionNotFound, id)
		}
		return session.Transcript{}, err
	}
	for _, file := range files {
		parsed, e := parseLog(file.Path)
		if e != nil || parsed.header.ID != id {
			continue
		}
		s := parsed.session(file)
		lines, e := readLogLines(file.Path)
		if e != nil {
			return session.Transcript{}, e
		}
		var msgs []session.Message
		for _, line := range lines[1:] {
			var ev eventEnvelope
			if json.Unmarshal(line, &ev) != nil {
				continue
			}
			var text string
			role := ""
			switch ev.Type {
			case "user/message":
				if v, ok := humanMessageText(ev.Data); ok {
					text = v
					role = "user"
				}
			case "assistant/message":
				text = genericDshText(ev.Data)
				role = "assistant"
			}
			if strings.TrimSpace(text) != "" {
				msgs = append(msgs, session.Message{Role: role, Text: strings.TrimSpace(text), At: unixMillis(ev.Time)})
			}
		}
		return session.Transcript{Session: s, Messages: msgs}, nil
	}
	return session.Transcript{}, fmt.Errorf("%w: dsh session %q", session.ErrSessionNotFound, id)
}

func genericDshText(raw json.RawMessage) string {
	var obj struct {
		Content []contentBlock `json:"content"`
		Message string         `json:"message"`
		Text    string         `json:"text"`
	}
	if json.Unmarshal(raw, &obj) != nil {
		return ""
	}
	if obj.Message != "" {
		return obj.Message
	}
	if obj.Text != "" {
		return obj.Text
	}
	var parts []string
	for _, b := range obj.Content {
		if strings.TrimSpace(b.Text) != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}
