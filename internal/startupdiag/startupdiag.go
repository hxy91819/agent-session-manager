package startupdiag

import (
	"encoding/json"
	"os"
	"sort"
	"sync"
	"time"
)

const envFile = "ASM_STARTUP_DIAG_FILE"

type event struct {
	Provider string `json:"provider"`
	Stage    string `json:"stage"`
	Nanos    int64  `json:"nanos"`
	Count    int64  `json:"count,omitempty"`
	Bytes    int64  `json:"bytes,omitempty"`
}

type key struct {
	provider string
	stage    string
}

var recorder = struct {
	sync.Mutex
	path   string
	events map[key]event
}{
	path:   os.Getenv(envFile),
	events: make(map[key]event),
}

// Begin starts an aggregate startup diagnostic stage. When diagnostics are
// disabled, the returned function is a no-op and no clock is read.
func Begin(provider, stage string) func(count, bytes int64) {
	if recorder.path == "" {
		return func(int64, int64) {}
	}
	started := time.Now()
	return func(count, bytes int64) {
		recorder.Lock()
		defer recorder.Unlock()
		itemKey := key{provider: provider, stage: stage}
		item := recorder.events[itemKey]
		item.Provider = provider
		item.Stage = stage
		item.Nanos += time.Since(started).Nanoseconds()
		item.Count += count
		item.Bytes += bytes
		recorder.events[itemKey] = item
	}
}

// Flush writes only aggregate diagnostic events. It is intentionally called
// by the CLI after discovery so normal commands do not retain session data.
func Flush() error {
	if recorder.path == "" {
		return nil
	}
	recorder.Lock()
	items := make([]event, 0, len(recorder.events))
	for _, item := range recorder.events {
		items = append(items, item)
	}
	recorder.Unlock()
	sort.Slice(items, func(i, j int) bool {
		if items[i].Provider != items[j].Provider {
			return items[i].Provider < items[j].Provider
		}
		return items[i].Stage < items[j].Stage
	})
	encoded, err := json.Marshal(items)
	if err != nil {
		return err
	}
	return os.WriteFile(recorder.path, append(encoded, '\n'), 0o600)
}
