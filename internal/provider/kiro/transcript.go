package kiro

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
		return session.Transcript{}, fmt.Errorf("%w: invalid kiro session id %q", session.ErrSessionNotFound, id)
	}
	home, err := p.home()
	if err != nil {
		return session.Transcript{}, err
	}
	path := filepath.Join(home, "sessions", "cli", id+".json")
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return session.Transcript{}, fmt.Errorf("%w: kiro session %q", session.ErrSessionNotFound, id)
		}
		return session.Transcript{}, err
	}
	rec, err := readSession(path)
	if err != nil {
		return session.Transcript{}, err
	}
	if strings.TrimSpace(rec.ID) != id {
		return session.Transcript{}, fmt.Errorf("%w: kiro session %q", session.ErrSessionNotFound, id)
	}
	s := sessionFromRecord(fileInfo{Path: path, ModTime: info.ModTime()}, rec)
	transcriptPath := strings.TrimSuffix(path, ".json") + ".jsonl"
	f, err := os.Open(transcriptPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return session.Transcript{Session: s}, nil
		}
		return session.Transcript{}, err
	}
	defer func() { _ = f.Close() }()
	var messages []session.Message
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), maxJSONLRecordBytes)
	for scanner.Scan() {
		var raw struct {
			Kind string          `json:"kind"`
			Data json.RawMessage `json:"data"`
		}
		if json.Unmarshal(scanner.Bytes(), &raw) != nil {
			continue
		}
		role, text := "", ""
		switch raw.Kind {
		case "Prompt":
			var data promptData
			if json.Unmarshal(raw.Data, &data) == nil {
				text = promptText(messageRecord{Kind: raw.Kind, Data: data})
				role = "user"
				at := parseTimestamp(data.Meta.Timestamp)
				if text != "" {
					messages = append(messages, session.Message{Role: role, Text: strings.TrimSpace(text), At: at})
				}
			}
		case "AssistantMessage", "Assistant":
			text = genericKiroText(raw.Data)
			role = "assistant"
		}
		if role == "assistant" && strings.TrimSpace(text) != "" {
			messages = append(messages, session.Message{Role: role, Text: strings.TrimSpace(text), At: genericKiroTime(raw.Data)})
		}
	}
	if err := scanner.Err(); err != nil {
		return session.Transcript{}, err
	}
	if s.UpdatedAt.IsZero() {
		s.UpdatedAt = info.ModTime()
	}
	if s.CreatedAt.IsZero() {
		s.CreatedAt = s.UpdatedAt
	}
	return session.Transcript{Session: s, Messages: messages}, nil
}

func genericKiroText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
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
		if strings.TrimSpace(b.Data) != "" {
			parts = append(parts, b.Data)
		}
	}
	return strings.Join(parts, "\n")
}
func genericKiroTime(raw json.RawMessage) time.Time {
	var x struct {
		Timestamp json.RawMessage `json:"timestamp"`
		Time      json.RawMessage `json:"time"`
	}
	if json.Unmarshal(raw, &x) != nil {
		return time.Time{}
	}
	if t := parseTimestamp(x.Timestamp); !t.IsZero() {
		return t
	}
	return parseTimestamp(x.Time)
}
