package cursor

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hxy91819/agent-session-manager/internal/cwdstatus"
	"github.com/hxy91819/agent-session-manager/internal/jsonlrecords"
	"github.com/hxy91819/agent-session-manager/internal/session"
	"github.com/hxy91819/agent-session-manager/internal/sessioncache"
)

const (
	Name                     = "cursor"
	maxJSONLRecordBytes      = 8 * 1024 * 1024
	oversizedJSONLRecordNote = "one or more oversized Cursor records may contain user evidence that bounded head/tail recovery could not identify"
	previewReadErrorNote     = "the Cursor transcript could not be read completely while collecting report evidence"
	oversizedRecordEdgeBytes = 64 * 1024
	oversizedTextEdgeBytes   = 4 * 1024
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
		// Coverage is dynamic report state, not stable session identity. Clear
		// cached warnings before re-reading previews with the current parser.
		delete(s.Metadata, session.MetadataReportEvidenceStatus)
		delete(s.Metadata, session.MetadataReportEvidenceNote)
		s.Metadata["source_home"] = home
		s.Metadata["project_key"] = file.ProjectKey
		if isAutoreviewTempCWD(s.CWD) {
			// Autoreview launches Cursor in a disposable checkout. Its transcript
			// may contain tool calls, so the one-shot transcript shape alone cannot
			// identify it reliably after the temporary cwd has been removed.
			s.Metadata["automation"] = "autoreview"
			s.Metadata["interaction_mode"] = "non_interactive"
		}
		switch {
		case file.CWDError != "":
			s.Metadata["cwd_error"] = file.CWDError
		case file.CWDMissing:
			s.Metadata["cwd_missing"] = "true"
		default:
			cwdChecker.Mark(&s)
		}
		if opts.Preview.Enabled() {
			previews, oversized, previewErr := readUserPreviews(file.Path, opts.Preview)
			s.Previews = previews
			if oversized > 0 {
				markReportEvidencePartial(s.Metadata, oversizedJSONLRecordNote)
			}
			if previewErr != nil {
				markReportEvidencePartial(s.Metadata, previewReadErrorNote)
			}
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

func isAutoreviewTempCWD(cwd string) bool {
	clean := filepath.Clean(cwd)
	if !filepath.IsAbs(clean) {
		return false
	}
	base := filepath.Base(clean)
	// The transcript persists the producer's cwd, but report discovery may run
	// with a different TMPDIR. These names are owned by autoreview; matching the
	// absolute path's basename keeps classification stable across processes.
	return strings.HasPrefix(base, "autoreview-cursor-agent.") ||
		strings.HasPrefix(base, "autoreview-fixture.")
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
	out := session.Session{Metadata: make(map[string]string)}
	var firstUserTitle string
	var lastUserTitle string
	oversized, err := readCursorRecords(r, func(line []byte) bool {
		var rec rawRecord
		if json.Unmarshal(line, &rec) != nil {
			return true
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
			return true
		}
		title := cleanTitle(unwrapUserText(messageText(msg.Content)))
		if title == "" {
			return true
		}
		if firstUserTitle == "" {
			firstUserTitle = title
		}
		lastUserTitle = title
		return true
	})
	if oversized > 0 {
		markReportEvidencePartial(out.Metadata, oversizedJSONLRecordNote)
	}
	if err != nil {
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

func readUserPreviews(path string, opts session.PreviewOptions) ([]session.MessagePreview, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = f.Close() }()

	var messages []session.MessagePreview
	oversized, err := readCursorRecords(f, func(line []byte) bool {
		var rec rawRecord
		if json.Unmarshal(line, &rec) != nil {
			return true
		}
		msg := parseMessage(rec)
		if messageRole(rec, msg) != "user" {
			return true
		}
		rawText := messageText(msg.Content)
		if text := cleanTitle(unwrapUserText(rawText)); text != "" {
			messages = append(messages, session.MessagePreview{
				Text:   text,
				At:     cursorMessageTime(rec.Timestamp, rawText),
				Source: "cursor:user",
			})
		}
		return true
	})
	return session.SelectMessagePreviews(messages, opts), oversized, err
}

func readCursorRecords(r io.Reader, visit func([]byte) bool) (int, error) {
	evidenceRisk := 0
	stopped := false
	_, err := jsonlrecords.ReadWithOversized(
		r,
		maxJSONLRecordBytes,
		oversizedRecordEdgeBytes,
		func(line []byte) bool {
			if stopped {
				return false
			}
			stopped = !visit(line)
			return !stopped
		},
		func(record jsonlrecords.OversizedRecord) {
			if stopped {
				return
			}
			if recovered, timestamped, ok := recoverOversizedCursorUserRecord(record); ok {
				stopped = !visit(recovered)
				if !timestamped {
					evidenceRisk++
				}
				return
			}
			if oversizedCursorCouldContainUserEvidence(record.Prefix) {
				evidenceRisk++
			}
		},
	)
	return evidenceRisk, err
}

func recoverOversizedCursorUserRecord(
	record jsonlrecords.OversizedRecord,
) ([]byte, bool, bool) {
	compact := jsonlrecords.Compact(record.Prefix)
	if !bytes.Contains(compact, []byte(`"role":"user"`)) {
		return nil, false, false
	}
	text, ok := jsonlrecords.TruncatedStringField(
		record,
		"content",
		oversizedTextEdgeBytes,
	)
	if !ok {
		text, ok = jsonlrecords.TruncatedStringField(
			record,
			"text",
			oversizedTextEdgeBytes,
		)
	}
	if !ok || strings.TrimSpace(text) == "" {
		return nil, false, false
	}

	timestamp, _ := jsonlrecords.CompleteStringField(record.Prefix, "timestamp")
	line, err := json.Marshal(map[string]any{
		"role":      "user",
		"timestamp": timestamp,
		"content":   text,
	})
	if err != nil {
		return nil, false, false
	}
	return line, !cursorMessageTime(timestamp, text).IsZero(), true
}

func oversizedCursorCouldContainUserEvidence(prefix []byte) bool {
	compact := jsonlrecords.Compact(prefix)
	if bytes.Contains(compact, []byte(`"role":"user"`)) {
		return true
	}
	if bytes.Contains(compact, []byte(`"role":"assistant"`)) {
		return false
	}
	return true
}

func markReportEvidencePartial(metadata map[string]string, note string) {
	metadata[session.MetadataReportEvidenceStatus] = session.ReportEvidencePartial
	metadata[session.MetadataReportEvidenceNote] = note
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
		if block.Type != "" && block.Type != "text" && block.Type != "input_text" {
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

func unwrapUserText(text string) string {
	const (
		openTag  = "<user_query>"
		closeTag = "</user_query>"
	)
	start := strings.Index(text, openTag)
	if start < 0 {
		return text
	}
	start += len(openTag)
	end := strings.Index(text[start:], closeTag)
	if end < 0 {
		return text
	}
	return strings.TrimSpace(text[start : start+end])
}

func cursorMessageTime(topLevel, text string) time.Time {
	if parsed := parseTime(topLevel); !parsed.IsZero() {
		return parsed
	}
	const (
		openTag  = "<timestamp>"
		closeTag = "</timestamp>"
	)
	start := strings.Index(text, openTag)
	if start < 0 {
		return time.Time{}
	}
	start += len(openTag)
	end := strings.Index(text[start:], closeTag)
	if end < 0 {
		return time.Time{}
	}
	return parseCursorTimestamp(strings.TrimSpace(text[start : start+end]))
}

func parseCursorTimestamp(value string) time.Time {
	open := strings.LastIndex(value, "(")
	if open < 0 || !strings.HasSuffix(value, ")") {
		return time.Time{}
	}
	localValue := strings.TrimSpace(value[:open])
	zoneValue := strings.TrimSuffix(value[open+1:], ")")
	offset, ok := cursorUTCOffset(zoneValue)
	if !ok {
		return time.Time{}
	}
	// Cursor owns this wrapper format. Parsing the persisted UTC offset is more
	// reliable than file mtime and lets report windows prove when a prompt
	// actually occurred.
	location := time.FixedZone(zoneValue, offset)
	parsed, err := time.ParseInLocation(
		"Monday, Jan 2, 2006, 3:04 PM",
		localValue,
		location,
	)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func cursorUTCOffset(value string) (int, bool) {
	if value == "UTC" {
		return 0, true
	}
	if !strings.HasPrefix(value, "UTC") || len(value) < 5 {
		return 0, false
	}
	sign := 1
	offsetValue := value[3:]
	switch offsetValue[0] {
	case '+':
	case '-':
		sign = -1
	default:
		return 0, false
	}
	var hours, minutes int
	if _, err := fmt.Sscanf(offsetValue[1:], "%d:%d", &hours, &minutes); err != nil {
		minutes = 0
		if _, hourErr := fmt.Sscanf(offsetValue[1:], "%d", &hours); hourErr != nil {
			return 0, false
		}
	}
	if hours > 23 || minutes > 59 {
		return 0, false
	}
	return sign * (hours*60 + minutes) * 60, true
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
