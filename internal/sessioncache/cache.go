package sessioncache

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/hxy91819/agent-session-manager/internal/session"
)

// Version covers parser-derived session fields stored in the cache. Bump it
// when a provider starts extracting new semantics from an otherwise unchanged
// source file, otherwise old cache entries can hide the new metadata.
const Version = 7

const defaultShardCount = 32

type FileIdentity struct {
	Provider string
	Path     string
	Size     int64
	ModTime  time.Time
}

type Cache struct {
	Version int              `json:"version"`
	Entries map[string]Entry `json:"entries"`
	dirty   bool

	path             string
	shardCount       int
	generation       string
	useShards        bool
	inline           bool
	adaptiveShards   bool
	legacyLoaded     bool
	migrationPending bool
	shards           map[int]map[string]Entry
	loadedShards     map[int]bool
	dirtyShards      map[int]bool
	states           map[string][]byte
	shardStates      map[int]map[string][]byte
	renameFile       func(string, string) error
}

type Entry struct {
	Provider        string          `json:"provider"`
	Path            string          `json:"path"`
	Size            int64           `json:"size"`
	ModTimeUnixNano int64           `json:"mod_time_unix_nano"`
	Session         session.Session `json:"session"`
}

type manifest struct {
	Version    int               `json:"version"`
	ShardCount int               `json:"shard_count"`
	Generation string            `json:"generation"`
	Inline     bool              `json:"inline,omitempty"`
	Entries    map[string]Entry  `json:"entries,omitempty"`
	States     map[string][]byte `json:"states,omitempty"`
}

type shardFile struct {
	Version int               `json:"version"`
	Entries map[string]Entry  `json:"entries"`
	States  map[string][]byte `json:"states,omitempty"`
}

func DefaultPath(provider string) (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "asm", provider+"-sessions.json"), nil
}

func SkipLoadForEmptyDiscovery(opts session.DiscoverOptions, fileCount int) bool {
	return fileCount == 0 && (!opts.Since.IsZero() || opts.LimitFiles > 0)
}

func Load(path string) *Cache {
	return loadWithShardCount(path, defaultShardCount)
}

func loadWithShardCount(path string, shardCount int) *Cache {
	cache := Cache{
		Version:        Version,
		path:           path,
		shardCount:     shardCount,
		shards:         make(map[int]map[string]Entry),
		loadedShards:   make(map[int]bool),
		dirtyShards:    make(map[int]bool),
		adaptiveShards: shardCount == defaultShardCount,
	}
	if path == "" {
		cache.Entries = make(map[string]Entry)
		cache.legacyLoaded = true
		return &cache
	}
	data, err := os.ReadFile(manifestPath(path))
	if err != nil {
		return &cache
	}
	var stored manifest
	if json.Unmarshal(data, &stored) == nil && stored.Version == Version && stored.ShardCount > 0 && stored.ShardCount <= 256 && stored.Generation != "" {
		cache.shardCount = stored.ShardCount
		cache.generation = stored.Generation
		cache.useShards = true
		cache.adaptiveShards = false
		if stored.Inline {
			cache.inline = true
			cache.shardCount = 1
			cache.shards[0] = stored.Entries
			if stored.States != nil {
				cache.shardStates = map[int]map[string][]byte{0: stored.States}
			}
			if cache.shards[0] == nil {
				cache.shards[0] = make(map[string]Entry)
			}
			cache.loadedShards[0] = true
		}
	}
	return &cache
}

func (c *Cache) Get(id FileIdentity) (session.Session, bool) {
	c.initialize("")
	key := Key(id.Provider, id.Path)
	entry, ok := c.entry(key)
	if !ok || entry.Provider != id.Provider || entry.Path != id.Path || entry.Size != id.Size {
		return session.Session{}, false
	}
	if entry.ModTimeUnixNano != id.ModTime.UnixNano() {
		return session.Session{}, false
	}
	return cloneSession(entry.Session), true
}

func (c *Cache) GetLatest(provider, path string) (FileIdentity, session.Session, []byte, bool) {
	c.initialize("")
	entry, ok := c.entry(Key(provider, path))
	if !ok || entry.Provider != provider || entry.Path != path {
		return FileIdentity{}, session.Session{}, nil, false
	}
	return FileIdentity{
		Provider: provider,
		Path:     path,
		Size:     entry.Size,
		ModTime:  time.Unix(0, entry.ModTimeUnixNano),
	}, cloneSession(entry.Session), append([]byte(nil), c.state(Key(provider, path))...), true
}

func (c *Cache) Put(id FileIdentity, s session.Session) session.Session {
	return c.PutWithState(id, s, nil)
}

