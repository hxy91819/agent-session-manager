package kimi

import (
	"path/filepath"
	"testing"
	"time"
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

func TestReadTranscriptReadsNumericMillisecondTimestamps(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "sessions", "repo", "reader")
	writeKimiSession(t, home, dir, "reader", "/repo", `{"createdAt":1787588320144,"updatedAt":1787588330643,"lastPrompt":"latest prompt"}`)

	got, err := New(home).ReadTranscript("reader")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Session.CreatedAt.Equal(time.UnixMilli(1787588320144).UTC()) {
		t.Fatalf("CreatedAt = %s", got.Session.CreatedAt)
	}
	if len(got.Messages) != 1 || !got.Messages[0].At.Equal(time.UnixMilli(1787588330643).UTC()) {
		t.Fatalf("messages = %#v", got.Messages)
	}
}
