package kimi

import (
	"path/filepath"
	"testing"
)

func TestReadTranscriptReturnsLatestPromptWithPartialMetadata(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "sessions", "repo", "reader")
	writeKimiSession(t, home, dir, "reader", "/repo", `{"createdAt":"2026-06-13T01:00:00Z","updatedAt":"2026-06-13T01:01:00Z","lastPrompt":"latest prompt"}`)
	got, err := New(home).ReadTranscript("reader")
	if err != nil || len(got.Messages) != 1 || got.Messages[0].Text != "latest prompt" {
		t.Fatalf("transcript=%#v err=%v", got, err)
	}
	if got.Session.Metadata["report_evidence_status"] != "partial" {
		t.Fatalf("metadata=%#v", got.Session.Metadata)
	}
}
