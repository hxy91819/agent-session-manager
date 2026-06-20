package openclaw

import (
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

const Name = "openclaw"

// sessioncache: not required - OpenClaw discovery reads compact per-agent sessions.json indexes.
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
	stateDir, err := p.stateDir()
	if err != nil {
		return nil, err
	}
	files, err := collectSessionIndexes(stateDir, opts)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	cwdChecker := cwdstatus.NewChecker()
	var sessions []session.Session
	for _, file := range files {
		records, err := readSessions(file.Path)
		if err != nil {
			continue
		}
		for key, rec := range records {
			s := sessionFromRecord(stateDir, file, key, rec)
			if s.ID == "" {
				continue
			}
			if !opts.Since.IsZero() && s.UpdatedAt.Before(opts.Since) {
				continue
			}
			if s.Metadata["cwd_missing"] != "true" {
				cwdChecker.Mark(&s)
			}
			sessions = append(sessions, s)
		}
	}
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})
	return sessions, nil
}

func (p Provider) ResumeCommand(session.Session) session.ExecSpec {
	return session.ExecSpec{UnsupportedReason: "OpenClaw resume is not supported by asm yet"}
}

func (p Provider) NewCommand(string) session.ExecSpec {
	return session.ExecSpec{UnsupportedReason: "OpenClaw new session is not supported by asm yet"}
}

func (p Provider) stateDir() (string, error) {
	if p.Home != "" {
		return p.Home, nil
	}
	if stateDir := os.Getenv("OPENCLAW_STATE_DIR"); stateDir != "" {
		return stateDir, nil
	}
	if home := os.Getenv("OPENCLAW_HOME"); home != "" {
		return filepath.Join(home, ".openclaw"), nil
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(userHome, ".openclaw"), nil
}

type fileInfo struct {
	Path    string
	AgentID string
	ModTime time.Time
}

func collectSessionIndexes(stateDir string, opts session.DiscoverOptions) ([]fileInfo, error) {
	root := filepath.Join(stateDir, "agents")
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var files []fileInfo
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(root, entry.Name(), "sessions", "sessions.json")
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		if !opts.Since.IsZero() && info.ModTime().Before(opts.Since) {
			continue
		}
		files = append(files, fileInfo{Path: path, AgentID: entry.Name(), ModTime: info.ModTime()})
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].ModTime.After(files[j].ModTime)
	})
	if opts.LimitFiles > 0 && len(files) > opts.LimitFiles {
		files = files[:opts.LimitFiles]
	}
	return files, nil
}

type rawSession struct {
	SessionID           string          `json:"sessionId"`
	UpdatedAt           int64           `json:"updatedAt"`
	CreatedAt           int64           `json:"createdAt"`
	SpawnedCWD          string          `json:"spawnedCwd"`
	SpawnedWorkspaceDir string          `json:"spawnedWorkspaceDir"`
	SessionFile         string          `json:"sessionFile"`
	Kind                string          `json:"kind"`
	ChatType            string          `json:"chatType"`
	Channel             string          `json:"channel"`
	DisplayName         string          `json:"displayName"`
	Subject             string          `json:"subject"`
	Origin              json.RawMessage `json:"origin"`
}

type originRecord struct {
	Label    string `json:"label"`
	Provider string `json:"provider"`
	Surface  string `json:"surface"`
	ChatType string `json:"chatType"`
}

func readSessions(path string) (map[string]rawSession, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var records map[string]rawSession
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, err
	}
	return records, nil
}

func sessionFromRecord(stateDir string, file fileInfo, key string, rec rawSession) session.Session {
	metadata := map[string]string{
		"agent_id":           file.AgentID,
		"session_file":       firstNonEmpty(rec.SessionFile, file.Path),
		"source_home":        stateDir,
		"resume_unsupported": "OpenClaw resume is not supported by asm yet",
	}
	if rec.SessionID != "" {
		metadata["native_session_id"] = rec.SessionID
	}
	if kind := firstNonEmpty(rec.Kind, rec.ChatType, rec.Channel); kind != "" {
		metadata["kind"] = kind
	}
	if rec.Channel != "" {
		metadata["channel"] = rec.Channel
	}
	if rec.SpawnedWorkspaceDir != "" {
		metadata["spawned_workspace_dir"] = rec.SpawnedWorkspaceDir
	}

	cwd := strings.TrimSpace(rec.SpawnedCWD)
	if cwd == "" {
		metadata["cwd_missing"] = "true"
	}

	updatedAt := unixMillis(rec.UpdatedAt)
	if updatedAt.IsZero() {
		updatedAt = file.ModTime
	}
	createdAt := unixMillis(rec.CreatedAt)
	if createdAt.IsZero() {
		createdAt = updatedAt
	}
	return session.Session{
		ID:        strings.TrimSpace(key),
		Provider:  Name,
		CWD:       cwd,
		Title:     titleFromRecord(key, rec),
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
		Path:      file.Path,
		Metadata:  metadata,
	}
}

func titleFromRecord(key string, rec rawSession) string {
	if title := cleanTitle(firstNonEmpty(rec.DisplayName, rec.Subject)); title != "" {
		return title
	}
	var origin originRecord
	if json.Unmarshal(rec.Origin, &origin) == nil {
		if title := cleanTitle(firstNonEmpty(origin.Label, origin.Provider, origin.Surface, origin.ChatType)); title != "" {
			return title
		}
	}
	return key
}

func cleanTitle(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	return strings.Join(strings.Fields(text), " ")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func unixMillis(value int64) time.Time {
	if value <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(value)
}
