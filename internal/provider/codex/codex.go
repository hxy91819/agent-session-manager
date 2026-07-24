package codex

import (
	"bufio"
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
	"github.com/hxy91819/agent-session-manager/internal/session"
	"github.com/hxy91819/agent-session-manager/internal/sessioncache"
)

const (
	Name                     = "codex"
	maxJSONLRecordBytes      = 8 * 1024 * 1024
	oversizedJSONLRecordNote = "one or more Codex JSONL records exceeded the per-record safety limit and were skipped"
	previewReadErrorNote     = "the Codex rollout could not be read completely while collecting report evidence"
)

type Provider struct {
	Home      string
	CachePath string
	Profile   string
}

func New(home string) Provider {
	return Provider{Home: home}
}

func NewWithProfile(home, profile string) Provider {
	return Provider{Home: home, Profile: profile}
}

func (p Provider) Name() string {
	return Name
}

func (p Provider) Discover(opts session.DiscoverOptions) ([]session.Session, error) {
	homes, err := p.homes()
	if err != nil {
		return nil, err
	}
	files, err := collectHomeJSONL(homes, opts)
	if err != nil {
		return nil, err
	}

	cachePath := p.cachePath()
	cache := sessioncache.Load(cachePath)
	keep := make(map[string]struct{}, len(files))
	cwdChecker := cwdstatus.NewChecker()
	seen := make(map[string]struct{}, len(files))
	histories := make(map[string]map[string]string, len(homes))
	threadNames := make(map[string]map[string]string, len(homes))
	for _, home := range homes {
		histories[home] = nonNilTitleMap(readHistoryTitles(filepath.Join(home, "history.jsonl")))
		threadNames[home] = nonNilTitleMap(readSessionIndexTitles(filepath.Join(home, "session_index.jsonl")))
	}
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
			var err error
			s, err = parseSessionFile(file.Path)
			if err != nil || s.ID == "" || s.CWD == "" {
				continue
			}
			cache.Put(id, s)
		}
		if s.ID == "" || s.CWD == "" {
			continue
		}
		if s.Metadata == nil {
			s.Metadata = make(map[string]string)
		}
		keep[sessioncache.Key(Name, file.Path)] = struct{}{}
		if _, ok := seen[s.ID]; ok {
			continue
		}
		// Extra homes can contain synchronized copies of the same Codex thread.
		// Resume only needs the stable session ID, so the newest rollout file is
		// the least surprising representation.
		seen[s.ID] = struct{}{}
		s.Provider = Name
		s.Path = file.Path
		s.UpdatedAt = file.ModTime
		if s.CreatedAt.IsZero() {
			s.CreatedAt = file.ModTime
		}
		s.Metadata["source_home"] = file.Home
		cwdChecker.Mark(&s)
		if title := titleForID(s.ID, file.Home, homes, threadNames); title != "" {
			s.Title = title
			s.Metadata["title_source"] = "session_index"
		} else if title := titleForID(s.ID, file.Home, homes, histories); title != "" {
			s.Title = title
			s.Metadata["title_source"] = "history"
		}
		if opts.Preview.Enabled() && s.Metadata[session.MetadataParentThreadID] == "" {
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

func (p Provider) homes() ([]string, error) {
	if p.Home != "" {
		return []string{p.Home}, nil
	}
	home := os.Getenv("CODEX_HOME")
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		home = filepath.Join(userHome, ".codex")
	}
	return append([]string{home}, splitHomeList(os.Getenv("ASM_CODEX_EXTRA_HOMES"))...), nil
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
	args := []string{"codex", "resume"}
	if p.Profile != "" {
		args = append(args, "--profile", p.Profile)
	}
	args = append(args, s.ID)
	return session.ExecSpec{
		Dir:  s.CWD,
		Args: args,
	}
}

