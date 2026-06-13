package codex

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"session-manager/internal/session"
)

const Name = "codex"

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
	home := p.Home
	if home == "" {
		home = os.Getenv("CODEX_HOME")
	}
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		home = filepath.Join(userHome, ".codex")
	}

	sessionRoot := filepath.Join(home, "sessions")
	files, err := collectJSONL(sessionRoot, opts)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	history := readHistoryTitles(filepath.Join(home, "history.jsonl"))
	sessions := make([]session.Session, 0, len(files))
	for _, file := range files {
		s, err := parseSessionFile(file.Path)
		if err != nil || s.ID == "" || s.CWD == "" {
			continue
		}
		s.Provider = Name
		s.Path = file.Path
		s.UpdatedAt = file.ModTime
		if s.CreatedAt.IsZero() {
			s.CreatedAt = file.ModTime
		}
		if title := history[s.ID]; title != "" {
			s.Title = title
		}
		sessions = append(sessions, s)
	}
	return sessions, nil
}

func (p Provider) ResumeCommand(s session.Session) session.ExecSpec {
	return session.ExecSpec{
		Dir:  s.CWD,
		Args: []string{"codex", "resume", s.ID},
	}
}

type fileInfo struct {
	Path    string
	ModTime time.Time
}

func collectJSONL(root string, opts session.DiscoverOptions) ([]fileInfo, error) {
	var files []fileInfo
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if shouldSkipSessionDateDir(root, path, opts.Since) {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".jsonl" {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if !opts.Since.IsZero() && info.ModTime().Before(opts.Since) {
			return nil
		}
		files = append(files, fileInfo{Path: path, ModTime: info.ModTime()})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].ModTime.After(files[j].ModTime)
	})
	if opts.LimitFiles > 0 && len(files) > opts.LimitFiles {
		files = files[:opts.LimitFiles]
	}
	return files, nil
}

func shouldSkipSessionDateDir(root, path string, since time.Time) bool {
	if since.IsZero() || path == root {
		return false
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) != 3 {
		return false
	}
	day, err := time.Parse("2006/01/02", filepath.ToSlash(rel))
	if err != nil {
		return false
	}
	return day.Before(startOfDay(since))
}

func startOfDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

type rawRecord struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type sessionMeta struct {
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
	CWD       string `json:"cwd"`
}

type turnContext struct {
	CWD   string `json:"cwd"`
	Model string `json:"model"`
}

func parseSessionFile(path string) (session.Session, error) {
	f, err := os.Open(path)
	if err != nil {
		return session.Session{}, err
	}
	defer f.Close()
	return parseSession(f)
}

func parseSession(r io.Reader) (session.Session, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	var out session.Session
	out.Metadata = make(map[string]string)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var rec rawRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		switch rec.Type {
		case "session_meta":
			var meta sessionMeta
			if json.Unmarshal(rec.Payload, &meta) == nil {
				out.ID = meta.ID
				out.CWD = meta.CWD
				if t := parseTime(meta.Timestamp); !t.IsZero() {
					out.CreatedAt = t
				}
			}
		case "turn_context":
			var ctx turnContext
			if json.Unmarshal(rec.Payload, &ctx) == nil {
				if ctx.CWD != "" {
					out.CWD = ctx.CWD
				}
				if ctx.Model != "" {
					out.Metadata["model"] = ctx.Model
				}
			}
		}
	}
	return out, scanner.Err()
}

type historyRecord struct {
	SessionID string `json:"session_id"`
	Text      string `json:"text"`
}

func readHistoryTitles(path string) map[string]string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	titles := make(map[string]string)
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		var rec historyRecord
		if json.Unmarshal(scanner.Bytes(), &rec) != nil {
			continue
		}
		text := strings.TrimSpace(rec.Text)
		if rec.SessionID == "" || text == "" || strings.HasPrefix(text, "$") || strings.HasPrefix(text, "/") {
			continue
		}
		titles[rec.SessionID] = text
	}
	return titles
}

func parseTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, value)
	if err == nil {
		return t
	}
	return time.Time{}
}
