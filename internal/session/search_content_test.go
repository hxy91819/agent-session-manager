package session

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestAppendSearchMessageJoinsDenoisedMessages(t *testing.T) {
	content, full := AppendSearchMessage("", "  first prompt  ")
	if full || content != "first prompt" {
		t.Fatalf("first append = %q full=%v", content, full)
	}
	content, full = AppendSearchMessage(content, "second prompt")
	if full || content != "first prompt\nsecond prompt" {
		t.Fatalf("second append = %q full=%v", content, full)
	}
	if got, full := AppendSearchMessage(content, " \t"); full || got != content {
		t.Fatalf("blank append changed content: %q full=%v", got, full)
	}
}

func TestAppendSearchMessageCapsOneMessage(t *testing.T) {
	content, _ := AppendSearchMessage("", strings.Repeat("字", SearchMessageMaxBytes)+"|tail-token")
	if len(content) > SearchMessageMaxBytes {
		t.Fatalf("message cap violated: %d bytes", len(content))
	}
	if !utf8.ValidString(content) {
		t.Fatal("truncation split a rune")
	}
	if strings.Contains(content, "tail-token") {
		t.Fatal("capped message kept its tail")
	}
}

func TestAppendSearchMessageCapsSessionTotal(t *testing.T) {
	content := ""
	full := false
	for i := 0; i < 64 && !full; i++ {
		content, full = AppendSearchMessage(content, strings.Repeat("a", SearchMessageMaxBytes))
	}
	if !full || len(content) > SearchContentMaxBytes {
		t.Fatalf("total cap violated: %d bytes full=%v", len(content), full)
	}
	frozen, stillFull := AppendSearchMessage(content, "overflow-token")
	if !stillFull || frozen != content {
		t.Fatal("appending past the cap changed content")
	}
}

func TestStripSearchContentKeepsMatchingFields(t *testing.T) {
	sessions := []Session{{ID: "one", Title: "keep", SearchContent: "secret"}}
	got := StripSearchContent(sessions)
	if got[0].SearchContent != "" || got[0].Title != "keep" || got[0].ID != "one" {
		t.Fatalf("strip damaged output fields: %#v", got[0])
	}
}
