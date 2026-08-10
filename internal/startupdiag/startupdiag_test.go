package startupdiag

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestBeginAndFlushAggregateWithoutSessionData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "diagnostics.json")
	recorder.Lock()
	recorder.path = path
	recorder.events = make(map[key]event)
	recorder.Unlock()
	t.Cleanup(func() {
		recorder.Lock()
		recorder.path = os.Getenv(envFile)
		recorder.events = make(map[key]event)
		recorder.Unlock()
	})

	finish := Begin("claude", "primary_parse")
	finish(2, 1234)
	finish = Begin("claude", "primary_parse")
	finish(1, 100)
	if err := Flush(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got []event
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("events = %#v", got)
	}
	if got[0].Provider != "claude" || got[0].Stage != "primary_parse" ||
		got[0].Count != 3 || got[0].Bytes != 1334 || got[0].Nanos <= 0 {
		t.Fatalf("event = %#v", got[0])
	}
	if gotInfo, err := os.Stat(path); err != nil || gotInfo.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, err = %v", gotInfo.Mode().Perm(), err)
	}
}
