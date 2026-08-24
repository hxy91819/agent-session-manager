// Package pi discovers sessions for the Pi coding agent
// (@earendil-works/pi-coding-agent). Pi stores each session as an append-only
// JSONL transcript under <agent-dir>/sessions/<encoded-cwd>/ where the first
// record is a session header carrying the stable id, cwd, and creation time.
package pi

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
	Name                     = "pi"
	maxJSONLRecordBytes      = 8 * 1024 * 1024
	oversizedRecordEdgeBytes = 64 * 1024
	oversizedRecordNote      = "one or more oversized Pi records may contain user evidence that bounded parsing could not recover"
	transcriptReadErrorNote  = "the Pi transcript could not be read completely while collecting report evidence"
	missingTimestampNote     = "some Pi user messages lacked original timestamps and were excluded from report evidence"
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
	files, err := collectSessionFiles(filepath.Join(home, "sessions"), opts)
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
			parsed, _, readErr := readTranscript(file.Path, nil)
			if readErr != nil || strings.TrimSpace(parsed.header.ID) == "" {
				continue
			}
			s = sessionFromTranscript(file, parsed)
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
			// Preview windows change with every report, so transcripts are
			// re-scanned here instead of caching per-file preview results.
			var previews []session.MessagePreview
			var incomplete bool
			_, oversizedRisk, readErr := readTranscript(file.Path, func(msg transcriptUserMessage) bool {
				if msg.at.IsZero() {
					// File mtimes change on copy and sync; only original record
					// timestamps count as work evidence.
					incomplete = true
					return true
				}
				previews = append(previews, session.MessagePreview{
					Text:   msg.text,
					At:     msg.at,
					Source: "pi:message",
				})
				return true
			})
			s.Previews = session.SelectMessagePreviews(previews, opts.Preview)
			switch {
			case readErr != nil:
				s.Metadata[session.MetadataReportEvidenceStatus] = session.ReportEvidencePartial
				s.Metadata[session.MetadataReportEvidenceNote] = transcriptReadErrorNote
			case oversizedRisk:
				s.Metadata[session.MetadataReportEvidenceStatus] = session.ReportEvidencePartial
				s.Metadata[session.MetadataReportEvidenceNote] = oversizedRecordNote
			case incomplete:
				s.Metadata[session.MetadataReportEvidenceStatus] = session.ReportEvidencePartial
				s.Metadata[session.MetadataReportEvidenceNote] = missingTimestampNote
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
		Args: []string{"pi", "--session", s.ID},
	}
}

func (p Provider) NewCommand(cwd string) session.ExecSpec {
	return session.ExecSpec{
		Dir:  cwd,
		Args: []string{"pi"},
	}
}

