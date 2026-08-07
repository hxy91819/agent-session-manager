package codex

import (
	"bufio"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hxy91819/agent-session-manager/internal/session"
)

const (
	dynamicTitleCacheVersion = 2
	dynamicTitleHistory      = "history"
	dynamicTitleSessionIndex = "session_index"
	dynamicTitleMaxRecord    = 4 * 1024 * 1024
	dynamicTitleHashEdge     = 64 * 1024
)

type dynamicTitleCache struct {
	Version int                               `json:"version"`
	Entries map[string]dynamicTitleIndexState `json:"entries"`
	dirty   bool

	renameFile func(string, string) error
}

type dynamicTitleIndexState struct {
	Kind            string            `json:"kind"`
	Offset          int64             `json:"offset"`
	ModTimeUnixNano int64             `json:"mod_time_unix_nano"`
	AppendSafe      bool              `json:"append_safe"`
	HeadSHA256      string            `json:"head_sha256"`
	TailSHA256      string            `json:"tail_sha256"`
	FileID          string            `json:"file_id,omitempty"`
	ChangeID        string            `json:"change_id,omitempty"`
	ContentSHA256   string            `json:"content_sha256,omitempty"`
	Titles          map[string]string `json:"titles"`
	IntegritySHA256 string            `json:"integrity_sha256"`
}

type historyRecord struct {
	SessionID string `json:"session_id"`
	Text      string `json:"text"`
}

type sessionIndexRecord struct {
	ID         string `json:"id"`
	ThreadName string `json:"thread_name"`
}

func dynamicTitleCachePath(cachePath string) string {
	if cachePath == "" {
		return ""
	}
	return cachePath + ".dynamic-titles.json"
}

func loadDynamicTitleCache(path string) *dynamicTitleCache {
	cache := &dynamicTitleCache{
		Version: dynamicTitleCacheVersion,
		Entries: make(map[string]dynamicTitleIndexState),
	}
	if path == "" {
		return cache
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return cache
	}
	var stored dynamicTitleCache
	if json.Unmarshal(data, &stored) != nil || stored.Version != dynamicTitleCacheVersion || stored.Entries == nil {
		return cache
	}
	cache.Entries = stored.Entries
	return cache
}

func (c *dynamicTitleCache) read(path, kind string) map[string]string {
	state, ok := c.Entries[path]
	if ok && state.Kind != kind {
		ok = false
	}

	f, err := os.Open(path)
	if err != nil {
		if ok {
			delete(c.Entries, path)
			c.dirty = true
		}
		return nil
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return nil
	}
	if ok && c.canReuse(f, info, state) {
		if info.Size() == state.Offset {
			return state.Titles
		}
		titles := cloneTitles(state.Titles)
		appendSafe, parseErr := scanDynamicTitles(io.NewSectionReader(f, state.Offset, info.Size()-state.Offset), kind, titles, true)
		if parseErr == nil {
			return c.storeParsed(path, kind, f, info.Size(), info.ModTime().UnixNano(), appendSafe, titles)
		}
	}

	return c.readFull(path, kind, f, info.Size(), info.ModTime().UnixNano())
}

func (c *dynamicTitleCache) canReuse(f *os.File, info os.FileInfo, state dynamicTitleIndexState) bool {
	size := info.Size()
	if state.Offset < 0 || size < state.Offset || state.Titles == nil {
		return false
	}
	if !dynamicTitleStateIntegrityValid(state) {
		return false
	}
	fileID, changeID, strongIdentity := dynamicTitleFileIdentity(info)
	if strongIdentity {
		if fileID != state.FileID {
			return false
		}
		if size == state.Offset && changeID != state.ChangeID {
			return false
		}
	} else {
		contentHash, err := dynamicTitleContentHash(f, state.Offset)
		if err != nil || contentHash != state.ContentSHA256 {
			return false
		}
	}
	if size == state.Offset && info.ModTime().UnixNano() != state.ModTimeUnixNano {
		return false
	}
	if size > state.Offset && !state.AppendSafe {
		return false
	}
	// Edge fingerprints reject the producer mutations seen in practice without
	// turning every warm discovery back into an O(index size) verification.
	head, tail, err := dynamicTitleFingerprints(f, state.Offset)
	return err == nil && head == state.HeadSHA256 && tail == state.TailSHA256
}

