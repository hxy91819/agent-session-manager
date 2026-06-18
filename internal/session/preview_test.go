package session

import (
	"testing"
	"time"
)

func TestSelectMessagePreviewsDeduplicatesEdges(t *testing.T) {
	got := SelectMessagePreviews([]MessagePreview{
		{Text: "one"},
		{Text: "two"},
		{Text: "three"},
	}, PreviewOptions{UserMessagesPerEdge: 2, MaxChars: 500})

	if len(got) != 3 {
		t.Fatalf("len = %d, want 3: %#v", len(got), got)
	}
	if got[0].Text != "one" || got[1].Text != "two" || got[2].Text != "three" {
		t.Fatalf("previews = %#v", got)
	}
}

func TestSelectMessagePreviewsUsesEdgeOffsetForIncrementalFetch(t *testing.T) {
	got := SelectMessagePreviews([]MessagePreview{
		{Text: "one"},
		{Text: "two"},
		{Text: "three"},
		{Text: "four"},
		{Text: "five"},
		{Text: "six"},
	}, PreviewOptions{UserMessagesPerEdge: 2, EdgeOffset: 2, MaxChars: 500})

	if len(got) != 2 {
		t.Fatalf("len = %d, want 2: %#v", len(got), got)
	}
	if got[0].Text != "three" || got[1].Text != "four" {
		t.Fatalf("previews = %#v", got)
	}
}

func TestSelectMessagePreviewsSkipsPriorEdgeOverlap(t *testing.T) {
	got := SelectMessagePreviews([]MessagePreview{
		{Text: "one"},
		{Text: "two"},
		{Text: "three"},
		{Text: "four"},
		{Text: "five"},
	}, PreviewOptions{UserMessagesPerEdge: 2, EdgeOffset: 2, MaxChars: 500})

	if len(got) != 1 {
		t.Fatalf("len = %d, want 1: %#v", len(got), got)
	}
	if got[0].Text != "three" {
		t.Fatalf("previews = %#v", got)
	}
}

func TestSelectMessagePreviewsFiltersByWindow(t *testing.T) {
	start := testTime(2)
	end := testTime(5)
	got := SelectMessagePreviews([]MessagePreview{
		{Text: "before", At: testTime(1)},
		{Text: "start", At: testTime(2)},
		{Text: "middle", At: testTime(3)},
		{Text: "end", At: testTime(5)},
		{Text: "undated"},
	}, PreviewOptions{UserMessagesPerEdge: 3, MaxChars: 500, Since: start, Before: end})

	if len(got) != 2 {
		t.Fatalf("len = %d, want 2: %#v", len(got), got)
	}
	if got[0].Text != "start" || got[1].Text != "middle" {
		t.Fatalf("previews = %#v", got)
	}
}

func TestNormalizePreviewTextCollapsesWhitespaceAndTruncatesRunes(t *testing.T) {
	got := NormalizePreviewText("你好\n 世界  abc", 4)
	if got != "你好 世" {
		t.Fatalf("text = %q", got)
	}
}

func testTime(hour int) time.Time {
	return time.Date(2026, 6, 17, hour, 0, 0, 0, time.UTC)
}
