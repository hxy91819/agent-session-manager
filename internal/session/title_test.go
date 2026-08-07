package session

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestNormalizeTitle(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "empty"},
		{name: "whitespace unchanged", input: "  \n\t", want: "  \n\t"},
		{name: "ASCII unchanged", input: "normal title", want: "normal title"},
		{name: "Chinese unchanged", input: "修复启动性能", want: "修复启动性能"},
		{name: "emoji and combining unchanged", input: "🙂 cafe\u0301", want: "🙂 cafe\u0301"},
		{name: "multiline unchanged", input: "line one\nline  two", want: "line one\nline  two"},
		{name: "exact rune limit", input: strings.Repeat("a", MaxTitleRunes), want: strings.Repeat("a", MaxTitleRunes)},
		{name: "exact byte limit", input: strings.Repeat("🙂", MaxTitleBytes/4), want: strings.Repeat("🙂", MaxTitleBytes/4)},
		{name: "rune limit", input: strings.Repeat("a", MaxTitleRunes+1), want: strings.Repeat("a", MaxTitleRunes-1) + "…"},
		{name: "byte limit", input: strings.Repeat("🙂", MaxTitleRunes+1), want: strings.Repeat("🙂", MaxTitleRunes-1) + "…"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := tt.input
			if got := NormalizeTitle(tt.input); got != tt.want {
				t.Fatalf("NormalizeTitle() = %q, want %q", got, tt.want)
			}
			if tt.input != original {
				t.Fatal("input changed")
			}
		})
	}
}

func TestNormalizeTitleAlwaysReturnsValidBoundedUTF8(t *testing.T) {
	got := NormalizeTitle(strings.Repeat("🙂汉", 600) + string([]byte{0xff, 0xfe}))
	if !utf8.ValidString(got) {
		t.Fatalf("result is not valid UTF-8: %q", got)
	}
	if runes := utf8.RuneCountInString(got); runes > MaxTitleRunes {
		t.Fatalf("runes = %d, want <= %d", runes, MaxTitleRunes)
	}
	if bytes := len(got); bytes > MaxTitleBytes {
		t.Fatalf("bytes = %d, want <= %d", bytes, MaxTitleBytes)
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("result = %q, want ellipsis", got)
	}
}
