package claude

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hxy91819/agent-session-manager/internal/cwdstatus"
	"github.com/hxy91819/agent-session-manager/internal/jsonlrecords"
	"github.com/hxy91819/agent-session-manager/internal/session"
	"github.com/hxy91819/agent-session-manager/internal/sessioncache"
	"github.com/hxy91819/agent-session-manager/internal/startupdiag"
)

const (
	Name                     = "claude"
	maxJSONLRecordBytes      = 8 * 1024 * 1024
	oversizedJSONLRecordNote = "one or more oversized Claude records may contain user evidence that bounded head/tail recovery could not identify"
	previewReadErrorNote     = "the Claude transcript could not be read completely while collecting report evidence"
	oversizedRecordEdgeBytes = 64 * 1024
	oversizedTextEdgeBytes   = 4 * 1024
	metadataParseCacheKey    = "_asm_claude_parse_mode"
	metadataParseCacheValue  = "metadata"
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
	homes, err := p.homes()
	if err != nil {
		return nil, err
	}
	finishEnumerate := startupdiag.Begin(Name, "enumerate_filter")
	files, err := collectHomeJSONL(homes, opts)
	var sourceBytes int64
	for _, file := range files {
		sourceBytes += file.Size
	}
	finishEnumerate(int64(len(files)), sourceBytes)
	if err != nil {
		return nil, err
	}
	if sessioncache.SkipLoadForEmptyDiscovery(opts, len(files)) {
		return []session.Session{}, nil
	}

	cachePath := p.cachePath()
	finishCacheRead := startupdiag.Begin(Name, "cache_read")
	cache := sessioncache.Load(cachePath)
	finishCacheRead(1, 0)
	keep := make(map[string]struct{}, len(files))
	cwdChecker := cwdstatus.NewChecker()
	seen := make(map[string]struct{}, len(files))
	sessions := make([]session.Session, 0, len(files))
	for _, file := range files {
		id := sessioncache.FileIdentity{
			Provider: Name,
			Path:     file.Path,
			Size:     file.Size,
			ModTime:  file.ModTime,
		}
		s, ok := cache.Get(id)
		if ok && opts.Preview.Enabled() && s.Metadata[metadataParseCacheKey] == metadataParseCacheValue {
			ok = false
		}
		if !ok {
			finishPrimaryParse := startupdiag.Begin(Name, "primary_parse")
			var err error
			if opts.Preview.Enabled() {
				s, err = parseSessionFile(file.Path)
			} else {
				s, err = parseSessionMetadataFile(file.Path)
				if s.Metadata == nil {
					s.Metadata = make(map[string]string)
				}
				s.Metadata[metadataParseCacheKey] = metadataParseCacheValue
			}
			finishPrimaryParse(1, file.Size)
			if err != nil || s.ID == "" || s.CWD == "" {
				continue
			}
			s = cache.Put(id, s)
		}
		if s.ID == "" || s.CWD == "" {
			continue
		}
		delete(s.Metadata, metadataParseCacheKey)
		keep[sessioncache.Key(Name, file.Path)] = struct{}{}
		if _, ok := seen[s.ID]; ok {
			continue
		}
		// Claude can leave the same session ID in multiple project files after
		// cwd/project changes. Resume targets the ID, so expose only the newest
		// file to avoid showing one Claude conversation as several sessions.
		seen[s.ID] = struct{}{}
		if s.Metadata == nil {
			s.Metadata = make(map[string]string)
		}
		// Coverage is recomputed from the current parser and preview window.
		// Cached warnings from the former skip-all-oversized behavior must not
		// survive after bounded user-message recovery succeeds.
		delete(s.Metadata, session.MetadataReportEvidenceStatus)
		delete(s.Metadata, session.MetadataReportEvidenceNote)
		s.Metadata["source_home"] = file.Home
		s.Provider = Name
		s.Path = file.Path
		s.UpdatedAt = file.ModTime
		if s.CreatedAt.IsZero() {
			s.CreatedAt = file.ModTime
		}
		cwdChecker.Mark(&s)
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
	finishCacheWrite := startupdiag.Begin(Name, "cache_write")
	if shouldPruneCache(opts, len(files)) {
		cache.Keep(keep)
	}
	_ = cache.Save(cachePath)
	finishCacheWrite(1, 0)
	return sessions, nil
}

func (p Provider) homes() ([]string, error) {
	if p.Home != "" {
		return []string{p.Home}, nil
	}
	home := os.Getenv("CLAUDE_HOME")
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		home = filepath.Join(userHome, ".claude")
	}
	return append([]string{home}, splitHomeList(os.Getenv("ASM_CLAUDE_EXTRA_HOMES"))...), nil
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
		Args: []string{"claude", "--resume", s.ID},
	}
}