func (p Provider) NewCommand(cwd string) session.ExecSpec {
	args := []string{"codex"}
	if p.Profile != "" {
		args = append(args, "--profile", p.Profile)
	}
	return session.ExecSpec{
		Dir:  cwd,
		Args: args,
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
		homeFiles, err := collectJSONL(home, filepath.Join(home, "sessions"), opts)
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
			// Do not prune sessions/YYYY/MM/DD directories by date: Codex stores
			// long-lived threads under their creation day while the rollout file
			// can keep receiving new writes much later. File mtime is the source
			// of truth for activity-window filtering.
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

func nonNilTitleMap(titles map[string]string) map[string]string {
	if titles != nil {
		return titles
	}
	return map[string]string{}
}

func titleForID(id, preferredHome string, homes []string, titlesByHome map[string]map[string]string) string {
	if title := titlesByHome[preferredHome][id]; title != "" {
		return title
	}
	for _, home := range homes {
		if home == preferredHome {
			continue
		}
		if title := titlesByHome[home][id]; title != "" {
			return title
		}
	}
	return ""
}

type rawRecord struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type sessionMeta struct {
	ID             string `json:"id"`
	ParentThreadID string `json:"parent_thread_id"`
	Timestamp      string `json:"timestamp"`
	CWD            string `json:"cwd"`
}

type turnContext struct {
	CWD   string `json:"cwd"`
	Model string `json:"model"`
}

type responseMessage struct {
	Type    string           `json:"type"`
	Role    string           `json:"role"`
	Content []messageContent `json:"content"`
}

type messageContent struct {
	Type      string `json:"type"`
	Text      string `json:"text"`
	InputText string `json:"input_text"`
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
	var out session.Session
	out.Metadata = make(map[string]string)
	haveSessionMeta := false

	oversized, err := readJSONLRecords(r, maxJSONLRecordBytes, func(line []byte) bool {
		var rec rawRecord
		if json.Unmarshal(line, &rec) != nil {
			return true
		}
		switch rec.Type {
		case "session_meta":
			if haveSessionMeta {
				// Repeated metadata for the child is still child-owned. Only the
				// direct parent's identity marks the inherited-history boundary.
				if parentID := out.Metadata[session.MetadataParentThreadID]; parentID != "" {
					var meta sessionMeta
					if json.Unmarshal(rec.Payload, &meta) == nil && meta.ID == parentID {
						return false
					}
				}
				return true
			}
			var meta sessionMeta
			if json.Unmarshal(rec.Payload, &meta) == nil && meta.ID != "" {
				haveSessionMeta = true
				out.ID = meta.ID
				out.CWD = meta.CWD
				if t := parseTime(meta.Timestamp); !t.IsZero() {
					out.CreatedAt = t
				}
				if meta.ParentThreadID != "" {
					out.Metadata[session.MetadataParentThreadID] = meta.ParentThreadID
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
		case "response_item":
			var msg responseMessage
			if json.Unmarshal(rec.Payload, &msg) == nil && msg.Type == "message" && msg.Role == "user" {
				if title := titleFromMessageContent(msg.Content); title != "" {
					out.Title = title
					out.Metadata["title_source"] = "rollout"
				}
			}
		}
		return true
	})
	if oversized > 0 {
		markReportEvidencePartial(out.Metadata, oversizedJSONLRecordNote)
	}
	return out, err
}

func titleFromMessageContent(content []messageContent) string {
	var parts []string
	for _, item := range content {
		if item.Type != "" && item.Type != "input_text" {
			continue
		}
		text := item.Text
		if text == "" {
			text = item.InputText
		}
		text = strings.TrimSpace(text)
		if text != "" {
			parts = append(parts, text)
		}
	}
	return cleanUserTitle(strings.Join(parts, "\n"))
}

func readUserPreviews(path string, opts session.PreviewOptions) ([]session.MessagePreview, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = f.Close() }()

	var messages []session.MessagePreview
	oversized, err := readJSONLRecords(f, maxJSONLRecordBytes, func(line []byte) bool {
		var rec rawRecord
		if json.Unmarshal(line, &rec) != nil || rec.Type != "response_item" {
			return true
		}
		var msg responseMessage
		if json.Unmarshal(rec.Payload, &msg) != nil || msg.Type != "message" || msg.Role != "user" {
			return true
		}
		if text := titleFromMessageContent(msg.Content); text != "" {
			messages = append(messages, session.MessagePreview{
				Text:   text,
				At:     parseTime(rec.Timestamp),
				Source: "codex:response_item",
			})
		}
		return true
	})
	return session.SelectMessagePreviews(messages, opts), oversized, err
}

func readJSONLRecords(r io.Reader, maxRecordBytes int, visit func([]byte) bool) (int, error) {
	reader := bufio.NewReader(r)
	oversizedRecords := 0
	for {
		record, oversized, err := readBoundedJSONLRecord(reader, maxRecordBytes)
		if errors.Is(err, io.EOF) {
			return oversizedRecords, nil
		}
		if err != nil {
			return oversizedRecords, err
		}
		if oversized {
			oversizedRecords++
			continue
		}
		record = bytes.TrimSpace(record)
		if len(record) != 0 {
			if !visit(record) {
				return oversizedRecords, nil
			}
		}
	}
}

func readBoundedJSONLRecord(reader *bufio.Reader, maxRecordBytes int) ([]byte, bool, error) {
	var record []byte
	oversized := false
	for {
		fragment, isPrefix, err := reader.ReadLine()
		if err != nil {
			if errors.Is(err, io.EOF) && (len(record) > 0 || oversized) {
				return record, oversized, nil
			}
			return nil, false, err
		}
		if !oversized {
			if len(fragment) > maxRecordBytes-len(record) {
				record = nil
				oversized = true
			} else {
				record = append(record, fragment...)
			}
		}
		if !isPrefix {
			return record, oversized, nil
		}
	}
}

func markReportEvidencePartial(metadata map[string]string, note string) {
	metadata[session.MetadataReportEvidenceStatus] = session.ReportEvidencePartial
	metadata[session.MetadataReportEvidenceNote] = note
}

func cleanUserTitle(text string) string {
	text = strings.TrimSpace(text)
	if text == "" || isInjectedUserContext(text) {
		return ""
	}
	return collapseWhitespace(text)
}

func isInjectedUserContext(text string) bool {
	prefixes := []string{
		"# AGENTS.md instructions",
		"<environment_context",
		"<codex_internal_context",
		"<turn_aborted",
		"<permissions",
		"<collaboration_mode",
		"<skills_instructions",
		"<skill",
		"<user_action",
		"The following is the Codex agent history added since your last approval assessment.",
		"The following is the Codex agent history whose request action you are assessing.",
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	return false
}

func collapseWhitespace(text string) string {
	return strings.Join(strings.Fields(text), " ")
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
	defer func() { _ = f.Close() }()

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

type sessionIndexRecord struct {
	ID         string `json:"id"`
	ThreadName string `json:"thread_name"`
}

func readSessionIndexTitles(path string) map[string]string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()

	titles := make(map[string]string)
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		var rec sessionIndexRecord
		if json.Unmarshal(scanner.Bytes(), &rec) != nil {
			continue
		}
		title := strings.TrimSpace(rec.ThreadName)
		if rec.ID == "" || title == "" {
			continue
		}
		titles[rec.ID] = title
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
