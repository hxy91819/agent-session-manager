package show

import (
	"strings"
	"testing"
	"time"

	"github.com/hxy91819/agent-session-manager/internal/session"
)

func transcript() session.Transcript {
	base := time.Date(2026, 6, 13, 1, 0, 0, 0, time.UTC)
	messages := []session.Message{
		{Role: RoleUser, Text: "first question", At: base},
		{Role: RoleAssistant, Text: "first answer", At: base.Add(time.Second)},
		{Role: RoleUser, Text: strings.Repeat("long ", 1000), At: base.Add(2 * time.Second)},
		{Role: RoleAssistant, Text: "the fix is ready", At: base.Add(3 * time.Second)},
	}
	return session.Transcript{
		Session: session.Session{
			ID:        "sess_x",
			Provider:  "claude",
			CWD:       "/repo",
			Title:     "t",
			CreatedAt: base,
			UpdatedAt: base.Add(3 * time.Second),
			Path:      "/repo/file.jsonl",
		},
		Messages: messages,
	}
}

func TestBuildDefaultIsBoundedTail(t *testing.T) {
	out := Build(transcript(), Options{})
	if out.TotalMessages != 4 || out.MatchedMessages != 4 || out.ReturnedMessages != 4 {
		t.Fatalf("counts = %d/%d/%d, want 4/4/4", out.TotalMessages, out.MatchedMessages, out.ReturnedMessages)
	}
	if len(out.Messages) != 4 {
		t.Fatalf("returned = %d", len(out.Messages))
	}
}

func TestBuildRoleFilterReportsMatchedCount(t *testing.T) {
	out := Build(transcript(), Options{Role: RoleUser})
	if out.MatchedMessages != 2 || out.ReturnedMessages != 2 {
		t.Fatalf("matched/returned = %d/%d, want 2/2", out.MatchedMessages, out.ReturnedMessages)
	}
	for _, msg := range out.Messages {
		if msg.Role != RoleUser {
			t.Fatalf("role = %q", msg.Role)
		}
	}
	// The oversized user message is still cut by the default per-message cap;
	// that shows up as a text cut, not as a dropped message.
	if out.TextCutMessages != 1 || !out.Truncated {
		t.Fatalf("textCut=%d truncated=%v, want 1/true", out.TextCutMessages, out.Truncated)
	}
}

func TestBuildGrepFiltersBySubstringCaseInsensitive(t *testing.T) {
	out := Build(transcript(), Options{Grep: "FIX"})
	if out.MatchedMessages != 1 || out.ReturnedMessages != 1 {
		t.Fatalf("matched/returned = %d/%d, want 1/1", out.MatchedMessages, out.ReturnedMessages)
	}
	if out.Filters.Grep != "FIX" {
		t.Fatalf("filters.grep = %q", out.Filters.Grep)
	}
}

func TestBuildGrepExcerptCentersOnMatch(t *testing.T) {
	long := strings.Repeat("a", 3000) + "needle" + strings.Repeat("b", 3000)
	tr := transcript()
	tr.Messages = []session.Message{{Role: RoleUser, Text: long}}
	out := Build(tr, Options{Grep: "needle"})
	if out.MatchedMessages != 1 || out.ReturnedMessages != 1 {
		t.Fatalf("matched/returned = %d/%d, want 1/1", out.MatchedMessages, out.ReturnedMessages)
	}
	text := out.Messages[0].Text
	if !strings.Contains(text, "needle") {
		t.Fatalf("excerpt lost match")
	}
	if !strings.HasPrefix(text, "…") || !strings.HasSuffix(text, "…") {
		t.Fatalf("excerpt should be centered with ellipses: %q", text[:20])
	}
	if got := len([]rune(strings.Trim(text, "…"))); got > DefaultMaxChars {
		t.Fatalf("excerpt too long: %d runes", got)
	}
	// A grep excerpt is a focused view chosen by the caller's own query and
	// fits the cap by construction, so it is not silent truncation.
	if out.Truncated || out.TextCutMessages != 0 {
		t.Fatalf("truncated=%v textCut=%d", out.Truncated, out.TextCutMessages)
	}
}

func TestBuildLastWindowTruncates(t *testing.T) {
	out := Build(transcript(), Options{Last: 2})
	if out.ReturnedMessages != 2 || !out.Truncated {
		t.Fatalf("returned=%d truncated=%v, want 2/true", out.ReturnedMessages, out.Truncated)
	}
	if out.Messages[len(out.Messages)-1].Text != "the fix is ready" {
		t.Fatalf("tail should keep the newest message last")
	}
}

