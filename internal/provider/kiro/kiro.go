package kiro

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hxy91819/agent-session-manager/internal/cwdstatus"
	"github.com/hxy91819/agent-session-manager/internal/jsonlrecords"
	"github.com/hxy91819/agent-session-manager/internal/session"
	"github.com/hxy91819/agent-session-manager/internal/sessioncache"
)

const (
	Name                     = "kiro"
	maxJSONLRecordBytes      = 8 * 1024 * 1024
	oversizedRecordEdgeBytes = 64 * 1024
	oversizedRecordNote      = "one or more oversized Kiro CLI records may contain user evidence that bounded parsing could not recover"
	transcriptReadErrorNote  = "the Kiro CLI transcript could not be read completely while collecting report evidence"
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
	files, err := collectSessionFiles(filepath.Join(home, "sessions", "cli"), opts)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	if sessioncache.SkipLoadForEmptyDiscovery(opts, len(files)) {
		return []session.Session{}, nil
	}

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
			rec, readErr := readSession(file.Path)
			if readErr != nil || strings.TrimSpace(rec.ID) == "" {
				continue
			}
			s = sessionFromRecord(file, rec)
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

		if s.Title == "" {
			if title, readErr := firstPromptTitle(file.TranscriptPath); readErr == nil && title != "" {
				s.Title = session.NormalizeTitle(title)
				s.Metadata["title_source"] = "prompt"
			}
		}
		if opts.Preview.Enabled() {
			previews, oversized, readErr := readUserPreviews(file.TranscriptPath, opts.Preview)
			s.Previews = previews
			if oversized > 0 {
				s.Metadata[session.MetadataReportEvidenceStatus] = session.ReportEvidencePartial
				s.Metadata[session.MetadataReportEvidenceNote] = oversizedRecordNote
			}
			if readErr != nil {
				s.Metadata[session.MetadataReportEvidenceStatus] = session.ReportEvidencePartial
				s.Metadata[session.MetadataReportEvidenceNote] = transcriptReadErrorNote
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

func (p Provider) ResumeCommand(s session.Session) session.ExecSpec {
	return session.ExecSpec{
		Dir:  s.CWD,
		Args: []string{"kiro-cli", "chat", "--resume-id", s.ID},
	}
}

func (p Provider) NewCommand(cwd string) session.ExecSpec {
	return session.ExecSpec{
		Dir:  cwd,
		Args: []string{"kiro-cli"},
	}
}

func (p Provider) home() (string, error) {
	if p.Home != "" {
		return p.Home, nil
	}
	if home := os.Getenv("KIRO_HOME"); home != "" {
		return home, nil
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(userHome, ".kiro"), nil
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
	Path           string
	TranscriptPath string
	Size           int64
	ModTime        time.Time
}

func collectSessionFiles(root string, opts session.DiscoverOptions) ([]fileInfo, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}

	files := make([]fileInfo, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(root, entry.Name())
		info, err := entry.Info()
		if err != nil || info.IsDir() {
			continue
		}
		if !opts.Since.IsZero() && info.ModTime().Before(opts.Since) {
			continue
		}
		files = append(files, fileInfo{
			Path:           path,
			TranscriptPath: strings.TrimSuffix(path, filepath.Ext(path)) + ".jsonl",
			Size:           info.Size(),
			ModTime:        info.ModTime(),
		})
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

type sessionRecord struct {
	ID                   string       `json:"session_id"`
	CWD                  string       `json:"cwd"`
	CreatedAt            string       `json:"created_at"`
	UpdatedAt            string       `json:"updated_at"`
	Title                string       `json:"title"`
	ParentSessionID      string       `json:"parent_session_id"`
	SessionCreatedReason string       `json:"session_created_reason"`
	SessionState         sessionState `json:"session_state"`
}

type sessionState struct {
	AgentName string `json:"agent_name"`
}

type messageRecord struct {
	Kind string     `json:"kind"`
	Data promptData `json:"data"`
}

type promptData struct {
	Content []contentBlock `json:"content"`
	Meta    promptMeta     `json:"meta"`
}

type promptMeta struct {
	Timestamp json.RawMessage `json:"timestamp"`
}

type contentBlock struct {
	Kind string `json:"kind"`
	Data string `json:"data"`
}

func readSession(path string) (sessionRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		return sessionRecord{}, err
	}
	defer func() { _ = f.Close() }()

	var rec sessionRecord
	if err := json.NewDecoder(f).Decode(&rec); err != nil {
		return sessionRecord{}, err
	}
	return rec, nil
}

func sessionFromRecord(file fileInfo, rec sessionRecord) session.Session {
	s := session.Session{
		ID:        strings.TrimSpace(rec.ID),
		Provider:  Name,
		CWD:       strings.TrimSpace(rec.CWD),
		Title:     cleanTitle(rec.Title),
		CreatedAt: parseTime(rec.CreatedAt),
		UpdatedAt: parseTime(rec.UpdatedAt),
		Path:      file.Path,
		Metadata:  make(map[string]string),
	}
	if s.UpdatedAt.IsZero() {
		s.UpdatedAt = file.ModTime
	}
	if s.CreatedAt.IsZero() {
		s.CreatedAt = s.UpdatedAt
	}
	if s.Title != "" {
		s.Metadata["title_source"] = "session"
	}
	if reason := strings.TrimSpace(rec.SessionCreatedReason); reason != "" {
		s.Metadata["session_created_reason"] = reason
	}
	if parentID := strings.TrimSpace(rec.ParentSessionID); parentID != "" {
		s.Metadata[session.MetadataParentThreadID] = parentID
	}
	if agentName := strings.TrimSpace(rec.SessionState.AgentName); agentName != "" {
		s.Metadata["agent_name"] = agentName
	}
	if !s.UpdatedAt.IsZero() {
		s.Metadata["kiro_updated_at"] = s.UpdatedAt.Format(time.RFC3339Nano)
	}
	return s
}

func firstPromptTitle(path string) (string, error) {
	var title string
	_, err := readRecords(path, func(line []byte) bool {
		var rec messageRecord
		if json.Unmarshal(line, &rec) != nil || rec.Kind != "Prompt" {
			return true
		}
		if candidate := cleanTitle(promptText(rec)); candidate != "" {
			title = candidate
			return false
		}
		return true
	})
	return title, err
}

func readUserPreviews(path string, opts session.PreviewOptions) ([]session.MessagePreview, int, error) {
	var previews []session.MessagePreview
	oversized, err := readRecords(path, func(line []byte) bool {
		var rec messageRecord
		if json.Unmarshal(line, &rec) != nil || rec.Kind != "Prompt" {
			return true
		}
		if text := cleanTitle(promptText(rec)); text != "" {
			previews = append(previews, session.MessagePreview{
				Text:   text,
				At:     parseTimestamp(rec.Data.Meta.Timestamp),
				Source: "kiro:prompt",
			})
		}
		return true
	})
	return session.SelectMessagePreviews(previews, opts), oversized, err
}

func readRecords(path string, visit func([]byte) bool) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()

	evidenceRisk := 0
	_, readErr := jsonlrecords.ReadWithOversized(
		f,
		maxJSONLRecordBytes,
		oversizedRecordEdgeBytes,
		visit,
		func(record jsonlrecords.OversizedRecord) {
			if oversizedKiroCouldContainUserEvidence(record.Prefix) {
				evidenceRisk++
			}
		},
	)
	return evidenceRisk, readErr
}

func oversizedKiroCouldContainUserEvidence(prefix []byte) bool {
	compact := jsonlrecords.Compact(prefix)
	if bytes.Contains(compact, []byte(`"kind":"Prompt"`)) {
		return true
	}
	if bytes.Contains(compact, []byte(`"kind":"AssistantMessage"`)) ||
		bytes.Contains(compact, []byte(`"kind":"ToolResults"`)) ||
		bytes.Contains(compact, []byte(`"kind":"Compaction"`)) ||
		bytes.Contains(compact, []byte(`"kind":"Clear"`)) {
		return false
	}
	return true
}

func promptText(rec messageRecord) string {
	parts := make([]string, 0, len(rec.Data.Content))
	for _, block := range rec.Data.Content {
		if block.Kind != "" && block.Kind != "text" {
			continue
		}
		if strings.TrimSpace(block.Data) != "" {
			parts = append(parts, block.Data)
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
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return t
}

func parseTimestamp(raw json.RawMessage) time.Time {
	if len(raw) == 0 || string(raw) == "null" {
		return time.Time{}
	}
	var number json.Number
	if err := json.Unmarshal(raw, &number); err == nil {
		return parseUnixNumber(number.String())
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		if parsed := parseTime(text); !parsed.IsZero() {
			return parsed
		}
		return parseUnixNumber(text)
	}
	return time.Time{}
}

func parseUnixNumber(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	if integer, err := strconv.ParseInt(value, 10, 64); err == nil {
		if integer > 1_000_000_000_000 {
			return time.UnixMilli(integer).UTC()
		}
		return time.Unix(integer, 0).UTC()
	}
	decimal, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(decimal) || math.IsInf(decimal, 0) {
		return time.Time{}
	}
	if decimal > 1_000_000_000_000 {
		return time.UnixMilli(int64(decimal)).UTC()
	}
	seconds, fraction := math.Modf(decimal)
	return time.Unix(int64(seconds), int64(fraction*1e9)).UTC()
}
