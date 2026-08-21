package session

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestAppendSearchMessageAppendsDenoisedMessages(t *testing.T) {
	corpus, full := AppendSearchMessage(nil, SearchMessage{Text: "  first prompt  ", Offset: 10})
	if full || len(corpus) != 1 || corpus[0].Text != "first prompt" || corpus[0].Offset != 10 {
		t.Fatalf("first append = %#v full=%v", corpus, full)
	}
	corpus, _ = AppendSearchMessage(corpus, SearchMessage{Text: "second prompt", At: time.Unix(100, 0)})
	if len(corpus) != 2 || corpus[1].Text != "second prompt" || corpus[1].At.IsZero() {
		t.Fatalf("second append = %#v", corpus)
	}
	if got, _ := AppendSearchMessage(corpus, SearchMessage{Text: " \t"}); len(got) != 2 {
		t.Fatalf("blank append changed corpus: %#v", got)
	}
}

func TestAppendSearchMessageCapsOneMessage(t *testing.T) {
	corpus, _ := AppendSearchMessage(nil, SearchMessage{Text: strings.Repeat("字", SearchMessageMaxBytes) + "|tail-token"})
	if len(corpus) != 1 || len(corpus[0].Text) > SearchMessageMaxBytes {
		t.Fatalf("message cap violated: %#v", corpus)
	}
	if !utf8.ValidString(corpus[0].Text) {
		t.Fatal("truncation split a rune")
	}
	if strings.Contains(corpus[0].Text, "tail-token") {
		t.Fatal("capped message kept its tail")
	}
}

func TestAppendSearchMessageCapsCorpus(t *testing.T) {
	corpus := []SearchMessage(nil)
	full := false
	for i := 0; i < SearchMessageMaxCount+8 && !full; i++ {
		corpus, full = AppendSearchMessage(corpus, SearchMessage{Text: strings.Repeat("a", SearchMessageMaxBytes)})
	}
	if !full || len(corpus) > SearchMessageMaxCount {
		t.Fatalf("count cap violated: %d full=%v", len(corpus), full)
	}
	frozen, stillFull := AppendSearchMessage(corpus, SearchMessage{Text: "overflow-token"})
	if !stillFull || len(frozen) != len(corpus) {
		t.Fatal("appending past the cap changed the corpus")
	}
}

func TestStripSearchCorpusKeepsMatchingFields(t *testing.T) {
	sessions := []Session{{ID: "one", Title: "keep", SearchMessages: []SearchMessage{{Text: "secret", Offset: 5}}}}
	got := StripSearchCorpus(sessions)
	if got[0].SearchMessages != nil || got[0].Title != "keep" || got[0].ID != "one" {
		t.Fatalf("strip damaged output fields: %#v", got[0])
	}
}
