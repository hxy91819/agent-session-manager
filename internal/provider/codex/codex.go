package codex

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hxy91819/agent-session-manager/internal/cwdstatus"
	"github.com/hxy91819/agent-session-manager/internal/jsonlrecords"
	"github.com/hxy91819/agent-session-manager/internal/session"
	"github.com/hxy91819/agent-session-manager/internal/sessioncache"
)

const (
	Name                     = "codex"
	maxJSONLRecordBytes      = 8 * 1024 * 1024
	oversizedJSONLRecordNote = "one or more oversized Codex records may contain user evidence that bounded head/tail recovery could not identify"
	previewReadErrorNote     = "the Codex rollout could not be read completely while collecting report evidence"
	desktopRequestMarker     = "## My request for Codex:"
	oversizedRecordEdgeBytes = 64 * 1024
	oversizedTextEdgeBytes   = 4 * 1024
	incrementalHashEdgeBytes = 64 * 1024
	minimumIncrementalBytes  = 1024 * 1024
	defaultParseWorkers      = 8
	metadataParseCacheKey    = "_asm_codex_parse_mode"
	metadataParseCacheValue  = "metadata"
)

type Provider struct {
	Home      string
	CachePath string
	Profile   string

	parseWorkers int
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
	if sessioncache.SkipLoadForEmptyDiscovery(opts, len(files)) {
		return []session.Session{}, nil
	}

	cachePath := p.cachePath()
	cache := sessioncache.Load(cachePath)
	titleCachePath := dynamicTitleCachePath(cachePath)
	titleCache := loadDynamicTitleCache(titleCachePath)
	keep := make(map[string]struct{}, len(files))
	cwdChecker := cwdstatus.NewChecker()
	seen := make(map[string]struct{}, len(files))
	histories := make(map[string]map[string]string, len(homes))
	threadNames := make(map[string]map[string]string, len(homes))
	for _, home := range homes {
		histories[home] = nonNilTitleMap(titleCache.read(filepath.Join(home, "history.jsonl"), dynamicTitleHistory))
		threadNames[home] = nonNilTitleMap(titleCache.read(filepath.Join(home, "session_index.jsonl"), dynamicTitleSessionIndex))
	}
	_ = titleCache.save(titleCachePath)
	parsed := make([]parsedFile, len(files))
	misses := make([]cacheMiss, 0, len(files))
	metadataOnly := !opts.Preview.Enabled()
	for i, file := range files {
		id := sessioncache.FileIdentity{
			Provider: Name,
			Path:     file.Path,
			Size:     file.Size,
			ModTime:  file.ModTime,
		}
		s, ok := cache.Get(id)
		if ok && (!opts.Preview.Enabled() || s.Metadata[metadataParseCacheKey] != metadataParseCacheValue) {
			parsed[i] = parsedFile{id: id, session: s, available: true}
			continue
		}
		miss := cacheMiss{index: i, file: file, id: id, metadataOnly: metadataOnly}
		miss.previousID, miss.previous, miss.previousState, miss.hasPrevious = cache.GetLatest(Name, file.Path)
		if opts.Preview.Enabled() && miss.previous.Metadata[metadataParseCacheKey] == metadataParseCacheValue {
			miss.hasPrevious = false
		}
		misses = append(misses, miss)
	}
	parseCacheMisses(misses, p.workerCount(), parsed)

	sessions := make([]session.Session, 0, len(files))
	for i, file := range files {
		result := parsed[i]
		if !result.available {
			continue
		}
		s := result.session
		if result.cacheMiss {
			s = cache.PutWithState(result.id, s, result.state)
		}
		delete(s.Metadata, metadataParseCacheKey)
		if s.ID == "" || s.CWD == "" {
			continue
		}
		if s.Metadata == nil {
			s.Metadata = make(map[string]string)
		}
		// Evidence coverage depends on the current preview window and parser
		// rules. Never reuse an older cached warning after the underlying
		// oversized record has been classified as non-user tool output.
		delete(s.Metadata, session.MetadataReportEvidenceStatus)
		delete(s.Metadata, session.MetadataReportEvidenceNote)
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

type cacheMiss struct {
	index         int
	file          fileInfo
	id            sessioncache.FileIdentity
	previousID    sessioncache.FileIdentity
	previous      session.Session
	previousState []byte
	hasPrevious   bool
	metadataOnly  bool
}

type parsedFile struct {
	id        sessioncache.FileIdentity
	session   session.Session
	state     []byte
	available bool
	cacheMiss bool
}

func (p Provider) workerCount() int {
	if p.parseWorkers > 0 {
		return p.parseWorkers
	}
	return defaultParseWorkers
}

func parseCacheMisses(misses []cacheMiss, workers int, parsed []parsedFile) {
	if len(misses) == 0 {
		return
	}
	workers = max(1, min(workers, len(misses)))
	jobs := make(chan int)
	var wait sync.WaitGroup
	wait.Add(workers)
	for range workers {
		go func() {
			defer wait.Done()
			for index := range jobs {
				miss := misses[index]
				s, state, ok := session.Session{}, []byte(nil), false
				if miss.hasPrevious {
					s, state, ok = parseSessionFileIncremental(
						miss.file.Path,
						miss.id,
						miss.previousID,
						miss.previous,
						miss.previousState,
						miss.metadataOnly,
					)
				}
				var err error
				if !ok {
					s, state, err = parseSessionFileWithState(miss.file.Path, miss.id.Size, miss.metadataOnly)
				}
				if err != nil || s.ID == "" || s.CWD == "" {
					continue
				}
				if miss.metadataOnly {
					if s.Metadata == nil {
						s.Metadata = make(map[string]string)
					}
					s.Metadata[metadataParseCacheKey] = metadataParseCacheValue
				} else {
					delete(s.Metadata, metadataParseCacheKey)
				}
				parsed[miss.index] = parsedFile{
					id:        miss.id,
					session:   s,
					state:     state,
					available: true,
					cacheMiss: true,
				}
			}
		}()
	}
	for index := range misses {
		jobs <- index
	}
	close(jobs)
	wait.Wait()
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
	ID             string          `json:"id"`
	ParentThreadID string          `json:"parent_thread_id"`
	Timestamp      string          `json:"timestamp"`
	CWD            string          `json:"cwd"`
	Source         json.RawMessage `json:"source"`
	Originator     string          `json:"originator"`
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

type incrementalParseState struct {
	Offset     int64  `json:"offset"`
	HeadSHA256 string `json:"head_sha256"`
	TailSHA256 string `json:"tail_sha256"`
}

func parseSessionFileWithState(path string, expectedSize int64, metadataOnly bool) (session.Session, []byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return session.Session{}, nil, err
	}
	defer func() { _ = f.Close() }()

	out, stopped, err := parseSessionIntoMode(f, session.Session{}, metadataOnly)
	if err != nil {
		return out, nil, err
	}
	if expectedSize < minimumIncrementalBytes || stopped ||
		!readerAtEnd(f, expectedSize) || !hasTrailingNewline(f, expectedSize) {
		return out, nil, nil
	}
	head, tail, ok := prefixBoundaryHashes(f, expectedSize)
	if !ok {
		return out, nil, nil
	}
	state, err := json.Marshal(incrementalParseState{
		Offset:     expectedSize,
		HeadSHA256: head,
		TailSHA256: tail,
	})
	return out, state, err
}

func parseSessionFileIncremental(
	path string,
	currentID sessioncache.FileIdentity,
	previousID sessioncache.FileIdentity,
	previous session.Session,
	rawState []byte,
	metadataOnly bool,
) (session.Session, []byte, bool) {
	var state incrementalParseState
	if json.Unmarshal(rawState, &state) != nil ||
		state.Offset <= 0 || state.Offset != previousID.Size || currentID.Size <= state.Offset {
		return session.Session{}, nil, false
	}

	f, err := os.Open(path)
	if err != nil {
		return session.Session{}, nil, false
	}
	defer func() { _ = f.Close() }()
	head, tail, ok := prefixBoundaryHashes(f, state.Offset)
	if !ok || head != state.HeadSHA256 || tail != state.TailSHA256 {
		return session.Session{}, nil, false
	}
	if _, err := f.Seek(state.Offset, io.SeekStart); err != nil {
		return session.Session{}, nil, false
	}

	out, stopped, err := parseSessionIntoMode(f, previous, metadataOnly)
	if err != nil {
		return session.Session{}, nil, false
	}
	var nextState []byte
	if !stopped && readerAtEnd(f, currentID.Size) && hasTrailingNewline(f, currentID.Size) {
		head, tail, ok = prefixBoundaryHashes(f, currentID.Size)
		if !ok {
			return session.Session{}, nil, false
		}
		nextState, err = json.Marshal(incrementalParseState{
			Offset:     currentID.Size,
			HeadSHA256: head,
			TailSHA256: tail,
		})
		if err != nil {
			return session.Session{}, nil, false
		}
	}
	return out, nextState, true
}

func readerAtEnd(f *os.File, expectedSize int64) bool {
	offset, err := f.Seek(0, io.SeekCurrent)
	return err == nil && offset == expectedSize
}

func hasTrailingNewline(f *os.File, size int64) bool {
	if size <= 0 {
		return false
	}
	var last [1]byte
	_, err := f.ReadAt(last[:], size-1)
	return err == nil && last[0] == '\n'
}

func prefixBoundaryHashes(f *os.File, size int64) (string, string, bool) {
	if size <= 0 {
		return "", "", false
	}
	// Hashing the whole old prefix would preserve the original O(file size)
	// startup cost. The producer's append-only contract plus both 64 KiB edges
	// rejects edge-altering replacements, prefix rewrites, and append-boundary
	// rewrites without rereading a multi-megabyte rollout; size checks reject
	// truncation separately.
	edgeSize := min(size, incrementalHashEdgeBytes)
	head := make([]byte, edgeSize)
	if _, err := f.ReadAt(head, 0); err != nil {
		return "", "", false
	}
	tail := make([]byte, edgeSize)
	if _, err := f.ReadAt(tail, size-edgeSize); err != nil {
		return "", "", false
	}
	headSum := sha256.Sum256(head)
	tailSum := sha256.Sum256(tail)
	return formatSHA256(headSum[:]), formatSHA256(tailSum[:]), true
}

func formatSHA256(sum []byte) string {
	return hex.EncodeToString(sum)
}

func parseSession(r io.Reader) (session.Session, error) {
	out, _, err := parseSessionInto(r, session.Session{})
	return out, err
}

func parseSessionInto(r io.Reader, out session.Session) (session.Session, bool, error) {
	return parseSessionIntoMode(r, out, false)
}

func parseSessionIntoMode(r io.Reader, out session.Session, metadataOnly bool) (session.Session, bool, error) {
	if out.Metadata == nil {
		out.Metadata = make(map[string]string)
	}
	haveSessionMeta := out.ID != ""
	stoppedAtInheritedHistory := false

	visit := func(line []byte) bool {
		if metadataOnly && !metadataRecordNeedsFullDecode(line) {
			return true
		}
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
						stoppedAtInheritedHistory = true
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
				entrypoint := sourceEntrypoint(meta.Source)
				if entrypoint != "" {
					out.Metadata["entrypoint"] = entrypoint
				}
				if isNonInteractiveSessionMeta(meta, entrypoint) {
					out.Metadata["interaction_mode"] = "non_interactive"
				}
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
	}
	var oversized int
	var err error
	if metadataOnly {
		oversized, err = readCodexMetadataRecords(r, visit)
	} else {
		oversized, err = readCodexRecords(r, visit)
	}
	if oversized > 0 {
		markReportEvidencePartial(out.Metadata, oversizedJSONLRecordNote)
	}
	return out, stoppedAtInheritedHistory, err
}

func metadataRecordNeedsFullDecode(line []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(line))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return true
	}
	recordType := ""
	for decoder.More() {
		keyToken, err := decoder.Token()
		key, ok := keyToken.(string)
		if err != nil || !ok {
			return true
		}
		switch key {
		case "type":
			if decoder.Decode(&recordType) != nil {
				return true
			}
		case "payload":
			switch recordType {
			case "session_meta", "turn_context":
				return true
			case "response_item":
				return responseItemNeedsFullDecode(decoder)
			case "":
				// Producer records put their discriminator before payload. Fall
				// back if that ordering changes instead of guessing from content.
				return true
			default:
				return false
			}
		default:
			var ignored json.RawMessage
			if decoder.Decode(&ignored) != nil {
				return true
			}
		}
	}
	return true
}

