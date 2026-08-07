package session

import (
	"strings"
	"unicode/utf8"
)

const (
	MaxTitleRunes = 512
	MaxTitleBytes = 2048
	titleEllipsis = "…"
)

// NormalizeTitle bounds the shared display/search value so provider transcripts
// cannot amplify cache, JSON, index, and UI costs through an unbounded title.
func NormalizeTitle(title string) string {
	if !utf8.ValidString(title) {
		title = strings.ToValidUTF8(title, "�")
	}
	if len(title) <= MaxTitleRunes {
		return title
	}
	if len(title) <= MaxTitleBytes && utf8.RuneCountInString(title) <= MaxTitleRunes {
		return title
	}

	maxContentRunes := MaxTitleRunes - utf8.RuneCountInString(titleEllipsis)
	maxContentBytes := MaxTitleBytes - len(titleEllipsis)
	contentRunes := 0
	contentBytes := 0
	end := 0
	for offset, r := range title {
		runeBytes := utf8.RuneLen(r)
		if contentRunes == maxContentRunes || contentBytes+runeBytes > maxContentBytes {
			break
		}
		contentRunes++
		contentBytes += runeBytes
		end = offset + runeBytes
	}
	return title[:end] + titleEllipsis
}
