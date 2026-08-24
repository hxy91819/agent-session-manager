package cursor

import (
	"path/filepath"
	"testing"
)

func TestReadTranscriptReturnsConversationMessages(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "projects", "project-reader", "agent-transcripts", "reader", "reader.jsonl")
	writeFile(t, path, `{"role":"user","timestamp":"2026-06-13T01:00:00Z","message":{"content":[{"type":"text","text":"prompt"}]}}
{"role":"assistant","timestamp":"2026-06-13T01:01:00Z","message":{"content":[{"type":"text","text":"answer"}]}}
`)
	got, err := New(home).ReadTranscript("reader")
	if err != nil || len(got.Messages) != 2 {
		t.Fatalf("transcript=%#v err=%v", got, err)
	}
}