func responseItemNeedsFullDecode(decoder *json.Decoder) bool {
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return true
	}
	payloadType := ""
	for decoder.More() {
		keyToken, err := decoder.Token()
		key, ok := keyToken.(string)
		if err != nil || !ok {
			return true
		}
		switch key {
		case "type":
			if decoder.Decode(&payloadType) != nil {
				return true
			}
			if payloadType != "message" {
				return false
			}
		case "role":
			var role string
			if decoder.Decode(&role) != nil {
				return true
			}
			return payloadType != "message" || role == "user"
		case "content":
			// A changed producer field order is safe but slower until the
			// header can again prove this is not a user-authored message.
			return true
		default:
			var ignored json.RawMessage
			if decoder.Decode(&ignored) != nil {
				return true
			}
		}
	}
	return true
}

func readCodexMetadataRecords(r io.Reader, visit func([]byte) bool) (int, error) {
	reader := bufio.NewReader(r)
	evidenceRisk := 0
	for {
		var record []byte
		var prefix []byte
		var suffix []byte
		totalBytes := 0
		discarded := false
		oversized := false
		haveFragment := false

		for {
			fragment, continues, err := reader.ReadLine()
			if err != nil {
				if errors.Is(err, io.EOF) && !haveFragment {
					return evidenceRisk, nil
				}
				if !errors.Is(err, io.EOF) {
					return evidenceRisk, err
				}
				continues = false
			}
			haveFragment = true
			totalBytes += len(fragment)
			if len(prefix) < oversizedRecordEdgeBytes {
				remaining := oversizedRecordEdgeBytes - len(prefix)
				prefix = append(prefix, fragment[:min(remaining, len(fragment))]...)
			}

			if !discarded && !oversized {
				if len(fragment) > maxJSONLRecordBytes-len(record) {
					suffix = appendCodexRecordSuffix(suffix, record)
					suffix = appendCodexRecordSuffix(suffix, fragment)
					record = nil
					oversized = true
				} else {
					record = append(record, fragment...)
					if !metadataRecordNeedsFullDecode(prefix) {
						record = nil
						discarded = true
					}
				}
			} else if oversized {
				suffix = appendCodexRecordSuffix(suffix, fragment)
			}

			if !continues {
				break
			}
		}
		if discarded {
			continue
		}
		if oversized {
			recovered, timestamped, ok := recoverOversizedCodexUserRecord(jsonlrecords.OversizedRecord{
				Prefix: prefix,
				Suffix: suffix,
				Bytes:  totalBytes,
			})
			if ok {
				if !visit(recovered) {
					return evidenceRisk, nil
				}
				if !timestamped {
					evidenceRisk++
				}
			} else if oversizedCouldContainUserEvidence(prefix) {
				evidenceRisk++
			}
			continue
		}
		record = bytes.TrimSpace(record)
		if len(record) != 0 && !visit(record) {
			return evidenceRisk, nil
		}
	}
}

