package openclaw

import (
	"path/filepath"
	"testing"
)

func TestReadTranscriptReadsIndexedSessionFile(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "agents", "main", "sessions")
	writeFile(t, filepath.Join(dir, "sessions.json"), `{"reader":{"sessionId":"native-reader","sessionFile":"reader.jsonl","spawnedCwd":"/repo"}}`)
	writeFile(t, filepath.Join(dir, "reader.jsonl"), `{"role":"user","content":"prompt"}
{"role":"assistant","content":"answer"}
`)
	got, err := New(home).ReadTranscript("reader")
	if err != nil || len(got.Messages) != 2 {
		t.Fatalf("transcript=%#v err=%v", got, err)
	}
}
