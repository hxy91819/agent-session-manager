package codebuddy

import (
	"path/filepath"
	"testing"
)

func TestReadTranscriptReturnsConversationMessages(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "projects", "repo", "reader.jsonl")
	writeFile(t, path, `{"sessionId":"reader","cwd":"/repo","timestamp":"2026-06-13T01:00:00Z","role":"user","content":"prompt"}
{"sessionId":"reader","cwd":"/repo","timestamp":"2026-06-13T01:01:00Z","role":"assistant","content":"answer"}
`)
	got, err := New(home).ReadTranscript("reader")
	if err != nil || len(got.Messages) != 2 {
		t.Fatalf("transcript=%#v err=%v", got, err)
	}
}