func (c *dynamicTitleCache) readFull(path, kind string, f *os.File, size, modTime int64) map[string]string {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil
	}
	titles := make(map[string]string)
	appendSafe, _ := scanDynamicTitles(io.LimitReader(f, size), kind, titles, false)
	normalizeDynamicTitles(titles)
	return c.storeParsed(path, kind, f, size, modTime, appendSafe, titles)
}

func (c *dynamicTitleCache) storeParsed(
	path string,
	kind string,
	f *os.File,
	size int64,
	modTime int64,
	appendSafe bool,
	titles map[string]string,
) map[string]string {
	info, err := f.Stat()
	if err != nil || info.Size() != size || info.ModTime().UnixNano() != modTime {
		return cloneTitles(titles)
	}
	head, tail, err := dynamicTitleFingerprints(f, size)
	if err != nil {
		return cloneTitles(titles)
	}
	appendSafe = appendSafe && dynamicTitleEndsWithNewline(f, size)
	fileID, changeID, strongIdentity := dynamicTitleFileIdentity(info)
	contentHash := ""
	if !strongIdentity {
		contentHash, err = dynamicTitleContentHash(f, size)
		if err != nil {
			return cloneTitles(titles)
		}
	}
	state := dynamicTitleIndexState{
		Kind:            kind,
		Offset:          size,
		ModTimeUnixNano: modTime,
		AppendSafe:      appendSafe,
		HeadSHA256:      head,
		TailSHA256:      tail,
		FileID:          fileID,
		ChangeID:        changeID,
		ContentSHA256:   contentHash,
		Titles:          titles,
	}
	c.Entries[path] = state
	c.dirty = true
	return titles
}

func dynamicTitleEndsWithNewline(f *os.File, size int64) bool {
	if size == 0 {
		return true
	}
	last := []byte{0}
	if _, err := f.ReadAt(last, size-1); err != nil {
		return false
	}
	return last[0] == '\n'
}

func scanDynamicTitles(r io.Reader, kind string, titles map[string]string, normalize bool) (bool, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), dynamicTitleMaxRecord)
	for scanner.Scan() {
		switch kind {
		case dynamicTitleHistory:
			applyHistoryTitle(scanner.Bytes(), titles, normalize)
		case dynamicTitleSessionIndex:
			applySessionIndexTitle(scanner.Bytes(), titles, normalize)
		}
	}
	if err := scanner.Err(); err != nil {
		return false, err
	}
	return true, nil
}

func applyHistoryTitle(data []byte, titles map[string]string, normalize bool) {
	var rec historyRecord
	if json.Unmarshal(data, &rec) != nil {
		return
	}
	text := strings.TrimSpace(rec.Text)
	if rec.SessionID == "" || text == "" || strings.HasPrefix(text, "$") || strings.HasPrefix(text, "/") {
		return
	}
	if normalize {
		text = normalizeDynamicTitle(text)
	}
	titles[rec.SessionID] = text
}

func applySessionIndexTitle(data []byte, titles map[string]string, normalize bool) {
	var rec sessionIndexRecord
	if json.Unmarshal(data, &rec) != nil {
		return
	}
	title := strings.TrimSpace(rec.ThreadName)
	if rec.ID == "" || title == "" {
		return
	}
	if normalize {
		title = normalizeDynamicTitle(title)
	}
	titles[rec.ID] = title
}

func normalizeDynamicTitle(title string) string {
	if len(title) <= session.MaxTitleRunes {
		return title
	}
	return session.NormalizeTitle(title)
}

func normalizeDynamicTitles(titles map[string]string) {
	for id, title := range titles {
		titles[id] = normalizeDynamicTitle(title)
	}
}