// home resolves the Pi agent directory: the --pi-home flag wins, then
// PI_CODING_AGENT_DIR, then ~/.pi/agent (matching Pi's own getAgentDir).
func (p Provider) home() (string, error) {
	if p.Home != "" {
		return p.Home, nil
	}
	if home := os.Getenv("PI_CODING_AGENT_DIR"); home != "" {
		return home, nil
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(userHome, ".pi", "agent"), nil
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

// collectSessionFiles walks <home>/sessions/<encoded-cwd>/*.jsonl. Pi encodes
// the cwd into the directory name, but the authoritative cwd lives in the
// session header record, so the encoding is never decoded here.
func collectSessionFiles(root string, opts session.DiscoverOptions) ([]fileInfo, error) {
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

type sessionHeader struct {
	Type          string `json:"type"`
	ID            string `json:"id"`
	CWD           string `json:"cwd"`
	Timestamp     string `json:"timestamp"`
	ParentSession string `json:"parentSession"`
}

type transcriptRecord struct {
	Type      string         `json:"type"`
	Timestamp string         `json:"timestamp"`
	Name      string         `json:"name"`
	Message   transcriptBody `json:"message"`
}

type transcriptBody struct {
	Role      string          `json:"role"`
	Content   json.RawMessage `json:"content"`
	Timestamp json.RawMessage `json:"timestamp"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type transcriptUserMessage struct {
	text string
	at   time.Time
}

// transcript holds everything extracted from one pass over a session file.
type transcript struct {
	header       sessionHeader
	name         string
	hasName      bool
	firstUserMsg string
	lastActivity time.Time
}

// readTranscript parses one Pi session JSONL file. The first parsed record
// must be the session header, mirroring Pi's own session listing. When visit
// is set it receives every clean user message for preview selection. The
// returned flag reports oversized records that may hide user evidence.
func readTranscript(path string, visit func(transcriptUserMessage) bool) (transcript, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return transcript{}, false, err
	}
	defer func() { _ = f.Close() }()

	var out transcript
	headerSeen := false
	oversizedUserRisk := false
	_, readErr := jsonlrecords.ReadWithOversized(
		f,
		maxJSONLRecordBytes,
		oversizedRecordEdgeBytes,
		func(line []byte) bool {
			if !headerSeen {
				headerSeen = true
				if json.Unmarshal(line, &out.header) != nil || out.header.Type != "session" {
					return false
				}
				return true
			}
			var rec transcriptRecord
			if json.Unmarshal(line, &rec) != nil {
				return true
			}
			switch rec.Type {
			case "session_info":
				// Pi resolves the display name from the latest session_info
				// record; an empty name explicitly clears it.
				out.name = strings.TrimSpace(rec.Name)
				out.hasName = true
			case "message":
				if activity := messageActivityTime(rec); !activity.IsZero() && out.lastActivity.Before(activity) {
					out.lastActivity = activity
				}
				if rec.Message.Role != "user" {
					return true
				}
				text := cleanTitle(extractText(rec.Message.Content))
				if text == "" {
					return true
				}
				if out.firstUserMsg == "" {
					out.firstUserMsg = text
				}
				if visit != nil {
					return visit(transcriptUserMessage{text: text, at: messageTimestamp(rec)})
				}
			}
			return true
		},
		func(record jsonlrecords.OversizedRecord) {
			if oversizedPiCouldContainUserEvidence(record.Prefix) {
				oversizedUserRisk = true
			}
		},
	)
	if !headerSeen || out.header.Type != "session" {
		return transcript{}, false, errors.New("pi session file is missing its session header record")
	}
	return out, oversizedUserRisk, readErr
}

func sessionFromTranscript(file fileInfo, tr transcript) session.Session {
	s := session.Session{
		ID:        strings.TrimSpace(tr.header.ID),
		Provider:  Name,
		CWD:       strings.TrimSpace(tr.header.CWD),
		CreatedAt: parseISOTime(tr.header.Timestamp),
		Path:      file.Path,
		Metadata:  make(map[string]string),
	}
	if tr.hasName {
		s.Title = cleanTitle(tr.name)
	}
	if s.Title != "" {
		s.Metadata["title_source"] = "session_info"
	} else if tr.firstUserMsg != "" {
		// Pi's own session picker shows the first user message when no display
		// name exists, so asm mirrors that choice for recognizable titles.
		s.Title = tr.firstUserMsg
		s.Metadata["title_source"] = "message"
	}
	// Pi derives "modified" from the last user/assistant message, then the
	// header timestamp, then the file mtime.
	switch {
	case !tr.lastActivity.IsZero():
		s.UpdatedAt = tr.lastActivity
	case !s.CreatedAt.IsZero():
		s.UpdatedAt = s.CreatedAt
	default:
		s.UpdatedAt = file.ModTime
	}
	if s.CreatedAt.IsZero() {
		s.CreatedAt = s.UpdatedAt
	}
	if parent := strings.TrimSpace(tr.header.ParentSession); parent != "" {
		s.Metadata["parent_session"] = parent
	}
	return s
}

// extractText joins text content exactly like Pi's extractTextContent: plain
// string content is used as-is, block arrays keep only type=text parts.
func extractText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var plain string
	if err := json.Unmarshal(raw, &plain); err == nil {
		return plain
	}
	var blocks []contentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return ""
	}
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if block.Type != "" && block.Type != "text" {
			continue
		}
		if strings.TrimSpace(block.Text) != "" {
			parts = append(parts, block.Text)
		}
	}
	return strings.Join(parts, " ")
}

// messageActivityTime mirrors Pi's getMessageActivityTime: only user and
// assistant messages count as activity.
func messageActivityTime(rec transcriptRecord) time.Time {
	if rec.Message.Role != "user" && rec.Message.Role != "assistant" {
		return time.Time{}
	}
	return messageTimestamp(rec)
}

// messageTimestamp prefers the numeric per-message timestamp (Unix
// milliseconds) and falls back to the record timestamp, like Pi itself.
func messageTimestamp(rec transcriptRecord) time.Time {
	if ts := parseUnixNumber(rec.Message.Timestamp); !ts.IsZero() {
		return ts
	}
	return parseISOTime(rec.Timestamp)
}

func oversizedPiCouldContainUserEvidence(prefix []byte) bool {
	compact := jsonlrecords.Compact(prefix)
	if bytes.Contains(compact, []byte(`"role":"user"`)) {
		return true
	}
	for _, marker := range []string{
		`"role":"assistant"`,
		`"role":"toolResult"`,
		`"type":"session_info"`,
		`"type":"model_change"`,
		`"type":"thinking_level_change"`,
	} {
		if bytes.Contains(compact, []byte(marker)) {
			return false
		}
	}
	return true
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

func parseISOTime(value string) time.Time {
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

func parseUnixNumber(raw json.RawMessage) time.Time {
	if len(raw) == 0 || string(raw) == "null" {
		return time.Time{}
	}
	var number json.Number
	if err := json.Unmarshal(raw, &number); err != nil {
		return time.Time{}
	}
	value := number.String()
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