func appendCodexRecordSuffix(suffix, fragment []byte) []byte {
	if len(fragment) >= oversizedRecordEdgeBytes {
		return append(suffix[:0], fragment[len(fragment)-oversizedRecordEdgeBytes:]...)
	}
	overflow := len(suffix) + len(fragment) - oversizedRecordEdgeBytes
	if overflow > 0 {
		copy(suffix, suffix[overflow:])
		suffix = suffix[:len(suffix)-overflow]
	}
	return append(suffix, fragment...)
}

func sourceEntrypoint(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return strings.TrimSpace(text)
	}
	var tagged map[string]json.RawMessage
	if json.Unmarshal(raw, &tagged) == nil {
		if _, ok := tagged["subagent"]; ok {
			return "subagent"
		}
	}
	return ""
}

func isNonInteractiveSessionMeta(meta sessionMeta, entrypoint string) bool {
	// Codex exec persists normal rollout JSONL, so source/originator are the
	// stable fields that distinguish script-style runs from interactive TUI or
	// desktop sessions without depending on prompt text.
	return entrypoint == "exec" || meta.Originator == "codex_exec"
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
		if text == "" {
			continue
		}
		if request, wrapped := extractWrappedUserRequest(text); wrapped {
			if request != "" {
				parts = append(parts, request)
			}
			continue
		}
		if isInjectedUserContext(text) {
			continue
		}
		parts = append(parts, text)
	}
	return collapseWhitespace(strings.Join(parts, "\n"))
}

