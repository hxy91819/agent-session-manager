package pi

import (
	"bufio"
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
		return session.Transcript{}, fmt.Errorf("%w: invalid pi session id %q", session.ErrSessionNotFound, id)
	}
	home, err := p.home()
	if err != nil {
		return session.Transcript{}, err
	}
	files, err := collectSessionFiles(filepath.Join(home, "sessions"), session.DiscoverOptions{})
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return session.Transcript{}, fmt.Errorf("%w: pi session %q", session.ErrSessionNotFound, id)
		}
		return session.Transcript{}, err
	}
	for _, file := range files {
		tr, _, e := readTranscript(file.Path, nil)
		if e != nil || tr.header.ID != id {
			continue
		}
		s := sessionFromTranscript(file, tr)
		msgs, e := readPiMessages(file.Path)
		if e != nil {
			return session.Transcript{}, e
		}
		return session.Transcript{Session: s, Messages: msgs}, nil
	}
	return session.Transcript{}, fmt.Errorf("%w: pi session %q", session.ErrSessionNotFound, id)
}
func readPiMessages(path string) ([]session.Message, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), maxJSONLRecordBytes)
	if !sc.Scan() {
		return nil, sc.Err()
	}
	var out []session.Message
	for sc.Scan() {
		var rec transcriptRecord
		if json.Unmarshal(sc.Bytes(), &rec) != nil || rec.Type != "message" {
			continue
		}
		if rec.Message.Role != "user" && rec.Message.Role != "assistant" {
			continue
		}
		text := strings.TrimSpace(extractText(rec.Message.Content))
		if text == "" || isInjectedContext(text) {
			continue
		}
		out = append(out, session.Message{Role: rec.Message.Role, Text: text, At: messageTimestamp(rec)})
	}
	return out, sc.Err()
}
