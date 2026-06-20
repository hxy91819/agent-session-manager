package opencode

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
	"github.com/hxy91819/agent-session-manager/internal/sessioncache"
)

const Name = "opencode"

type Provider struct {
	Home      string
	CachePath string
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
	storageRoot := filepath.Join(home, "storage")
	files, err := collectSessionJSON(filepath.Join(storageRoot, "session"), opts)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	cachePath := p.cachePath()
	cache := sessioncache.Load(cachePath)
	keep := make(map[string]struct{}, len(files))
	cwdChecker := cwdstatus.NewChecker()
	sessions := make([]session.Session, 0, len(files))
	for _, file := range files {
		id := sessioncache.FileIdentity{
			Provider: Name,
			Path:     file.Path,
			Size:     file.Size,
			ModTime:  file.ModTime,
		}
		s, ok := cache.Get(id)
		if !ok {
			rec, err := readSession(file.Path)
			if err != nil || strings.TrimSpace(rec.ID) == "" {
				continue
			}
			s = sessionFromRecord(file, rec)
			cache.Put(id, s)
		}
		if s.ID == "" {
			continue
		}
		if s.Metadata == nil {
			s.Metadata = make(map[string]string)
		}
		keep[sessioncache.Key(Name, file.Path)] = struct{}{}

		if s.CWD == "" && strings.TrimSpace(s.Metadata["project_id"]) != "" {
			project, err := readProject(filepath.Join(storageRoot, "project", s.Metadata["project_id"]+".json"))
			if err == nil {
				s.CWD = strings.TrimSpace(project.Worktree)
			}
		}
		if s.CWD == "" {
			continue
		}
		if s.Title == "" {
			if title := fallbackTitleFromMessages(storageRoot, s.ID); title != "" {
				s.Title = title
				s.Metadata["title_source"] = "message"
			}
		}
		if s.Title == "" {
			delete(s.Metadata, "title_source")
		}
		if opts.Preview.Enabled() {
			s.Previews = userPreviewsFromMessages(storageRoot, s.ID, opts.Preview)
		} else {
			s.Previews = nil
		}
		cwdChecker.Mark(&s)
		sessions = append(sessions, s)
	}
	if shouldPruneCache(opts, len(files)) {
		cache.Keep(keep)
	}
	_ = cache.Save(cachePath)
	return sessions, nil
}

func sessionFromRecord(file fileInfo, rec sessionRecord) session.Session {
	s := session.Session{
		ID:        strings.TrimSpace(rec.ID),
		Provider:  Name,
		CWD:       strings.TrimSpace(rec.Directory),
		Title:     cleanTitle(rec.Title),
		CreatedAt: unixMillis(rec.Time.Created),
		UpdatedAt: file.ModTime,
		Path:      file.Path,
		Metadata:  make(map[string]string),
	}
	if s.CreatedAt.IsZero() {
		s.CreatedAt = file.ModTime
	}
	if updated := unixMillis(rec.Time.Updated); !updated.IsZero() {
		s.Metadata["opencode_updated_at"] = updated.Format(time.RFC3339Nano)
	}
	if rec.ProjectID != "" {
		s.Metadata["project_id"] = rec.ProjectID
	}
	if rec.Version != "" {
		s.Metadata["version"] = rec.Version
	}
	if s.Title != "" {
		s.Metadata["title_source"] = "session"
	}
	return s
}

func shouldPruneCache(opts session.DiscoverOptions, fileCount int) bool {
	return opts.Since.IsZero() && (opts.LimitFiles <= 0 || fileCount < opts.LimitFiles)
}

func (p Provider) cachePath() string {
	if p.CachePath != "" {
		return p.CachePath
	}
	path, err := sessioncache.DefaultPath(Name)
	if err != nil {
		return ""
	}
	return path
}

func (p Provider) ResumeCommand(s session.Session) session.ExecSpec {
	return session.ExecSpec{
		Dir:  s.CWD,
		Args: []string{"opencode", "-s", s.ID},
	}
}

func (p Provider) NewCommand(cwd string) session.ExecSpec {
	return session.ExecSpec{
		Dir:  cwd,
		Args: []string{"opencode"},
	}
}