func TestBuildFirstWindow(t *testing.T) {
	out := Build(transcript(), Options{First: 1})
	if out.ReturnedMessages != 1 || out.Messages[0].Text != "first question" {
		t.Fatalf("unexpected head window: %#v", out.Messages)
	}
}

func TestBuildMaxCharsCutsTextAndReportsIt(t *testing.T) {
	out := Build(transcript(), Options{MaxChars: 10})
	cut := 0
	for _, msg := range out.Messages {
		if strings.HasSuffix(msg.Text, "…") {
			cut++
		}
	}
	if cut == 0 || out.TextCutMessages != cut || !out.Truncated {
		t.Fatalf("cut=%d textCut=%d truncated=%v", cut, out.TextCutMessages, out.Truncated)
	}
}

func TestBuildFullReturnsEverythingUncut(t *testing.T) {
	out := Build(transcript(), Options{Full: true})
	if out.ReturnedMessages != 4 || out.Truncated || out.TextCutMessages != 0 {
		t.Fatalf("full mode must pass everything: %#v", out)
	}
	if len([]rune(out.Messages[2].Text)) != 5000 {
		t.Fatalf("full mode altered long message length: %d", len([]rune(out.Messages[2].Text)))
	}
}

func TestValidateRejectsBadOptions(t *testing.T) {
	if err := (Options{Role: "system"}).Validate(); err == nil {
		t.Fatal("bad role accepted")
	}
	if err := (Options{First: 1, Last: 1}).Validate(); err == nil {
		t.Fatal("first+last accepted")
	}
	if err := (Options{Last: -1}).Validate(); err == nil {
		t.Fatal("negative last accepted")
	}
}

func TestBuildGrepContextAndOffsets(t *testing.T) {
	tr := transcript()
	tr.Messages = []session.Message{
		{Role: RoleUser, Text: "before"},
		{Role: RoleAssistant, Text: "needle here"},
		{Role: RoleUser, Text: "after"},
	}
	out := Build(tr, Options{Grep: "needle", Before: 1, After: 1, Full: true})
	if len(out.Messages) != 3 || out.MatchedMessages != 1 || out.Messages[1].Index != 1 {
		t.Fatalf("context output = %#v", out)
	}
	if len(out.Messages[1].MatchOffsets) != 1 || out.Messages[1].MatchOffsets[0].Start != 0 || out.Messages[1].MatchOffsets[0].End != 6 {
		t.Fatalf("match offsets = %#v", out.Messages[1].MatchOffsets)
	}
}

func TestBuildRegexExactAndCaseSensitivity(t *testing.T) {
	tr := transcript()
	tr.Messages = []session.Message{{Role: RoleUser, Text: "Fix issue 42"}, {Role: RoleUser, Text: "fix another"}}
	if got := Build(tr, Options{Grep: `FIX\s+ISSUE\s+\d+`, Regex: true}).MatchedMessages; got != 1 {
		t.Fatalf("regex matches = %d", got)
	}
	if got := Build(tr, Options{Grep: "Fix issue 42", Exact: true}).MatchedMessages; got != 1 {
		t.Fatalf("exact matches = %d", got)
	}
	if got := Build(tr, Options{Grep: "FIX", CaseSensitive: true}).MatchedMessages; got != 0 {
		t.Fatalf("case-sensitive matches = %d", got)
	}
}

func TestBuildFromIndexAndSummary(t *testing.T) {
	tr := transcript()
	tr.Messages = []session.Message{{Role: RoleUser, Text: "zero"}, {Role: RoleAssistant, Text: "one"}, {Role: RoleUser, Text: "two"}}
	out := Build(tr, Options{Full: true, FromIndex: 1, Summary: true, SummaryChars: 2})
	if len(out.Messages) != 2 || out.Messages[0].Index != 1 || out.Messages[0].Text != "on…" {
		t.Fatalf("summary cursor output = %#v", out.Messages)
	}
}

func TestValidateRejectsGrepThatCannotFitMaxChars(t *testing.T) {
	if err := (Options{Grep: "needle", MaxChars: 7}).Validate(); err == nil {
		t.Fatal("grep term that cannot fit its excerpt was accepted")
	}
	if err := (Options{Grep: "needle", MaxChars: 8}).Validate(); err != nil {
		t.Fatalf("grep term should fit at max-chars=8: %v", err)
	}
}