func (c *Cache) PutWithState(id FileIdentity, s session.Session, state []byte) session.Session {
	c.initialize("")
	s.Title = session.NormalizeTitle(s.Title)
	entry := Entry{
		Provider:        id.Provider,
		Path:            id.Path,
		Size:            id.Size,
		ModTimeUnixNano: id.ModTime.UnixNano(),
		Session:         cloneSession(s),
	}
	key := Key(id.Provider, id.Path)
	if c.useShards {
		shard := c.shardForKey(key)
		c.loadShard(shard)
		c.shards[shard][key] = entry
		c.setShardState(shard, key, state)
		c.dirtyShards[shard] = true
		return s
	}
	c.loadLegacy()
	c.Entries[key] = entry
	c.setState(key, state)
	c.dirty = true
	return s
}

func (c *Cache) Keep(keys map[string]struct{}) {
	c.initialize("")
	if c.useShards {
		for shard := 0; shard < c.shardCount; shard++ {
			c.loadShard(shard)
			for key := range c.shards[shard] {
				if _, ok := keys[key]; !ok {
					delete(c.shards[shard], key)
					delete(c.shardStates[shard], key)
					c.dirtyShards[shard] = true
				}
			}
		}
		return
	}
	c.loadLegacy()
	for key := range c.Entries {
		if _, ok := keys[key]; !ok {
			delete(c.Entries, key)
			delete(c.states, key)
			c.dirty = true
		}
	}
}

func (c *Cache) Save(path string) error {
	if path == "" {
		return nil
	}
	c.initialize(path)
	if c.useShards {
		return c.saveDirtyShards()
	}
	if !c.dirty && !c.migrationPending {
		return nil
	}
	return c.migrateLegacy()
}

func Key(provider, path string) string {
	return provider + "\x00" + path
}

func (c *Cache) initialize(path string) {
	if c.Version == 0 {
		c.Version = Version
	}
	if c.path == "" {
		c.path = path
	}
	if c.shardCount <= 0 {
		c.shardCount = defaultShardCount
	}
	if c.shards == nil {
		c.shards = make(map[int]map[string]Entry)
	}
	if c.loadedShards == nil {
		c.loadedShards = make(map[int]bool)
	}
	if c.dirtyShards == nil {
		c.dirtyShards = make(map[int]bool)
	}
	if c.Entries != nil && c.path == "" {
		c.legacyLoaded = true
	}
}

func (c *Cache) entry(key string) (Entry, bool) {
	if c.useShards {
		shard := c.shardForKey(key)
		c.loadShard(shard)
		entry, ok := c.shards[shard][key]
		return entry, ok
	}
	c.loadLegacy()
	entry, ok := c.Entries[key]
	return entry, ok
}

func (c *Cache) state(key string) []byte {
	if c.useShards {
		return c.shardStates[c.shardForKey(key)][key]
	}
	return c.states[key]
}

func (c *Cache) setState(key string, state []byte) {
	if len(state) == 0 {
		delete(c.states, key)
		return
	}
	if c.states == nil {
		c.states = make(map[string][]byte)
	}
	c.states[key] = append([]byte(nil), state...)
}

func (c *Cache) setShardState(shard int, key string, state []byte) {
	if len(state) == 0 {
		if c.shardStates == nil {
			return
		}
		delete(c.shardStates[shard], key)
		return
	}
	if c.shardStates == nil {
		c.shardStates = make(map[int]map[string][]byte)
	}
	if c.shardStates[shard] == nil {
		c.shardStates[shard] = make(map[string][]byte)
	}
	c.shardStates[shard][key] = append([]byte(nil), state...)
}

func (c *Cache) loadLegacy() {
	if c.legacyLoaded {
		if c.Entries == nil {
			c.Entries = make(map[string]Entry)
		}
		return
	}
	c.legacyLoaded = true
	c.Entries = make(map[string]Entry)
	if c.path == "" {
		return
	}
	data, err := os.ReadFile(c.path)
	if err != nil {
		return
	}
	var stored shardFile
	if json.Unmarshal(data, &stored) != nil || stored.Version != Version {
		return
	}
	if stored.Entries != nil {
		c.Entries = stored.Entries
	}
	c.states = stored.States
	c.migrationPending = true
}

func (c *Cache) loadShard(shard int) {
	if c.loadedShards[shard] {
		return
	}
	c.loadedShards[shard] = true
	c.shards[shard] = make(map[string]Entry)
	data, err := os.ReadFile(shardPath(c.path, c.generation, shard))
	if err != nil {
		return
	}
	var stored shardFile
	if json.Unmarshal(data, &stored) != nil || stored.Version != Version || stored.Entries == nil {
		return
	}
	c.shards[shard] = stored.Entries
	if stored.States != nil {
		if c.shardStates == nil {
			c.shardStates = make(map[int]map[string][]byte)
		}
		c.shardStates[shard] = stored.States
	}
}

