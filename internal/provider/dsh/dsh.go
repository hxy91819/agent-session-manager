// Package dsh discovers DeepSeek Harness (`dsh`) sessions from its JSONL
// session-persistence store.
//
// Store layout (mirroring dsh-session-persistence-jsonl):
//
//	<home>/sessions/<project-dir>/<encoded-session-id>/session.jsonl.zstd
//	<home>/sessions/<project-dir>/<encoded-session-id>/session.jsonl
//
// The first record of every log is the session header (`type":"session"`)
// carrying id, createdAt (Unix epoch milliseconds), cwd, origin, and
// agentPreset. Event rows follow; one append-only Zstandard frame per write
// batch. Titles come from `session/title` events (latest wins); when no title
// event exists the first human `user/message` event is the fallback.
package dsh

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/hxy91819/agent-session-manager/internal/cwdstatus"
	"github.com/hxy91819/agent-session-manager/internal/session"
	"github.com/hxy91819/agent-session-manager/internal/sessioncache"
)

const (
	Name = "dsh"
	// Reject any other on-disk format version: dsh itself refuses foreign
	// versions on load, so treating them as unreadable matches producer
	// semantics and keeps a newer format from being silently misread.
	supportedFormatVersion = 0

	zstdLogName  = "session.jsonl.zstd"
	plainLogName = "session.jsonl"
)

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
	files, err := collectLogFiles(filepath.Join(home, "sessions"), opts)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	if sessioncache.SkipLoadForEmptyDiscovery(opts, len(files)) {
		return []session.Session{}, nil
	}

	// A cold parse decompresses the whole append-only log (the latest
	// session/title event decides the title), so the primary parse result is
	// cached by path/size/mtime identity; report previews are a dynamic side
	// input re-read on every pass, and cwd status is reapplied after hits.
	cachePath := p.cachePath()
	cache := sessioncache.Load(cachePath)
	keep := make(map[string]struct{}, len(files))
	cwdChecker := cwdstatus.NewChecker()
	sessions := make([]session.Session, 0, len(files))
	for _, file := range files {
		identity := sessioncache.FileIdentity{
			Provider: Name,
			Path:     file.Path,
			Size:     file.Size,
			ModTime:  file.ModTime,
		}
		s, ok := cache.Get(identity)
		if !ok {
			parsed, readErr := parseLog(file.Path)
			if readErr != nil || parsed.header.ID == "" {
				continue
			}
			s = parsed.session(file)
			s = cache.Put(identity, s)
		}
		if strings.TrimSpace(s.ID) == "" {
			continue
		}
		if s.Metadata == nil {
			s.Metadata = make(map[string]string)
		}
		keep[sessioncache.Key(Name, file.Path)] = struct{}{}

		s.Provider = Name
		s.Path = file.Path
		delete(s.Metadata, session.MetadataReportEvidenceStatus)
		delete(s.Metadata, session.MetadataReportEvidenceNote)

		if opts.Preview.Enabled() {
			previews, readErr := readUserPreviews(file.Path, opts.Preview)
			s.Previews = previews
			if readErr != nil {
				s.Metadata[session.MetadataReportEvidenceStatus] = session.ReportEvidencePartial
				s.Metadata[session.MetadataReportEvidenceNote] = "the dsh session log could not be read completely while collecting report evidence"
			}
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

// ResumeCommand emits the documented `dsh --profile tui --resume <session-id>`
// invocation from the original session cwd. The dsh launcher hands flags after
// its own to the booted profile, where the app parses --resume; asm cannot
// verify a deployment's tui profile is installed, so dsh is effectively
// discover-only when it is not.
func (p Provider) ResumeCommand(s session.Session) session.ExecSpec {
	return session.ExecSpec{
		Dir:  s.CWD,
		Args: []string{"dsh", "--profile", "tui", "--resume", s.ID},
	}
}

func (p Provider) NewCommand(string) session.ExecSpec {
	return session.ExecSpec{UnsupportedReason: "dsh new session is not supported by asm yet"}
}

func (p Provider) home() (string, error) {
	if p.Home != "" {
		return p.Home, nil
	}
	if home := os.Getenv("DSH_HOME"); home != "" {
		return home, nil
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(userHome, ".dsh"), nil
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
	Path    string
	Size    int64
	ModTime time.Time
}

// collectLogFiles lists session log artifacts newest-first: each session owns a
// directory under its project directory, holding exactly one physical encoding
// (zstd, or plaintext when a deployment selects compression "none").
func collectLogFiles(root string, opts session.DiscoverOptions) ([]fileInfo, error) {
	projects, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var files []fileInfo
	for _, project := range projects {
		if !project.IsDir() {
			continue
		}
		sessionDirs, err := os.ReadDir(filepath.Join(root, project.Name()))
		if err != nil {
			continue
		}
		for _, dir := range sessionDirs {
			if !dir.IsDir() {
				continue
			}
			sessionDir := filepath.Join(root, project.Name(), dir.Name())
			for _, name := range []string{zstdLogName, plainLogName} {
				path := filepath.Join(sessionDir, name)
				info, err := os.Stat(path)
				if err != nil || info.IsDir() {
					continue
				}
				if !opts.Since.IsZero() && info.ModTime().Before(opts.Since) {
					continue
				}
				files = append(files, fileInfo{Path: path, Size: info.Size(), ModTime: info.ModTime()})
				break
			}
		}
	}
	sort.SliceStable(files, func(i, j int) bool {
		if files[i].ModTime.Equal(files[j].ModTime) {
			return files[i].Path > files[j].Path
		}
		return files[i].ModTime.After(files[j].ModTime)
	})
	if opts.LimitFiles > 0 && len(files) > opts.LimitFiles {
		files = files[:opts.LimitFiles]
	}
	return files, nil
}

type headerLine struct {
	Type            string `json:"type"`
	Version         int    `json:"version"`
	ID              string `json:"id"`
	CreatedAt       int64  `json:"createdAt"`
	CWD             string `json:"cwd"`
	ParentSession   string `json:"parentSession"`
	Origin          string `json:"origin"`
	DelegationDepth int    `json:"delegationDepth"`
	AgentPreset     string `json:"agentPreset"`
}

type eventEnvelope struct {
	Type string          `json:"type"`
	Time int64           `json:"time"`
	Data json.RawMessage `json:"data"`
}

type messageSource struct {
	Kind string `json:"kind"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type userMessageData struct {
	Content []contentBlock `json:"content"`
	Source  messageSource  `json:"source"`
}

type titleEventData struct {
	Title  string        `json:"title"`
	Source messageSource `json:"source"`
}

type parsedLog struct {
	header     headerLine
	title      string
	titleEvent bool
	lastTime   int64
}

func (p parsedLog) session(file fileInfo) session.Session {
	s := session.Session{
		ID:        strings.TrimSpace(p.header.ID),
		Provider:  Name,
		CWD:       strings.TrimSpace(p.header.CWD),
		CreatedAt: unixMillis(p.header.CreatedAt),
		UpdatedAt: unixMillis(p.lastTime),
		Path:      file.Path,
		Metadata:  make(map[string]string),
	}
	if s.UpdatedAt.IsZero() {
		s.UpdatedAt = file.ModTime
	}
	if s.CreatedAt.IsZero() {
		s.CreatedAt = s.UpdatedAt
	}
	if p.title != "" {
		s.Title = session.NormalizeTitle(p.title)
		if p.titleEvent {
			s.Metadata["title_source"] = "session_title"
		} else {
			s.Metadata["title_source"] = "first_input"
		}
	}
	if preset := strings.TrimSpace(p.header.AgentPreset); preset != "" {
		s.Metadata["agent_preset"] = preset
	}
	if p.header.Origin == "subagent" {
		s.Metadata["origin"] = "subagent"
	}
	if p.header.DelegationDepth > 0 {
		s.Metadata["delegation_depth"] = strconv.Itoa(p.header.DelegationDepth)
	}
	if parent := strings.TrimSpace(p.header.ParentSession); parent != "" {
		// dsh persists the seed parent explicitly, so reports can avoid
		// counting a forked child as a second copy of the same work.
		s.Metadata[session.MetadataParentThreadID] = parent
	}
	return s
}

// parseLog decodes a session log tolerantly: a crash-torn final Zstandard
// frame drops only its tail (dsh repairs it on next resume), and unrecognized
// rows are skipped instead of rejecting the whole session.
func parseLog(path string) (parsedLog, error) {
	lines, err := readLogLines(path)
	if err != nil {
		return parsedLog{}, err
	}
	if len(lines) == 0 {
		return parsedLog{}, errors.New("empty session log")
	}
	var header headerLine
	if err := json.Unmarshal(lines[0], &header); err != nil {
		return parsedLog{}, err
	}
	if header.Type != "session" {
		return parsedLog{}, errors.New("first line is not a session header")
	}
	if header.Version != supportedFormatVersion {
		return parsedLog{}, errors.New("unsupported session format version")
	}
	if strings.TrimSpace(header.ID) == "" {
		return parsedLog{}, errors.New("session header has no id")
	}

	parsed := parsedLog{header: header, lastTime: header.CreatedAt}
	for _, line := range lines[1:] {
		var event eventEnvelope
		if json.Unmarshal(line, &event) != nil {
			continue
		}
		if event.Time > parsed.lastTime {
			parsed.lastTime = event.Time
		}
		switch event.Type {
		case "session/title":
			// Latest title event wins: user renames and regenerated titles are
			// appended, so the last one is the current title.
			var data titleEventData
			if json.Unmarshal(event.Data, &data) == nil {
				if title := cleanTitle(data.Title); title != "" {
					parsed.title = title
					parsed.titleEvent = true
				}
			}
		case "user/message":
			if parsed.titleEvent {
				continue
			}
			if text, ok := humanMessageText(event.Data); ok {
				if parsed.title == "" {
					parsed.title = cleanTitle(text)
				}
			}
		}
	}
	return parsed, nil
}

func readUserPreviews(path string, opts session.PreviewOptions) ([]session.MessagePreview, error) {
	lines, err := readLogLines(path)
	if err != nil {
		return nil, err
	}
	var previews []session.MessagePreview
	for _, line := range lines {
		var event eventEnvelope
		if json.Unmarshal(line, &event) != nil || event.Type != "user/message" {
			continue
		}
		text, ok := humanMessageText(event.Data)
		if !ok {
			continue
		}
		if text = cleanTitle(text); text != "" {
			previews = append(previews, session.MessagePreview{
				Text:   text,
				At:     unixMillis(event.Time),
				Source: "dsh:user_message",
			})
		}
	}
	return session.SelectMessagePreviews(previews, opts), nil
}

// humanMessageText joins the text blocks of a direct human prompt. dsh tags
// synthetic injected context (file-change notices, skills, cron) with a
// non-"user" source kind, so this filter never needs text-shape heuristics.
func humanMessageText(raw json.RawMessage) (string, bool) {
	var data userMessageData
	if json.Unmarshal(raw, &data) != nil || data.Source.Kind != "user" {
		return "", false
	}
	parts := make([]string, 0, len(data.Content))
	for _, block := range data.Content {
		if block.Type != "" && block.Type != "text" {
			continue
		}
		if strings.TrimSpace(block.Text) != "" {
			parts = append(parts, block.Text)
		}
	}
	text := strings.Join(parts, "\n")
	return text, strings.TrimSpace(text) != ""
}

// readLogLines returns the decoded JSONL rows. Zstandard logs store one frame
// per write batch; decoding streams through them and keeps the complete-prefix
// plaintext even when a torn trailing frame reports an error.
func readLogLines(path string) ([][]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var data []byte
	if strings.HasSuffix(path, ".zstd") {
		decoder, err := zstd.NewReader(f)
		if err != nil {
			return nil, err
		}
		// io.ReadAll returns the decoded prefix together with the error, so a
		// torn final frame still yields every complete committed record.
		data, _ = io.ReadAll(decoder)
		decoder.Close()
	} else {
		data, err = io.ReadAll(f)
		if err != nil {
			return nil, err
		}
	}

	rows := bytes.Split(data, []byte{'\n'})
	lines := make([][]byte, 0, len(rows))
	for _, row := range rows {
		if len(bytes.TrimSpace(row)) > 0 {
			lines = append(lines, row)
		}
	}
	return lines, nil
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
	return time.UnixMilli(value).UTC()
}
