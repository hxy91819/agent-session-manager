package kimi

import (
	"bufio"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hxy91819/agent-session-manager/internal/cwdstatus"
	"github.com/hxy91819/agent-session-manager/internal/session"
)

const Name = "kimi"

// sessioncache: not required - Kimi discovery reads a compact index plus small per-session state.json files.
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
	home, err := p.home()
	if err != nil {
		return nil, err
	}

	entries, err := readSessionIndex(filepath.Join(home, "session_index.jsonl"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	files := make([]fileInfo, 0, len(entries))
	for _, entry := range entries {
		statePath := filepath.Join(entry.SessionDir, "state.json")
		info, err := os.Stat(statePath)
		if err != nil || info.IsDir() {
			continue
		}
		if !opts.Since.IsZero() && info.ModTime().Before(opts.Since) {
			continue
		}
		files = append(files, fileInfo{Entry: entry, StatePath: statePath, ModTime: info.ModTime()})
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].ModTime.After(files[j].ModTime)
	})
	if opts.LimitFiles > 0 && len(files) > opts.LimitFiles {
		files = files[:opts.LimitFiles]
	}

	cwdChecker := cwdstatus.NewChecker()
	sessions := make([]session.Session, 0, len(files))
	for _, file := range files {
		state, err := readState(file.StatePath)
		if err != nil || file.Entry.SessionID == "" || file.Entry.WorkDir == "" {
			continue
		}
		s := session.Session{
			ID:        file.Entry.SessionID,
			Provider:  Name,
			CWD:       file.Entry.WorkDir,
			Title:     titleFromState(state),
			CreatedAt: parseTime(state.CreatedAt),
			UpdatedAt: file.ModTime,
			Path:      file.StatePath,
			Metadata:  map[string]string{"session_dir": file.Entry.SessionDir},
		}
		if s.CreatedAt.IsZero() {
			s.CreatedAt = file.ModTime
		}
		if s.Title != "" {
			if strings.TrimSpace(state.Title) != "" {
				s.Metadata["title_source"] = "title"
			} else {
				s.Metadata["title_source"] = "last_prompt"
			}
		}
		if updated := parseTime(state.UpdatedAt); !updated.IsZero() {
			s.Metadata["kimi_updated_at"] = updated.Format(time.RFC3339Nano)
		}
		cwdChecker.Mark(&s)
		sessions = append(sessions, s)
	}
	return sessions, nil
}

func (p Provider) ResumeCommand(s session.Session) session.ExecSpec {
	return session.ExecSpec{
		Dir:  s.CWD,
		Args: []string{"kimi", "--session", s.ID},
	}
}

func (p Provider) home() (string, error) {
	if p.Home != "" {
		return p.Home, nil
	}
	if home := os.Getenv("KIMI_CODE_HOME"); home != "" {
		return home, nil
	}
	if home := os.Getenv("KIMI_HOME"); home != "" {
		return home, nil
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(userHome, ".kimi-code"), nil
}

type indexRecord struct {
	SessionID  string `json:"sessionId"`
	SessionDir string `json:"sessionDir"`
	WorkDir    string `json:"workDir"`
}

type stateRecord struct {
	CreatedAt  string `json:"createdAt"`
	UpdatedAt  string `json:"updatedAt"`
	Title      string `json:"title"`
	LastPrompt string `json:"lastPrompt"`
}

type fileInfo struct {
	Entry     indexRecord
	StatePath string
	ModTime   time.Time
}

func readSessionIndex(path string) ([]indexRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []indexRecord
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		var rec indexRecord
		if json.Unmarshal(scanner.Bytes(), &rec) != nil {
			continue
		}
		rec.SessionID = strings.TrimSpace(rec.SessionID)
		rec.SessionDir = strings.TrimSpace(rec.SessionDir)
		rec.WorkDir = strings.TrimSpace(rec.WorkDir)
		if rec.SessionID == "" || rec.SessionDir == "" || rec.WorkDir == "" {
			continue
		}
		out = append(out, rec)
	}
	return out, scanner.Err()
}

func readState(path string) (stateRecord, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return stateRecord{}, err
	}
	var state stateRecord
	if err := json.Unmarshal(data, &state); err != nil {
		return stateRecord{}, err
	}
	return state, nil
}

func titleFromState(state stateRecord) string {
	if title := cleanTitle(state.Title); title != "" {
		return title
	}
	return cleanTitle(state.LastPrompt)
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
		"# AGENTS.md instructions",
		"# CLAUDE.md instructions",
		"<environment_context",
		"<system-reminder>",
		"<user_action",
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	return false
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