func (p Provider) home() (string, error) {
	if p.Home != "" {
		return p.Home, nil
	}
	for _, key := range []string{"OPENCODE_HOME", "OPENCODE_DATA_HOME", "OPENCODE_DATA_DIR"} {
		if home := os.Getenv(key); home != "" {
			return home, nil
		}
	}
	if dataHome := os.Getenv("XDG_DATA_HOME"); dataHome != "" {
		return filepath.Join(dataHome, "opencode"), nil
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(userHome, ".local", "share", "opencode"), nil
}

type fileInfo struct {
	Path    string
	Size    int64
	ModTime time.Time
}

func collectSessionJSON(root string, opts session.DiscoverOptions) ([]fileInfo, error) {
	var files []fileInfo
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".json" {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if !opts.Since.IsZero() && info.ModTime().Before(opts.Since) {
			return nil
		}
		files = append(files, fileInfo{Path: path, Size: info.Size(), ModTime: info.ModTime()})
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

type sessionRecord struct {
	ID        string     `json:"id"`
	Version   string     `json:"version"`
	ProjectID string     `json:"projectID"`
	Directory string     `json:"directory"`
	Title     string     `json:"title"`
	Time      recordTime `json:"time"`
}

type projectRecord struct {
	ID       string     `json:"id"`
	Worktree string     `json:"worktree"`
	Time     recordTime `json:"time"`
}

type messageRecord struct {
	ID        string     `json:"id"`
	SessionID string     `json:"sessionID"`
	Role      string     `json:"role"`
	Time      recordTime `json:"time"`
}

type partRecord struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type recordTime struct {
	Created int64 `json:"created"`
	Updated int64 `json:"updated"`
}

func readSession(path string) (sessionRecord, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return sessionRecord{}, err
	}
	var rec sessionRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return sessionRecord{}, err
	}
	return rec, nil
}

func readProject(path string) (projectRecord, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return projectRecord{}, err
	}
	var rec projectRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return projectRecord{}, err
	}
	return rec, nil
}

func fallbackTitleFromMessages(storageRoot, sessionID string) string {
	messages, err := collectMessageJSON(filepath.Join(storageRoot, "message", sessionID))
	if err != nil {
		return ""
	}
	var title string
	for _, message := range messages {
		rec, err := readMessage(message.Path)
		if err != nil || rec.Role != "user" {
			continue
		}
		if text := titleFromMessageParts(filepath.Join(storageRoot, "part", rec.ID)); text != "" {
			title = text
		}
	}
	return title
}

func userPreviewsFromMessages(storageRoot, sessionID string, opts session.PreviewOptions) []session.MessagePreview {
	messages, err := collectMessageJSON(filepath.Join(storageRoot, "message", sessionID))
	if err != nil {
		return nil
	}
	var previews []session.MessagePreview
	for _, message := range messages {
		rec, err := readMessage(message.Path)
		if err != nil || rec.Role != "user" {
			continue
		}
		if text := titleFromMessageParts(filepath.Join(storageRoot, "part", rec.ID)); text != "" {
			at := unixMillis(rec.Time.Created)
			if at.IsZero() {
				at = unixMillis(rec.Time.Updated)
			}
			if at.IsZero() {
				at = message.ModTime
			}
			previews = append(previews, session.MessagePreview{
				Text:   text,
				At:     at,
				Source: "opencode:message",
			})
		}
	}
	return session.SelectMessagePreviews(previews, opts)
}

func collectMessageJSON(root string) ([]fileInfo, error) {
	var files []fileInfo
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() || filepath.Ext(path) != ".json" {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		files = append(files, fileInfo{Path: path, ModTime: info.ModTime()})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].ModTime.Before(files[j].ModTime)
	})
	return files, nil
}

func readMessage(path string) (messageRecord, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return messageRecord{}, err
	}
	var rec messageRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return messageRecord{}, err
	}
	return rec, nil
}

func titleFromMessageParts(root string) string {
	var parts []string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".json" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		var rec partRecord
		if json.Unmarshal(data, &rec) != nil {
			return nil
		}
		if rec.Type != "" && rec.Type != "text" {
			return nil
		}
		if strings.TrimSpace(rec.Text) != "" {
			parts = append(parts, rec.Text)
		}
		return nil
	})
	return cleanTitle(strings.Join(parts, "\n"))
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

func unixMillis(value int64) time.Time {
	if value <= 0 {
		return time.Time{}
	}
	if value < 1_000_000_000_000 {
		return time.Unix(value, 0).UTC()
	}
	return time.UnixMilli(value).UTC()
}