func (p Provider) NewCommand(cwd string) session.ExecSpec {
	return session.ExecSpec{
		Dir:  cwd,
		Args: []string{"claude"},
	}
}

type fileInfo struct {
	Path    string
	Home    string
	Size    int64
	ModTime time.Time
}

func collectHomeJSONL(homes []string, opts session.DiscoverOptions) ([]fileInfo, error) {
	var files []fileInfo
	for _, home := range homes {
		home = strings.TrimSpace(home)
		if home == "" {
			continue
		}
		homeFiles, err := collectJSONL(home, filepath.Join(home, "projects"), opts)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, err
		}
		files = append(files, homeFiles...)
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].ModTime.After(files[j].ModTime)
	})
	if opts.LimitFiles > 0 && len(files) > opts.LimitFiles {
		files = files[:opts.LimitFiles]
	}
	return files, nil
}

func collectJSONL(home, root string, opts session.DiscoverOptions) ([]fileInfo, error) {
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
		files = append(files, fileInfo{Path: path, Home: home, Size: info.Size(), ModTime: info.ModTime()})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

func splitHomeList(value string) []string {
	if value == "" {
		return nil
	}
	var out []string
	for _, item := range filepath.SplitList(value) {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

type rawRecord struct {
	Type         string          `json:"type"`
	SessionID    string          `json:"sessionId"`
	CWD          string          `json:"cwd"`
	Timestamp    string          `json:"timestamp"`
	Summary      string          `json:"summary"`
	Title        string          `json:"title"`
	GitBranch    string          `json:"gitBranch"`
	Entrypoint   string          `json:"entrypoint"`
	PromptSource string          `json:"promptSource"`
	IsMeta       bool            `json:"isMeta"`
	Message      json.RawMessage `json:"message"`
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
	defer func() { _ = f.Close() }()
	return parseSession(f)
}

func parseSessionMetadataFile(path string) (session.Session, error) {
	f, err := os.Open(path)
	if err != nil {
		return session.Session{}, err
	}
	defer func() { _ = f.Close() }()
	return parseSessionMetadata(f)
}

func parseSession(r io.Reader) (session.Session, error) {
	return parseSessionMode(r, false)
}

func parseSessionMetadata(r io.Reader) (session.Session, error) {
	return parseSessionMode(r, true)
}

func parseSessionMode(r io.Reader, metadataOnly bool) (session.Session, error) {
	out := session.Session{Metadata: make(map[string]string)}
	var lastUserTitle string

	oversized, err := readClaudeRecords(r, func(line []byte) bool {
		var rec rawRecord
		var msg claudeMessage
		if metadataOnly {
			var ok bool
			rec, msg, ok = decodeClaudeMetadataRecord(line)
			if !ok && json.Unmarshal(line, &rec) == nil {
				msg = parseMessage(rec.Message)
			} else if !ok {
				return true
			}
		} else {
			if json.Unmarshal(line, &rec) != nil {
				return true
			}
			msg = parseMessage(rec.Message)
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
		if rec.Entrypoint != "" {
			out.Metadata["entrypoint"] = strings.TrimSpace(rec.Entrypoint)
		}
		if rec.PromptSource != "" {
			out.Metadata["prompt_source"] = strings.TrimSpace(rec.PromptSource)
		}
		if isNonInteractiveRecord(rec) {
			out.Metadata["interaction_mode"] = "non_interactive"
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
			return true
		}

		if msg.Model != "" {
			out.Metadata["model"] = msg.Model
		}
		if rec.Type == "user" && !rec.IsMeta && msg.Role == "user" {
			if title := cleanTitle(messageText(msg.Content)); title != "" {
				lastUserTitle = title
			}
		}
		return true
	})
	if oversized > 0 {
		markReportEvidencePartial(out.Metadata, oversizedJSONLRecordNote)
	}
	if err != nil {
		return session.Session{}, err
	}
	if out.Title == "" && lastUserTitle != "" {
		out.Title = lastUserTitle
		out.Metadata["title_source"] = "user"
	}
	return out, nil
}

func decodeClaudeMetadataRecord(line []byte) (rawRecord, claudeMessage, bool) {
	var rec rawRecord
	var message []byte
	ok := scanJSONObject(line, func(key string, value []byte) bool {
		switch key {
		case "type":
			return json.Unmarshal(value, &rec.Type) == nil
		case "sessionId":
			return json.Unmarshal(value, &rec.SessionID) == nil
		case "cwd":
			return json.Unmarshal(value, &rec.CWD) == nil
		case "timestamp":
			return json.Unmarshal(value, &rec.Timestamp) == nil
		case "summary":
			return json.Unmarshal(value, &rec.Summary) == nil
		case "title":
			return json.Unmarshal(value, &rec.Title) == nil
		case "gitBranch":
			return json.Unmarshal(value, &rec.GitBranch) == nil
		case "entrypoint":
			return json.Unmarshal(value, &rec.Entrypoint) == nil
		case "promptSource":
			return json.Unmarshal(value, &rec.PromptSource) == nil
		case "isMeta":
			return json.Unmarshal(value, &rec.IsMeta) == nil
		case "message":
			if len(message) != 0 && !json.Valid(message) {
				return false
			}
			message = value
		default:
			return json.Valid(value)
		}
		return true
	})
	if !ok {
		return rawRecord{}, claudeMessage{}, false
	}
	var msg claudeMessage
	if len(message) == 0 {
		return rec, msg, true
	}
	if rec.Type == "user" && !rec.IsMeta {
		if json.Unmarshal(message, &msg) != nil {
			return rawRecord{}, claudeMessage{}, false
		}
		return rec, msg, true
	}
	if !scanJSONObject(message, func(key string, value []byte) bool {
		switch key {
		case "role":
			return json.Unmarshal(value, &msg.Role) == nil
		case "model":
			return json.Unmarshal(value, &msg.Model) == nil
		default:
			return json.Valid(value)
		}
	}) {
		return rawRecord{}, claudeMessage{}, false
	}
	return rec, msg, true
}

func scanJSONObject(data []byte, visit func(key string, value []byte) bool) bool {
	index := skipJSONSpace(data, 0)
	if index >= len(data) || data[index] != '{' {
		return false
	}
	index++
	allowEnd := true
	for {
		index = skipJSONSpace(data, index)
		if index >= len(data) {
			return false
		}
		if data[index] == '}' {
			return allowEnd && skipJSONSpace(data, index+1) == len(data)
		}
		keyStart := index
		keyEnd, ok := scanJSONString(data, index)
		if !ok {
			return false
		}
		var key string
		if json.Unmarshal(data[keyStart:keyEnd], &key) != nil {
			return false
		}
		index = skipJSONSpace(data, keyEnd)
		if index >= len(data) || data[index] != ':' {
			return false
		}
		valueStart := skipJSONSpace(data, index+1)
		valueEnd, ok := scanJSONValue(data, valueStart)
		if !ok || !visit(key, data[valueStart:valueEnd]) {
			return false
		}
		index = skipJSONSpace(data, valueEnd)
		if index >= len(data) {
			return false
		}
		switch data[index] {
		case ',':
			index++
			allowEnd = false
		case '}':
			return skipJSONSpace(data, index+1) == len(data)
		default:
			return false
		}
	}
}

func skipJSONSpace(data []byte, index int) int {
	for index < len(data) {
		switch data[index] {
		case ' ', '\t', '\r', '\n':
			index++
		default:
			return index
		}
	}
	return index
}

func scanJSONString(data []byte, index int) (int, bool) {
	if index >= len(data) || data[index] != '"' {
		return 0, false
	}
	for index++; index < len(data); index++ {
		switch data[index] {
		case '\\':
			index++
			if index >= len(data) {
				return 0, false
			}
		case '"':
			return index + 1, true
		case '\n', '\r':
			return 0, false
		}
	}
	return 0, false
}

func scanJSONValue(data []byte, index int) (int, bool) {
	if index >= len(data) {
		return 0, false
	}
	if data[index] == '"' {
		return scanJSONString(data, index)
	}
	if data[index] == '{' || data[index] == '[' {
		stack := []byte{data[index]}
		for index++; index < len(data); index++ {
			switch data[index] {
			case '"':
				end, ok := scanJSONString(data, index)
				if !ok {
					return 0, false
				}
				index = end - 1
			case '{', '[':
				stack = append(stack, data[index])
			case '}', ']':
				open := stack[len(stack)-1]
				if (open == '{' && data[index] != '}') || (open == '[' && data[index] != ']') {
					return 0, false
				}
				stack = stack[:len(stack)-1]
				if len(stack) == 0 {
					return index + 1, true
				}
			}
		}
		return 0, false
	}
	start := index
	for index < len(data) {
		switch data[index] {
		case ',', '}', ']', ' ', '\t', '\r', '\n':
			if index == start {
				return 0, false
			}
			var value any
			return index, json.Unmarshal(data[start:index], &value) == nil
		default:
			index++
		}
	}
	if index == start {
		return 0, false
	}
	var value any
	return index, json.Unmarshal(data[start:index], &value) == nil
}

func isNonInteractiveRecord(rec rawRecord) bool {
	// Interactive clients can forward individual prompts through the SDK, so
	// promptSource=sdk alone does not make the whole session automated. The
	// sdk-cli entrypoint identifies Claude -p/SDK sessions without hiding later
	// user-typed work stored in interactive CLI or VS Code sessions.
	return rec.Entrypoint == "sdk-cli"
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

func readUserPreviews(path string, opts session.PreviewOptions) ([]session.MessagePreview, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = f.Close() }()

	var messages []session.MessagePreview
	oversized, err := readClaudeRecords(f, func(line []byte) bool {
		var rec rawRecord
		if json.Unmarshal(line, &rec) != nil || rec.Type != "user" || rec.IsMeta {
			return true
		}
		msg := parseMessage(rec.Message)
		if msg.Role != "user" {
			return true
		}
		if text := cleanTitle(messageText(msg.Content)); text != "" {
			messages = append(messages, session.MessagePreview{
				Text:   text,
				At:     parseTime(rec.Timestamp),
				Source: "claude:user",
			})
		}
		return true
	})
	return session.SelectMessagePreviews(messages, opts), oversized, err
}

func readClaudeRecords(r io.Reader, visit func([]byte) bool) (int, error) {
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
			if recovered, timestamped, ok := recoverOversizedClaudeUserRecord(record); ok {
				stopped = !visit(recovered)
				if !timestamped {
					evidenceRisk++
				}
				return
			}
			if oversizedClaudeCouldContainUserEvidence(record.Prefix) {
				evidenceRisk++
			}
		},
	)
	return evidenceRisk, err
}

func recoverOversizedClaudeUserRecord(
	record jsonlrecords.OversizedRecord,
) ([]byte, bool, bool) {
	compact := jsonlrecords.Compact(record.Prefix)
	if !bytes.Contains(compact, []byte(`"type":"user"`)) ||
		!bytes.Contains(compact, []byte(`"role":"user"`)) {
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

	sessionID, _ := jsonlrecords.CompleteStringField(record.Prefix, "sessionId")
	cwd, _ := jsonlrecords.CompleteStringField(record.Prefix, "cwd")
	timestamp, _ := jsonlrecords.CompleteStringField(record.Prefix, "timestamp")
	content, _ := json.Marshal(text)
	message, _ := json.Marshal(claudeMessage{Role: "user", Content: content})
	line, err := json.Marshal(rawRecord{
		Type:      "user",
		SessionID: sessionID,
		CWD:       cwd,
		Timestamp: timestamp,
		Message:   message,
	})
	if err != nil {
		return nil, false, false
	}
	return line, timestamp != "", true
}

func oversizedClaudeCouldContainUserEvidence(prefix []byte) bool {
	compact := jsonlrecords.Compact(prefix)
	if bytes.Contains(compact, []byte(`"role":"user"`)) ||
		bytes.Contains(compact, []byte(`"type":"user"`)) {
		return true
	}
	if bytes.Contains(compact, []byte(`"role":"assistant"`)) ||
		bytes.Contains(compact, []byte(`"type":"assistant"`)) {
		return false
	}
	return true
}

func markReportEvidencePartial(metadata map[string]string, note string) {
	metadata[session.MetadataReportEvidenceStatus] = session.ReportEvidencePartial
	metadata[session.MetadataReportEvidenceNote] = note
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
		"This session is being continued from a previous conversation that ran out of context.",
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
