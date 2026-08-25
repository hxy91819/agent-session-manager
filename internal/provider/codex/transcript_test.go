package codex

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hxy91819/agent-session-manager/internal/session"
)

func TestReadTranscriptReturnsUserAndAssistantMessages(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "sessions", "2026", "06", "13", "reader.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, `{"timestamp":"2026-06-13T01:00:00Z","type":"session_meta","payload":{"id":"reader","timestamp":"2026-06-13T01:00:00Z","cwd":"/repo"}}
{"timestamp":"2026-06-13T01:01:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"prompt"}]}}
{"timestamp":"2026-06-13T01:02:00Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"answer"}]}}
`)
	got, err := New(home).ReadTranscript("reader")
	if err != nil || len(got.Messages) != 2 {
		t.Fatalf("transcript=%#v err=%v", got, err)
	}
}

func TestReadSelectedTranscriptUsesDiscoveredSourcePath(t *testing.T) {
	home := t.TempDir()
	selectedPath := filepath.Join(home, "sessions", "2026", "06", "13", "selected.jsonl")
	if err := os.MkdirAll(filepath.Dir(selectedPath), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, selectedPath, `{"timestamp":"2026-06-13T01:00:00Z","type":"session_meta","payload":{"id":"selected","cwd":"/repo"}}
{"timestamp":"2026-06-13T01:01:00Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"selected answer"}]}}
`)
	brokenPath := filepath.Join(home, "sessions", "2026", "06", "12", "broken.jsonl")
	if err := os.MkdirAll(filepath.Dir(brokenPath), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, brokenPath, "{broken\n")

	got, err := New(home).ReadSelectedTranscript(session.Session{ID: "selected", Provider: Name, Path: selectedPath})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Messages) != 1 || got.Messages[0].Text != "selected answer" {
		t.Fatalf("transcript = %#v", got)
	}
}