func (c *Cache) saveDirtyShards() error {
	if len(c.dirtyShards) == 0 {
		return nil
	}
	if c.inline {
		if err := c.writeJSONAtomically(manifestPath(c.path), manifest{
			Version: Version, ShardCount: 1, Generation: c.generation,
			Inline: true, Entries: c.shards[0],
			States: c.shardStates[0],
		}); err != nil {
			return err
		}
		c.dirtyShards = make(map[int]bool)
		return nil
	}
	shards := make([]int, 0, len(c.dirtyShards))
	for shard := range c.dirtyShards {
		shards = append(shards, shard)
	}
	sort.Ints(shards)
	for _, shard := range shards {
		if err := c.writeJSONAtomically(shardPath(c.path, c.generation, shard), shardFile{Version: Version, Entries: c.shards[shard], States: c.shardStates[shard]}); err != nil {
			return err
		}
		delete(c.dirtyShards, shard)
	}
	return nil
}

func (c *Cache) migrateLegacy() error {
	generation := fmt.Sprintf("%x-%x", time.Now().UnixNano(), os.Getpid())
	if c.adaptiveShards && len(c.Entries) <= 128 {
		if err := c.writeJSONAtomically(manifestPath(c.path), manifest{
			Version: Version, ShardCount: 1, Generation: generation,
			Inline: true, Entries: c.Entries,
			States: c.states,
		}); err != nil {
			return err
		}
		c.useShards = true
		c.inline = true
		c.adaptiveShards = false
		c.shardCount = 1
		c.generation = generation
		c.shards = map[int]map[string]Entry{0: c.Entries}
		c.shardStates = map[int]map[string][]byte{0: c.states}
		c.loadedShards = map[int]bool{0: true}
		c.dirtyShards = make(map[int]bool)
		c.dirty = false
		c.migrationPending = false
		return nil
	}
	if c.adaptiveShards {
		if len(c.Entries) <= 1024 {
			c.shardCount = 16
		} else {
			c.shardCount = 64
		}
	}
	entriesByShard := make(map[int]map[string]Entry, c.shardCount)
	statesByShard := make(map[int]map[string][]byte, c.shardCount)
	for shard := 0; shard < c.shardCount; shard++ {
		entriesByShard[shard] = make(map[string]Entry)
		statesByShard[shard] = make(map[string][]byte)
	}
	for key, entry := range c.Entries {
		shard := c.shardForKey(key)
		entriesByShard[shard][key] = entry
		if state := c.states[key]; len(state) > 0 {
			statesByShard[shard][key] = state
		}
	}
	for shard := 0; shard < c.shardCount; shard++ {
		if len(entriesByShard[shard]) == 0 {
			continue
		}
		if err := c.writeJSONAtomically(shardPath(c.path, generation, shard), shardFile{Version: Version, Entries: entriesByShard[shard], States: statesByShard[shard]}); err != nil {
			return err
		}
	}
	if err := c.writeJSONAtomically(manifestPath(c.path), manifest{Version: Version, ShardCount: c.shardCount, Generation: generation}); err != nil {
		return err
	}
	c.useShards = true
	c.adaptiveShards = false
	c.generation = generation
	c.shards = entriesByShard
	c.shardStates = statesByShard
	c.loadedShards = make(map[int]bool, c.shardCount)
	for shard := 0; shard < c.shardCount; shard++ {
		c.loadedShards[shard] = true
	}
	c.dirtyShards = make(map[int]bool)
	c.dirty = false
	c.migrationPending = false
	return nil
}

func (c *Cache) shardForKey(key string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return int(h.Sum32() % uint32(c.shardCount))
}

func shardDir(path string) string {
	return path + ".d"
}

func manifestPath(path string) string {
	return filepath.Join(shardDir(path), "manifest.json")
}

func shardPath(path, generation string, shard int) string {
	return filepath.Join(shardDir(path), fmt.Sprintf("shard-%s-%03d.json", generation, shard))
}

func (c *Cache) writeJSONAtomically(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	encErr := json.NewEncoder(f).Encode(value)
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
	return nil
}

func cloneSession(s session.Session) session.Session {
	if s.Metadata == nil {
		return s
	}
	metadata := make(map[string]string, len(s.Metadata))
	for key, value := range s.Metadata {
		metadata[key] = value
	}
	s.Metadata = metadata
	return s
}
