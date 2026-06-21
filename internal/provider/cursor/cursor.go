package cursor

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hxy91819/agent-session-manager/internal/cwdstatus"
	"github.com/hxy91819/agent-session-manager/internal/session"
	"github.com/hxy91819/agent-session-manager/internal/sessioncache"
)

const Name = "cursor"

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
	files, err := collectTranscripts(filepath.Join(home, "projects"), opts)
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
			s, err = parseSessionFile(file.Path)
			if err != nil {
				continue
			}
			cache.Put(id, s)
		}
		keep[sessioncache.Key(Name, file.Path)] = struct{}{}
		s.ID = file.ChatID
		s.Provider = Name
		s.Path = file.Path
		s.CWD = file.CWD
		s.UpdatedAt = file.ModTime
		if s.CreatedAt.IsZero() {
			s.CreatedAt = file.ModTime
		}
		if s.Metadata == nil {
			s.Metadata = make(map[string]string)
		}
		s.Metadata["source_home"] = home
		s.Metadata["project_key"] = file.ProjectKey
		switch {
		case file.CWDError != "":
			s.Metadata["cwd_error"] = file.CWDError
		case file.CWDMissing:
			s.Metadata["cwd_missing"] = "true"
		default:
			cwdChecker.Mark(&s)
		}
		if opts.Preview.Enabled() {
			s.Previews = readUserPreviews(file.Path, opts.Preview)
		} else {
			s.Previews = nil
		}
		sessions = append(sessions, s)
	}
	if shouldPruneCache(opts, len(files)) {
		cache.Keep(keep)
	}
	_ = cache.Save(cachePath)
	return sessions, nil
}

func (p Provider) ResumeCommand(s session.Session) session.ExecSpec {
	if s.Metadata["cwd_missing"] == "true" || s.Metadata["cwd_error"] != "" {
		return session.ExecSpec{UnsupportedReason: "Cursor resume cwd is unavailable or ambiguous"}
	}
	return session.ExecSpec{
		Dir:  s.CWD,
		Args: []string{"cursor-agent", "--resume", s.ID},
	}
}

func (p Provider) NewCommand(cwd string) session.ExecSpec {
	return session.ExecSpec{
		Dir:  cwd,
		Args: []string{"cursor-agent"},
	}
}

