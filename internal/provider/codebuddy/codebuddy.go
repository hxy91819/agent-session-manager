package codebuddy

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
)

const (
	Name                     = "codebuddy"
	maxJSONLRecordBytes      = 8 * 1024 * 1024
	oversizedJSONLRecordNote = "one or more oversized CodeBuddy records may contain user evidence that bounded head/tail recovery could not identify"
	previewReadErrorNote     = "the CodeBuddy transcript could not be read completely while collecting report evidence"
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
	files, err := collectJSONL(filepath.Join(home, "projects"), opts)
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
			if err != nil || s.ID == "" {
				continue
			}
			s = cache.Put(id, s)
		}
		if s.ID == "" {
			continue
		}
		if s.Metadata == nil {
			s.Metadata = make(map[string]string)
		}
		// Coverage is dynamic report state, not stable session identity. Clear
		// cached warnings before re-reading previews with the current parser.
		delete(s.Metadata, session.MetadataReportEvidenceStatus)
		delete(s.Metadata, session.MetadataReportEvidenceNote)
		keep[sessioncache.Key(Name, file.Path)] = struct{}{}
		s.Provider = Name
		s.Path = file.Path
		s.UpdatedAt = file.ModTime
		if s.CreatedAt.IsZero() {
			s.CreatedAt = file.ModTime
		}
		s.Metadata["source_home"] = home
		if s.CWD == "" {
			s.Metadata["cwd_missing"] = "true"
		} else {
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
	return session.ExecSpec{
		Dir:  s.CWD,
		Args: []string{"codebuddy", "--resume", s.ID},
	}
}

func (p Provider) NewCommand(cwd string) session.ExecSpec {
	return session.ExecSpec{
		Dir:  cwd,
		Args: []string{"codebuddy"},
	}
}