func dynamicTitleFingerprints(f *os.File, size int64) (string, string, error) {
	if size < 0 {
		return "", "", errors.New("negative dynamic title index size")
	}
	if size <= 2*dynamicTitleHashEdge {
		data := make([]byte, size)
		if _, err := f.ReadAt(data, 0); err != nil && !errors.Is(err, io.EOF) {
			return "", "", err
		}
		headStart := min(size, dynamicTitleHashEdge)
		tailStart := max(int64(0), size-dynamicTitleHashEdge)
		return dynamicTitleHash(data[:headStart]), dynamicTitleHash(data[tailStart:]), nil
	}
	headSize := min(size, dynamicTitleHashEdge)
	tailStart := max(int64(0), size-dynamicTitleHashEdge)
	head := make([]byte, headSize)
	if _, err := f.ReadAt(head, 0); err != nil && !errors.Is(err, io.EOF) {
		return "", "", err
	}
	tail := make([]byte, size-tailStart)
	if _, err := f.ReadAt(tail, tailStart); err != nil && !errors.Is(err, io.EOF) {
		return "", "", err
	}
	return dynamicTitleHash(head), dynamicTitleHash(tail), nil
}

func dynamicTitleHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func dynamicTitleContentHash(f *os.File, size int64) (string, error) {
	if size < 0 {
		return "", errors.New("negative dynamic title index size")
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, io.NewSectionReader(f, 0, size)); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func dynamicTitleStateIntegrity(state dynamicTitleIndexState) string {
	hash := sha256.New()
	writeString := func(value string) {
		var size [binary.MaxVarintLen64]byte
		n := binary.PutUvarint(size[:], uint64(len(value)))
		_, _ = hash.Write(size[:n])
		_, _ = io.WriteString(hash, value)
	}
	writeInt64 := func(value int64) {
		var data [8]byte
		binary.LittleEndian.PutUint64(data[:], uint64(value))
		_, _ = hash.Write(data[:])
	}
	writeString(state.Kind)
	writeInt64(state.Offset)
	writeInt64(state.ModTimeUnixNano)
	if state.AppendSafe {
		_, _ = hash.Write([]byte{1})
	} else {
		_, _ = hash.Write([]byte{0})
	}
	writeString(state.HeadSHA256)
	writeString(state.TailSHA256)
	writeString(state.FileID)
	writeString(state.ChangeID)
	writeString(state.ContentSHA256)
	ids := make([]string, 0, len(state.Titles))
	for id := range state.Titles {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		writeString(id)
		writeString(state.Titles[id])
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func dynamicTitleStateIntegrityValid(state dynamicTitleIndexState) bool {
	return state.IntegritySHA256 != "" && state.IntegritySHA256 == dynamicTitleStateIntegrity(state)
}

func cloneTitles(titles map[string]string) map[string]string {
	if titles == nil {
		return nil
	}
	clone := make(map[string]string, len(titles))
	for id, title := range titles {
		clone[id] = title
	}
	return clone
}

func (c *dynamicTitleCache) save(path string) error {
	if path == "" || !c.dirty {
		return nil
	}
	for key, state := range c.Entries {
		if state.IntegritySHA256 == "" {
			state.IntegritySHA256 = dynamicTitleStateIntegrity(state)
			c.Entries[key] = state
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	encErr := json.NewEncoder(f).Encode(struct {
		Version int                               `json:"version"`
		Entries map[string]dynamicTitleIndexState `json:"entries"`
	}{Version: dynamicTitleCacheVersion, Entries: c.Entries})
	closeErr := f.Close()
	if encErr != nil {
		_ = os.Remove(tmp)
		return encErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	renameFile := c.renameFile
	if renameFile == nil {
		renameFile = os.Rename
	}
	if err := renameFile(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	c.dirty = false
	return nil
}

func readSessionIndexTitles(path string) map[string]string {
	return new(dynamicTitleCache).readFullPath(path, dynamicTitleSessionIndex)
}

func (c *dynamicTitleCache) readFullPath(path, kind string) map[string]string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil
	}
	titles := make(map[string]string)
	_, _ = scanDynamicTitles(io.LimitReader(f, info.Size()), kind, titles, false)
	return titles
}
