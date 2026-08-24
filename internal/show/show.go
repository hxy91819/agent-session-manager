// Package show builds bounded, self-describing single-session transcript
// output for agent consumers. The envelope always reports how many messages
// exist, how many matched the requested filters, and how many came back.
package show

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hxy91819/agent-session-manager/internal/session"
)

const (
	RoleUser            = "user"
	RoleAssistant       = "assistant"
	DefaultTailMessages = 50
	DefaultMaxChars     = 2000
	DefaultSummaryChars = 200
)

type Options struct {
	Role          string
	Grep          string
	Regex         bool
	Exact         bool
	CaseSensitive bool
	Before        int
	After         int
	First         int
	Last          int
	Offset        int
	FromIndex     int
	MaxChars      int
	Summary       bool
	SummaryChars  int
	Full          bool
}

type filters struct {
	Role          string `json:"role,omitempty"`
	Grep          string `json:"grep,omitempty"`
	Regex         bool   `json:"regex,omitempty"`
	Exact         bool   `json:"exact,omitempty"`
	CaseSensitive bool   `json:"case_sensitive,omitempty"`
	Before        int    `json:"before,omitempty"`
	After         int    `json:"after,omitempty"`
	Head          int    `json:"head,omitempty"`
	Tail          int    `json:"tail,omitempty"`
	Offset        int    `json:"offset,omitempty"`
	FromIndex     int    `json:"from_index,omitempty"`
	Summary       bool   `json:"summary,omitempty"`
}

// Error is the stable machine-readable error returned by asm show.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e Error) Error() string { return e.Message }

func NewError(code, message string) error { return Error{Code: code, Message: message} }

type Output struct {
	ID               string            `json:"id"`
	Provider         string            `json:"provider"`
	CWD              string            `json:"cwd,omitempty"`
	Title            string            `json:"title,omitempty"`
	CreatedAt        time.Time         `json:"created_at,omitempty"`
	UpdatedAt        time.Time         `json:"updated_at,omitempty"`
	Path             string            `json:"path,omitempty"`
	Metadata         map[string]string `json:"metadata,omitempty"`
	TotalMessages    int               `json:"total_messages"`
	MatchedMessages  int               `json:"matched_messages"`
	ReturnedMessages int               `json:"returned_messages"`
	Truncated        bool              `json:"truncated"`
	TextCutMessages  int               `json:"text_cut_messages,omitempty"`
	Filters          filters           `json:"filters"`
	Summary          bool              `json:"summary,omitempty"`
	Messages         []session.Message `json:"messages"`
}

func (o Options) Validate() error {
	switch o.Role {
	case "", RoleUser, RoleAssistant:
	default:
		return fmt.Errorf("role must be %q or %q", RoleUser, RoleAssistant)
	}
	if o.First > 0 && o.Last > 0 {
		return fmt.Errorf("first and last cannot be used together")
	}
	if o.Exact && o.Regex {
		return fmt.Errorf("exact and regex cannot be used together")
	}
	if o.Before < 0 || o.After < 0 || o.First < 0 || o.Last < 0 || o.Offset < 0 || o.FromIndex < 0 || o.MaxChars < 0 || o.SummaryChars < 0 {
		return fmt.Errorf("before, after, first, last, offset, from-index, max-chars, and summary-chars must be >= 0")
	}
	if (o.Before > 0 || o.After > 0) && o.Grep == "" {
		return fmt.Errorf("before and after require --grep")
	}
	if o.Grep != "" && o.Regex {
		pattern := o.Grep
		if !o.CaseSensitive {
			pattern = "(?i)" + pattern
		}
		if _, err := regexp.Compile(pattern); err != nil {
			return fmt.Errorf("invalid grep regex: %w", err)
		}
	}
	if o.Grep != "" && !o.Regex && o.MaxChars > 0 && utf8.RuneCountInString(o.Grep)+2 > o.MaxChars && !o.Exact {
		return fmt.Errorf("max-chars must be at least the grep term length plus excerpt markers")
	}
	return nil
}