func (p Provider) home() (string, error) {
	if p.Home != "" {
		return p.Home, nil
	}
	if home := os.Getenv("CODEBUDDY_HOME"); home != "" {
		return home, nil
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(userHome, ".codebuddy"), nil
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

type rawRecord struct {
	Type      string          `json:"type"`
	Role      string          `json:"role"`
	SessionID string          `json:"sessionId"`
	CWD       string          `json:"cwd"`
	Timestamp flexibleTime    `json:"timestamp"`
	AITitle   string          `json:"ai-title"`
	Summary   string          `json:"summary"`
	Model     string          `json:"model"`
	Content   json.RawMessage `json:"content"`
	Message   json.RawMessage `json:"message"`
	Provider  providerData    `json:"providerData"`
}

type providerData struct {
	Agent string `json:"agent"`
}

type codebuddyMessage struct {
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

func parseSession(r io.Reader) (session.Session, error) {
	out := session.Session{Metadata: make(map[string]string)}
	var aiTitle string
	var summaryTitle string
	var lastUserTitle string

	oversized, err := readCodeBuddyRecords(r, func(line []byte) bool {
		var rec rawRecord
		if json.Unmarshal(line, &rec) != nil {
			return true
		}
		if rec.SessionID != "" {
			out.ID = strings.TrimSpace(rec.SessionID)
		}
		if rec.CWD != "" {
			out.CWD = strings.TrimSpace(rec.CWD)
		}
		if rec.Model != "" {
			out.Metadata["model"] = strings.TrimSpace(rec.Model)
		}
		if rec.Provider.Agent != "" {
			agent := strings.TrimSpace(rec.Provider.Agent)
			out.Metadata["agent"] = agent
		}
		if t := rec.Timestamp.Time(); !t.IsZero() {
			if out.CreatedAt.IsZero() || t.Before(out.CreatedAt) {
				out.CreatedAt = t
			}
			if t.After(out.UpdatedAt) {
				out.UpdatedAt = t
			}
		}
		if title := cleanTitle(rec.AITitle); title != "" {
			aiTitle = title
		}
		if title := cleanTitle(rec.Summary); title != "" {
			summaryTitle = title
		}

		msg := parseMessage(rec)
		if msg.Model != "" {
			out.Metadata["model"] = msg.Model
		}
		if messageRole(rec, msg) == "user" {
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
	switch {
	case aiTitle != "":
		out.Title = aiTitle
		out.Metadata["title_source"] = "ai-title"
	case summaryTitle != "":
		out.Title = summaryTitle
		out.Metadata["title_source"] = "summary"
	case lastUserTitle != "":
		out.Title = lastUserTitle
		out.Metadata["title_source"] = "user"
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
	oversized, err := readCodeBuddyRecords(f, func(line []byte) bool {
		var rec rawRecord
		if json.Unmarshal(line, &rec) != nil {
			return true
		}
		msg := parseMessage(rec)
		if messageRole(rec, msg) != "user" {
			return true
		}
		if text := cleanTitle(messageText(msg.Content)); text != "" {
			messages = append(messages, session.MessagePreview{
				Text:   text,
				At:     rec.Timestamp.Time(),
				Source: "codebuddy:user",
			})
		}
		return true
	})
	return session.SelectMessagePreviews(messages, opts), oversized, err
}

func readCodeBuddyRecords(r io.Reader, visit func([]byte) bool) (int, error) {
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
			if recovered, timestamped, ok := recoverOversizedCodeBuddyUserRecord(record); ok {
				stopped = !visit(recovered)
				if !timestamped {
					evidenceRisk++
				}
				return
			}
			if oversizedCodeBuddyCouldContainUserEvidence(record.Prefix) {
				evidenceRisk++
			}
		},
	)
	return evidenceRisk, err
}

func recoverOversizedCodeBuddyUserRecord(
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

	sessionID, _ := jsonlrecords.CompleteStringField(record.Prefix, "sessionId")
	cwd, _ := jsonlrecords.CompleteStringField(record.Prefix, "cwd")
	var timestamp flexibleTime
	timestampRaw, timestamped := jsonlrecords.CompleteScalarField(
		record.Prefix,
		"timestamp",
	)
	if timestamped && json.Unmarshal(timestampRaw, &timestamp) != nil {
		timestamped = false
	}
	synthetic := map[string]any{
		"sessionId": sessionID,
		"cwd":       cwd,
		"role":      "user",
		"content":   text,
	}
	if timestamped {
		synthetic["timestamp"] = json.RawMessage(timestampRaw)
	}
	line, err := json.Marshal(synthetic)
	if err != nil {
		return nil, false, false
	}
	return line, timestamped, true
}

func oversizedCodeBuddyCouldContainUserEvidence(prefix []byte) bool {
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

func parseMessage(rec rawRecord) codebuddyMessage {
	if len(rec.Message) != 0 {
		var msg codebuddyMessage
		if json.Unmarshal(rec.Message, &msg) == nil {
			return msg
		}
	}
	return codebuddyMessage{
		Role:    rec.Role,
		Model:   rec.Model,
		Content: rec.Content,
	}
}

func messageRole(rec rawRecord, msg codebuddyMessage) string {
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

type flexibleTime struct {
	value time.Time
}

func (t flexibleTime) Time() time.Time {
	return t.value
}

func (t *flexibleTime) UnmarshalJSON(data []byte) error {
	var text string
	if json.Unmarshal(data, &text) == nil {
		t.value = parseTime(text)
		return nil
	}
	var number json.Number
	if json.Unmarshal(data, &number) == nil {
		// CodeBuddy currently emits millisecond epochs as JSON integers, but
		// accepting decimal number tokens keeps the parser tolerant of equivalent
		// encoders without broadening the provider contract beyond epoch millis.
		parsed, err := number.Float64()
		if err == nil {
			t.value = time.UnixMilli(int64(parsed))
		}
	}
	// A malformed timestamp must not discard an otherwise usable transcript
	// record; unknown time is safer than losing its title and evidence.
	return nil
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