func (p Provider) home() (string, error) {
	if p.Home != "" {
		return p.Home, nil
	}
	if home := os.Getenv("CURSOR_HOME"); home != "" {
		return home, nil
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(userHome, ".cursor"), nil
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

func shouldPruneCache(opts session.DiscoverOptions, fileCount int) bool {
	return opts.Since.IsZero() && (opts.LimitFiles <= 0 || fileCount < opts.LimitFiles)
}

type fileInfo struct {
	Path       string
	ChatID     string
	ProjectKey string
	CWD        string
	CWDMissing bool
	CWDError   string
	Size       int64
	ModTime    time.Time
}

func collectTranscripts(projectRoot string, opts session.DiscoverOptions) ([]fileInfo, error) {
	var files []fileInfo
	cwdCache := make(map[string]cwdResolution)
	err := filepath.WalkDir(projectRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == "subagents" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".jsonl" || containsPathPart(path, "subagents") {
			return nil
		}
		chatID := strings.TrimSuffix(filepath.Base(path), ".jsonl")
		if chatID == "" || filepath.Base(filepath.Dir(path)) != chatID {
			return nil
		}
		if filepath.Base(filepath.Dir(filepath.Dir(path))) != "agent-transcripts" {
			return nil
		}
		projectDir := filepath.Dir(filepath.Dir(filepath.Dir(path)))
		projectKey := filepath.Base(projectDir)
		resolution, ok := cwdCache[projectKey]
		if !ok {
			resolution = projectCWD(projectDir, projectKey)
			cwdCache[projectKey] = resolution
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if !opts.Since.IsZero() && info.ModTime().Before(opts.Since) {
			return nil
		}
		files = append(files, fileInfo{
			Path:       path,
			ChatID:     chatID,
			ProjectKey: projectKey,
			CWD:        resolution.CWD,
			CWDMissing: resolution.Missing,
			CWDError:   resolution.Error,
			Size:       info.Size(),
			ModTime:    info.ModTime(),
		})
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

func containsPathPart(path, part string) bool {
	for _, value := range strings.Split(filepath.Clean(path), string(os.PathSeparator)) {
		if value == part {
			return true
		}
	}
	return false
}

func projectCWD(projectDir, projectKey string) cwdResolution {
	// worker.log records Cursor's real workspace path. The project directory
	// name is only a lossy fallback because "-" can be either a path separator
	// or a literal character in the original cwd.
	if cwd := readWorkspacePath(filepath.Join(projectDir, "worker.log")); cwd != "" {
		return checkedCWD(cwd)
	}
	return decodeProjectCWD(projectKey)
}

func readWorkspacePath(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	const marker = "workspacePath="
	for scanner.Scan() {
		line := scanner.Text()
		idx := strings.Index(line, marker)
		if idx < 0 {
			continue
		}
		value := strings.TrimSpace(line[idx+len(marker):])
		if value == "" {
			continue
		}
		return value
	}
	return ""
}

type cwdResolution struct {
	CWD     string
	Missing bool
	Error   string
}

func decodeProjectCWD(projectKey string) cwdResolution {
	if decoded, err := url.PathUnescape(projectKey); err == nil && decoded != projectKey && isProjectCWDAbs(decoded) {
		return checkedCWD(decoded)
	}
	if !strings.Contains(projectKey, "-") {
		return checkedCWD("/" + projectKey)
	}
	// Cursor's project key uses "-" for path separators, which is lossy when
	// any original path segment may also contain "-". Leave CWD empty rather
	// than publishing a guessed project path to JSON, grouping, and resume.
	return cwdResolution{Error: "cursor project cwd encoding is ambiguous"}
}

func isProjectCWDAbs(cwd string) bool {
	return filepath.IsAbs(cwd) || strings.HasPrefix(cwd, "/")
}

func checkedCWD(cwd string) cwdResolution {
	info, err := os.Stat(cwd)
	if err == nil && info.IsDir() {
		return cwdResolution{CWD: cwd}
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return cwdResolution{CWD: cwd, Error: err.Error()}
	}
	return cwdResolution{CWD: cwd, Missing: true}
}

type rawRecord struct {
	Role      string          `json:"role"`
	Timestamp string          `json:"timestamp"`
	Message   json.RawMessage `json:"message"`
	Content   json.RawMessage `json:"content"`
}

type cursorMessage struct {
	Role    string          `json:"role"`
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
	defer func() { _ = f.Close() }()
	return parseSession(f)
}

func parseSession(r io.Reader) (session.Session, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)

	out := session.Session{Metadata: make(map[string]string)}
	var firstUserTitle string
	var lastUserTitle string
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var rec rawRecord
		if json.Unmarshal([]byte(line), &rec) != nil {
			continue
		}
		if t := parseTime(rec.Timestamp); !t.IsZero() {
			if out.CreatedAt.IsZero() || t.Before(out.CreatedAt) {
				out.CreatedAt = t
			}
			if t.After(out.UpdatedAt) {
				out.UpdatedAt = t
			}
		}
		msg := parseMessage(rec)
		if messageRole(rec, msg) != "user" {
			continue
		}
		title := cleanTitle(messageText(msg.Content))
		if title == "" {
			continue
		}
		if firstUserTitle == "" {
			firstUserTitle = title
		}
		lastUserTitle = title
	}
	if err := scanner.Err(); err != nil {
		return session.Session{}, err
	}
	if firstUserTitle != "" {
		out.Title = firstUserTitle
		out.Metadata["title_source"] = "first_user"
	} else if lastUserTitle != "" {
		out.Title = lastUserTitle
		out.Metadata["title_source"] = "last_user"
	}
	return out, nil
}

func readUserPreviews(path string, opts session.PreviewOptions) []session.MessagePreview {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	var messages []session.MessagePreview
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var rec rawRecord
		if json.Unmarshal([]byte(line), &rec) != nil {
			continue
		}
		msg := parseMessage(rec)
		if messageRole(rec, msg) != "user" {
			continue
		}
		if text := cleanTitle(messageText(msg.Content)); text != "" {
			messages = append(messages, session.MessagePreview{
				Text:   text,
				At:     parseTime(rec.Timestamp),
				Source: "cursor:user",
			})
		}
	}
	return session.SelectMessagePreviews(messages, opts)
}

func parseMessage(rec rawRecord) cursorMessage {
	if len(rec.Message) != 0 {
		var msg cursorMessage
		if json.Unmarshal(rec.Message, &msg) == nil {
			return msg
		}
	}
	return cursorMessage{Role: rec.Role, Content: rec.Content}
}

func messageRole(rec rawRecord, msg cursorMessage) string {
	if msg.Role != "" {
		return msg.Role
	}
	return rec.Role
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
