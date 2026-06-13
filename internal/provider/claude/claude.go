package claude

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

	"github.com/hxy91819/agent-session-manager/internal/session"
)

const Name = "claude"

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
		home = os.Getenv("CLAUDE_HOME")
	}
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		home = filepath.Join(userHome, ".claude")
	}

	projectRoot := filepath.Join(home, "projects")
	files, err := collectJSONL(projectRoot, opts)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

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
		markCWDStatus(&s)
		sessions = append(sessions, s)
	}
	return sessions, nil
}

func (p Provider) ResumeCommand(s session.Session) session.ExecSpec {
	return session.ExecSpec{
		Dir:  s.CWD,
		Args: []string{"claude", "--resume", s.ID},
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

type rawRecord struct {
	Type      string          `json:"type"`
	SessionID string          `json:"sessionId"`
	CWD       string          `json:"cwd"`
	Timestamp string          `json:"timestamp"`
	Summary   string          `json:"summary"`
	Title     string          `json:"title"`
	GitBranch string          `json:"gitBranch"`
	IsMeta    bool            `json:"isMeta"`
	Message   json.RawMessage `json:"message"`
}

type claudeMessage struct {
	Role    string          `json:"role"`
	Model   string          `json:"model"`
	Content json.RawMessage `json:"content"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
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

	out := session.Session{Metadata: make(map[string]string)}
	var lastUserTitle string

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var rec rawRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		if rec.SessionID != "" {
			out.ID = rec.SessionID
		}
		if rec.CWD != "" {
			out.CWD = rec.CWD
		}
		if rec.GitBranch != "" {
			out.Metadata["git_branch"] = rec.GitBranch
		}
		if t := parseTime(rec.Timestamp); !t.IsZero() {
			if out.CreatedAt.IsZero() || t.Before(out.CreatedAt) {
				out.CreatedAt = t
			}
			if t.After(out.UpdatedAt) {
				out.UpdatedAt = t
			}
		}

		if title := cleanTitle(firstNonEmpty(rec.Summary, rec.Title)); title != "" {
			out.Title = title
			out.Metadata["title_source"] = rec.Type
			continue
		}

		msg := parseMessage(rec.Message)
		if msg.Model != "" {
			out.Metadata["model"] = msg.Model
		}
		if rec.Type == "user" && !rec.IsMeta && msg.Role == "user" {
			if title := cleanTitle(messageText(msg.Content)); title != "" {
				lastUserTitle = title
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return session.Session{}, err
	}
	if out.Title == "" && lastUserTitle != "" {
		out.Title = lastUserTitle
		out.Metadata["title_source"] = "user"
	}
	return out, nil
}

func parseMessage(raw json.RawMessage) claudeMessage {
	if len(raw) == 0 {
		return claudeMessage{}
	}
	var msg claudeMessage
	if json.Unmarshal(raw, &msg) != nil {
		return claudeMessage{}
	}
	return msg
}

func messageText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var blocks []contentBlock
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, block := range blocks {
		if block.Type != "" && block.Type != "text" {
			continue
		}
		if strings.TrimSpace(block.Text) != "" {
			parts = append(parts, block.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func cleanTitle(text string) string {
	text = strings.TrimSpace(text)
	if text == "" || isInjectedContext(text) {
		return ""
	}
	return strings.Join(strings.Fields(text), " ")
}

func isInjectedContext(text string) bool {
	prefixes := []string{
		"# CLAUDE.md instructions",
		"# AGENTS.md instructions",
		"<environment_context",
		"<system-reminder>",
		"<command-name>",
		"<local-command-stdout>",
		"<user_action",
		"The following is the Claude agent history",
		"The following is the Codex agent history",
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func markCWDStatus(s *session.Session) {
	info, err := os.Stat(s.CWD)
	if err == nil && info.IsDir() {
		return
	}
	if errors.Is(err, fs.ErrNotExist) || err == nil {
		s.Metadata["cwd_missing"] = "true"
		return
	}
	s.Metadata["cwd_error"] = err.Error()
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
