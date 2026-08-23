package dsh

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadTranscriptReadsPlainSessionLog(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "sessions", "repo", "reader", "session.jsonl")
	writeDshReaderFile(t, path, `{"type":"session","version":0,"id":"reader","createdAt":1781312400000,"cwd":"/repo"}
{"type":"user/message","time":1781312401000,"data":{"content":[{"type":"text","text":"prompt"}],"source":{"kind":"user"}}}
{"type":"assistant/message","time":1781312402000,"data":{"content":[{"type":"text","text":"answer"}]}}
`)
	got, err := New(home).ReadTranscript("reader")
	if err != nil || len(got.Messages) != 2 {
		t.Fatalf("transcript=%#v err=%v", got, err)
	}
}

func writeDshReaderFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}