func readUserPreviews(path string, opts session.PreviewOptions) ([]session.MessagePreview, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = f.Close() }()

	var messages []session.MessagePreview
	oversized, err := readCodexRecords(f, func(line []byte) bool {
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

func readCodexRecords(r io.Reader, visit func([]byte) bool) (int, error) {
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
			if recovered, timestamped, ok := recoverOversizedCodexUserRecord(record); ok {
				stopped = !visit(recovered)
				if !timestamped {
					evidenceRisk++
				}
				return
			}
			if oversizedCouldContainUserEvidence(record.Prefix) {
				evidenceRisk++
			}
		},
	)
	return evidenceRisk, err
}

func recoverOversizedCodexUserRecord(
	record jsonlrecords.OversizedRecord,
) ([]byte, bool, bool) {
	compact := jsonlrecords.Compact(record.Prefix)
	if !bytes.Contains(compact, []byte(`"type":"response_item"`)) ||
		!bytes.Contains(compact, []byte(`"type":"message"`)) ||
		!bytes.Contains(compact, []byte(`"role":"user"`)) {
		return nil, false, false
	}

	text, ok := jsonlrecords.TruncatedStringField(
		record,
		"text",
		oversizedTextEdgeBytes,
	)
	if !ok {
		text, ok = jsonlrecords.TruncatedStringField(
			record,
			"input_text",
			oversizedTextEdgeBytes,
		)
	}
	if !ok || strings.TrimSpace(text) == "" {
		return nil, false, false
	}

	timestamp, _ := jsonlrecords.CompleteStringField(record.Prefix, "timestamp")
	payload, err := json.Marshal(responseMessage{
		Type: "message",
		Role: "user",
		Content: []messageContent{{
			Type: "input_text",
			Text: text,
		}},
	})
	if err != nil {
		return nil, false, false
	}
	line, err := json.Marshal(rawRecord{
		Timestamp: timestamp,
		Type:      "response_item",
		Payload:   payload,
	})
	if err != nil {
		return nil, false, false
	}
	return line, timestamp != "", true
}

