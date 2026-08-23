// Package show builds bounded, self-describing single-session transcript
// output for agent consumers. The envelope always reports how many messages
// exist, how many matched the requested filters, and how many came back, so a
// calling agent knows what it did not see and can refine its next query
// instead of needing an unbounded dump.
package show

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hxy91819/agent-session-manager/internal/session"
)

const (
	// RoleUser and RoleAssistant are the accepted --role values.
	RoleUser      = "user"
	RoleAssistant = "assistant"

	// Defaults keep the no-flag invocation bounded: agents start from the
	// recent tail of a conversation and widen deliberately.
	DefaultTailMessages = 50
	DefaultMaxChars     = 2000
)

// Options controls which part of a transcript comes back. The zero value asks
// for the bounded default view: the last DefaultTailMessages messages with
// per-message text capped at DefaultMaxChars.
type Options struct {
	Role     string // "", "user", "assistant"
	Grep     string // case-insensitive substring on message text
	First    int    // keep the first N matching messages
	Last     int    // keep the last N matching messages
	MaxChars int    // per-message text cap
	Full     bool   // return every message without the default caps
}

type filters struct {
	Role string `json:"role,omitempty"`
	Grep string `json:"grep,omitempty"`
	Head int    `json:"head,omitempty"`
	Tail int    `json:"tail,omitempty"`
}

// Output is the machine-readable envelope. Truncation bookkeeping
// (total/matched/returned plus truncated flags) is the contract: consumers
// must be able to tell whether they saw the whole conversation.
type Output struct {
	ID        string            `json:"id"`
	Provider  string            `json:"provider"`
	CWD       string            `json:"cwd,omitempty"`
	Title     string            `json:"title,omitempty"`
	CreatedAt time.Time         `json:"created_at,omitempty"`
	UpdatedAt time.Time         `json:"updated_at,omitempty"`
	Path      string            `json:"path,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`

	TotalMessages    int  `json:"total_messages"`
	MatchedMessages  int  `json:"matched_messages"`
	ReturnedMessages int  `json:"returned_messages"`
	Truncated        bool `json:"truncated"`
	TextCutMessages  int  `json:"text_cut_messages,omitempty"`

	Filters  filters           `json:"filters"`
	Messages []session.Message `json:"messages"`
}

// Validate rejects unsupported option combinations before any store access.
func (o Options) Validate() error {
	switch o.Role {
	case "", RoleUser, RoleAssistant:
	default:
		return fmt.Errorf("role must be %q or %q", RoleUser, RoleAssistant)
	}
	if o.First > 0 && o.Last > 0 {
		return fmt.Errorf("first and last cannot be used together")
	}
	if o.First < 0 || o.Last < 0 || o.MaxChars < 0 {
		return fmt.Errorf("first, last, and max-chars must be >= 0")
	}
	if o.Grep != "" && o.MaxChars > 0 && o.MaxChars < utf8.RuneCountInString(o.Grep)+2 {
		return fmt.Errorf("max-chars must be at least the grep term length plus excerpt markers")
	}
	return nil
}

func (o Options) maxChars() int {
	if o.MaxChars > 0 {
		return o.MaxChars
	}
	if o.Full {
		return 0
	}
	return DefaultMaxChars
}

func (o Options) window() (head, tail int) {
	if o.First > 0 {
		return o.First, 0
	}
	if o.Last > 0 {
		return 0, o.Last
	}
	if o.Full {
		return 0, 0
	}
	return 0, DefaultTailMessages
}

// Build applies the filters to a transcript and produces the envelope. It is
// pure so tests can pin filter and truncation semantics without stores.
func Build(t session.Transcript, opts Options) Output {
	out := Output{
		ID:        t.Session.ID,
		Provider:  t.Session.Provider,
		CWD:       t.Session.CWD,
		Title:     t.Session.Title,
		CreatedAt: t.Session.CreatedAt,
		UpdatedAt: t.Session.UpdatedAt,
		Path:      t.Session.Path,
		Metadata:  t.Session.Metadata,
		Filters: filters{
			Role: opts.Role,
			Grep: opts.Grep,
		},
		TotalMessages: len(t.Messages),
	}

	maxChars := opts.maxChars()
	head, tail := opts.window()
	grep := strings.ToLower(opts.Grep)

	matched := make([]session.Message, 0, len(t.Messages))
	for i, msg := range t.Messages {
		msg.Index = i
		if opts.Role != "" && msg.Role != opts.Role {
			continue
		}
		text := msg.Text
		if grep != "" {
			index := strings.Index(strings.ToLower(text), grep)
			if index < 0 {
				continue
			}
			if maxChars > 0 && utf8.RuneCountInString(text) > maxChars {
				// Shorten grep-matched oversized messages down to a focused
				// excerpt; an exact-window match would otherwise be dropped
				// by the per-message cap even though the caller searched
				// for it.
				msg.Text = grepExcerpt(text, index, maxChars)
			}
		}
		matched = append(matched, msg)
	}
	out.MatchedMessages = len(matched)
	out.Filters.Head = head
	out.Filters.Tail = tail

	selected := matched
	if head > 0 && len(selected) > head {
		selected = selected[:head]
		out.Truncated = true
	}
	if tail > 0 && len(selected) > tail {
		selected = selected[len(selected)-tail:]
		out.Truncated = true
	}
	if maxChars > 0 {
		for i, msg := range selected {
			if utf8.RuneCountInString(msg.Text) <= maxChars {
				continue
			}
			selected[i].Text = truncateRunes(msg.Text, maxChars)
			out.TextCutMessages++
			out.Truncated = true
		}
	}
	out.ReturnedMessages = len(selected)
	out.Messages = selected
	return out
}

// grepExcerpt keeps a window around the match instead of the message head so
// the caller sees the context they asked for. The whole excerpt, including
// the ellipses, stays within maxChars so later caps never re-cut it.
func grepExcerpt(text string, matchIndex, maxChars int) string {
	runes := []rune(text)
	matchIndex = len([]rune(text[:matchIndex]))
	width := maxChars - 2
	if width < 1 {
		width = 1
	}
	if width > len(runes) {
		width = len(runes)
	}
	start := matchIndex - width/3
	if start < 0 {
		start = 0
	}
	end := start + width
	if end > len(runes) {
		end = len(runes)
		start = end - width
		if start < 0 {
			start = 0
		}
	}
	excerpt := string(runes[start:end])
	prefix := ""
	suffix := ""
	if start > 0 {
		prefix = "…"
	}
	if end < len(runes) {
		suffix = "…"
	}
	return prefix + excerpt + suffix
}

func truncateRunes(text string, maxChars int) string {
	runes := []rune(text)
	return string(runes[:maxChars]) + "…"
}