func (o Options) maxChars() int {
	if o.Summary {
		if o.SummaryChars > 0 {
			return o.SummaryChars
		}
		return DefaultSummaryChars
	}
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

func Build(t session.Transcript, opts Options) Output {
	out := Output{ID: t.Session.ID, Provider: t.Session.Provider, CWD: t.Session.CWD, Title: t.Session.Title,
		CreatedAt: t.Session.CreatedAt, UpdatedAt: t.Session.UpdatedAt, Path: t.Session.Path, Metadata: t.Session.Metadata,
		TotalMessages: len(t.Messages), Summary: opts.Summary,
		Filters: filters{Role: opts.Role, Grep: opts.Grep, Regex: opts.Regex, Exact: opts.Exact, CaseSensitive: opts.CaseSensitive,
			Before: opts.Before, After: opts.After, Offset: opts.Offset, FromIndex: opts.FromIndex, Summary: opts.Summary}}
	match := compileMatcher(opts)
	maxChars := opts.maxChars()
	matched := make([]session.Message, 0, len(t.Messages))
	for i, original := range t.Messages {
		msg := original
		msg.Index = i
		if opts.Role != "" && msg.Role != opts.Role {
			continue
		}
		if offsets := matchOffsets(msg.Text, match, opts); match != nil && len(offsets) == 0 {
			continue
		} else if len(offsets) > 0 {
			msg.MatchOffsets = offsets
			if !opts.Summary && maxChars > 0 && utf8.RuneCountInString(msg.Text) > maxChars && !opts.Exact {
				msg.Text = grepExcerpt(msg.Text, offsets[0].Start, maxChars)
			}
		}
		matched = append(matched, msg)
	}
	out.MatchedMessages = len(matched)
	head, tail := opts.window()
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
	if opts.FromIndex > 0 {
		before := len(selected)
		selected = filterFromIndex(selected, opts.FromIndex)
		if len(selected) != before {
			out.Truncated = true
		}
	}
	if opts.Before > 0 || opts.After > 0 {
		selected = withContext(selected, opts.Before, opts.After, t.Messages)
	}
	if opts.Offset > 0 {
		if opts.Offset >= len(selected) {
			selected = nil
			out.Truncated = true
		} else {
			selected = selected[opts.Offset:]
			out.Truncated = true
		}
	}
	for i := range selected {
		if maxChars <= 0 || utf8.RuneCountInString(selected[i].Text) <= maxChars {
			continue
		}
		selected[i].Text = truncateRunes(selected[i].Text, maxChars)
		out.TextCutMessages++
		out.Truncated = true
	}
	out.ReturnedMessages = len(selected)
	out.Messages = selected
	return out
}

type compiledMatcher struct {
	substring string
	exact     bool
	re        *regexp.Regexp
}

func compileMatcher(o Options) *compiledMatcher {
	if o.Grep == "" {
		return nil
	}
	if o.Regex {
		p := o.Grep
		if !o.CaseSensitive {
			p = "(?i)" + p
		}
		return &compiledMatcher{re: regexp.MustCompile(p)}
	}
	term := o.Grep
	if !o.CaseSensitive {
		term = strings.ToLower(term)
	}
	return &compiledMatcher{substring: term, exact: o.Exact}
}
func matchOffsets(text string, m *compiledMatcher, o Options) []session.MatchOffset {
	if m == nil {
		return nil
	}
	if m.re != nil {
		indexes := m.re.FindAllStringIndex(text, -1)
		out := make([]session.MatchOffset, 0, len(indexes))
		for _, x := range indexes {
			out = append(out, session.MatchOffset{Start: x[0], End: x[1]})
		}
		return out
	}
	candidate := text
	if !o.CaseSensitive {
		candidate = strings.ToLower(text)
	}
	if m.exact {
		if candidate != m.substring {
			return nil
		}
		return []session.MatchOffset{{Start: 0, End: len(text)}}
	}
	var out []session.MatchOffset
	start := 0
	for {
		i := strings.Index(candidate[start:], m.substring)
		if i < 0 {
			break
		}
		i += start
		out = append(out, session.MatchOffset{Start: i, End: i + len(m.substring)})
		start = i + len(m.substring)
		if start >= len(candidate) {
			break
		}
	}
	return out
}
func filterFromIndex(items []session.Message, index int) []session.Message {
	out := items[:0]
	for _, m := range items {
		if m.Index >= index {
			out = append(out, m)
		}
	}
	return out
}
func withContext(selected []session.Message, before, after int, all []session.Message) []session.Message {
	keep := map[int]bool{}
	selectedByIndex := make(map[int]session.Message, len(selected))
	for _, m := range selected {
		keep[m.Index] = true
		selectedByIndex[m.Index] = m
		for i := m.Index - before; i <= m.Index+after; i++ {
			if i >= 0 && i < len(all) {
				keep[i] = true
			}
		}
	}
	out := make([]session.Message, 0, len(keep))
	for i, m := range all {
		if !keep[i] {
			continue
		}
		if selected, ok := selectedByIndex[i]; ok {
			out = append(out, selected)
			continue
		}
		m.Index = i
		out = append(out, m)
	}
	return out
}
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
	prefix, suffix := "", ""
	if start > 0 {
		prefix = "…"
	}
	if end < len(runes) {
		suffix = "…"
	}
	return prefix + string(runes[start:end]) + suffix
}

