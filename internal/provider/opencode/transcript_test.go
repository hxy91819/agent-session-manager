package opencode

import "testing"

func TestReadTranscriptReadsLegacyConversationMessages(t *testing.T) {
	home := t.TempDir()
	writeOpencodeSession(t, home, "project", "reader", "/repo", `{"id":"reader","projectID":"project","directory":"/repo","title":"title","time":{"created":1781322000000,"updated":1781322060000}}`)
	writeOpencodeMessage(t, home, "reader", "user", "user", "prompt")
	writeOpencodeMessage(t, home, "reader", "assistant", "assistant", "answer")
	got, err := New(home).ReadTranscript("reader")
	if err != nil || len(got.Messages) != 2 {
		t.Fatalf("transcript=%#v err=%v", got, err)
	}
}
