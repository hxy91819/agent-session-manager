package kiro

import (
	"path/filepath"
	"testing"
)

func TestReadTranscriptReadsPromptRecords(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "sessions", "cli")
	writeFile(t, filepath.Join(dir, "reader.json"), `{"session_id":"reader","cwd":"/repo","created_at":"2026-06-13T01:00:00Z","updated_at":"2026-06-13T01:01:00Z"}`)
	writeFile(t, filepath.Join(dir, "reader.jsonl"), `{"kind":"Prompt","data":{"content":[{"kind":"text","data":"prompt"}],"meta":{"timestamp":1781312400}}}`+"\n")
	got, err := New(home).ReadTranscript("reader")
	if err != nil || len(got.Messages) != 1 || got.Messages[0].Role != "user" {
		t.Fatalf("transcript=%#v err=%v", got, err)
	}
}