func truncateRunes(text string, maxChars int) string {
	runes := []rune(text)
	if maxChars <= 0 || len(runes) <= maxChars {
		return text
	}
	return string(runes[:maxChars]) + "…"
}

// Render returns the public output in one of the supported wire formats. The
// default JSON object is intentionally unchanged; compact only removes
// whitespace, while jsonl emits one metadata record followed by message rows.
func Render(out Output, format string, fields []string) ([]byte, error) {
	if format != "" && format != "json" && format != "compact" && format != "jsonl" {
		return nil, fmt.Errorf("format must be json, compact, or jsonl")
	}
	value := selectedFields(out, fields)
	if format != "jsonl" && len(fields) == 0 {
		data, err := json.Marshal(out)
		if err != nil {
			return nil, err
		}
		if format == "" || format == "json" {
			var b bytes.Buffer
			if err := json.Indent(&b, data, "", "  "); err != nil {
				return nil, err
			}
			b.WriteByte('\n')
			return b.Bytes(), nil
		}
		return append(data, '\n'), nil
	}
	if format == "jsonl" {
		meta := map[string]any{"type": "meta"}
		for k, v := range value {
			if k != "messages" {
				meta[k] = v
			}
		}
		var b strings.Builder
		enc := json.NewEncoder(&b)
		if err := enc.Encode(meta); err != nil {
			return nil, err
		}
		for _, msg := range out.Messages {
			row := map[string]any{"type": "message", "message": msg}
			if _, ok := value["id"]; ok {
				row["id"] = out.ID
			}
			if _, ok := value["provider"]; ok {
				row["provider"] = out.Provider
			}
			if err := enc.Encode(row); err != nil {
				return nil, err
			}
		}
		return []byte(b.String()), nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if format == "" || format == "json" {
		var b bytes.Buffer
		if err := json.Indent(&b, data, "", "  "); err != nil {
			return nil, err
		}
		b.WriteByte('\n')
		return b.Bytes(), nil
	}
	data = append(data, '\n')
	return data, nil
}
func selectedFields(out Output, fields []string) map[string]any {
	all := map[string]any{"id": out.ID, "provider": out.Provider, "cwd": out.CWD, "title": out.Title, "created_at": out.CreatedAt, "updated_at": out.UpdatedAt, "path": out.Path, "metadata": out.Metadata, "total_messages": out.TotalMessages, "matched_messages": out.MatchedMessages, "returned_messages": out.ReturnedMessages, "truncated": out.Truncated, "text_cut_messages": out.TextCutMessages, "filters": out.Filters, "summary": out.Summary, "messages": out.Messages}
	if len(fields) == 0 {
		return all
	}
	result := make(map[string]any, len(fields))
	for _, field := range fields {
		if v, ok := all[field]; ok {
			result[field] = v
		}
	}
	return result
}