func oversizedCouldContainUserEvidence(prefix []byte) bool {
	compact := jsonlrecords.Compact(prefix)
	if bytes.Contains(compact, []byte(`"role":"user"`)) {
		return true
	}
	if bytes.Contains(compact, []byte(`"role":"assistant"`)) ||
		bytes.Contains(compact, []byte(`"type":"custom_tool_call_output"`)) ||
		bytes.Contains(compact, []byte(`"type":"function_call_output"`)) {
		return false
	}
	// Unknown producer shapes remain conservative. Only known assistant/tool
	// payloads are allowed to avoid a false partial-coverage warning.
	return true
}

func markReportEvidencePartial(metadata map[string]string, note string) {
	metadata[session.MetadataReportEvidenceStatus] = session.ReportEvidencePartial
	metadata[session.MetadataReportEvidenceNote] = note
}

func extractWrappedUserRequest(text string) (string, bool) {
	text = strings.TrimSpace(text)
	switch {
	case strings.HasPrefix(text, "# Response annotations:"):
		parts := responseAnnotationComments(text)
		if request := desktopRequest(text); request != "" {
			parts = append(parts, request)
		}
		return strings.Join(parts, "\n"), true
	case strings.HasPrefix(text, "<in-app-browser-context"),
		strings.HasPrefix(text, "# Files mentioned by the user:"):
		return desktopRequest(text), true
	default:
		return "", false
	}
}

func desktopRequest(text string) string {
	// Codex Desktop adds the marker after ambient UI state, attachment
	// references, or response selections. Restrict extraction to those known
	// envelopes so ordinary user-authored Markdown containing the same heading
	// is never truncated.
	index := strings.Index(text, desktopRequestMarker)
	if index < 0 {
		return ""
	}
	return strings.TrimSpace(text[index+len(desktopRequestMarker):])
}

func responseAnnotationComments(text string) []string {
	const (
		openTag  = "<response-annotations>"
		closeTag = "</response-annotations>"
	)
	start := strings.Index(text, openTag)
	if start < 0 {
		return nil
	}
	start += len(openTag)
	endOffset := strings.Index(text[start:], closeTag)
	if endOffset < 0 {
		return nil
	}

	var annotations []struct {
		Annotation string `json:"annotation"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(text[start:start+endOffset])), &annotations) != nil {
		return nil
	}

	comments := make([]string, 0, len(annotations))
	for _, annotation := range annotations {
		if comment := strings.TrimSpace(annotation.Annotation); comment != "" {
			// The selected text is an earlier assistant response; only the
			// annotation is new user-authored evidence.
			comments = append(comments, comment)
		}
	}
	return comments
}

func isInjectedUserContext(text string) bool {
	prefixes := []string{
		"# AGENTS.md instructions",
		"<app-context",
		"<apps_instructions",
		"<collaboration_mode",
		"<codex_internal_context",
		"<environment_context",
		"<heartbeat",
		"<in-app-browser-context",
		"<multi_agent_mode",
		"<permissions",
		"<plugins_instructions",
		"<recommended_plugins",
		"<skills_instructions",
		"<skill",
		"<turn_aborted",
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
