package pi

import "testing"

func TestReadTranscriptReturnsConversationMessages(t *testing.T) {
	home := t.TempDir()
	writePiSession(t, home, "reader", "/repo", []string{
		`{"type":"session","id":"reader","cwd":"/repo","timestamp":"2026-06-13T01:00:00Z"}`,
		`{"type":"message","timestamp":"2026-06-13T01:01:00Z","message":{"role":"user","content":[{"type":"text","text":"prompt"}]}}`,
		`{"type":"message","timestamp":"2026-06-13T01:02:00Z","message":{"role":"assistant","content":[{"type":"text","text":"answer"}]}}`,
	})
	got, err := New(home).ReadTranscript("reader")
	if err != nil || len(got.Messages) != 2 {
		t.Fatalf("transcript=%#v err=%v", got, err)
	}
}
