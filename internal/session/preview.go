package session

import "strings"

const (
	DefaultPreviewMessagesPerEdge = 2
	DefaultPreviewMaxChars        = 500
)

func (opts PreviewOptions) Enabled() bool {
	return opts.UserMessagesPerEdge > 0 || opts.MaxChars > 0
}

func SelectMessagePreviews(messages []MessagePreview, opts PreviewOptions) []MessagePreview {
	if !opts.Enabled() {
		return nil
	}
	opts = opts.withDefaults()

	normalized := make([]MessagePreview, 0, len(messages))
	for _, message := range messages {
		if !previewInWindow(message, opts) {
			continue
		}
		message.Text = NormalizePreviewText(message.Text, opts.MaxChars)
		if message.Text != "" {
			normalized = append(normalized, message)
		}
	}
	if len(normalized) == 0 {
		return nil
	}

	seen := make(map[int]struct{}, opts.UserMessagesPerEdge*2)
	out := make([]MessagePreview, 0, min(len(normalized), opts.UserMessagesPerEdge*2))
	// EdgeOffset is an incremental cursor: skip content already covered by
	// earlier edge windows so agents can append only genuinely new context.
	priorLastStart := len(normalized) - opts.EdgeOffset
	for i := opts.EdgeOffset; i < len(normalized) && i < opts.EdgeOffset+opts.UserMessagesPerEdge; i++ {
		if i >= priorLastStart {
			continue
		}
		seen[i] = struct{}{}
		out = append(out, normalized[i])
	}
	start := len(normalized) - opts.EdgeOffset - opts.UserMessagesPerEdge
	if start < 0 {
		start = 0
	}
	end := len(normalized) - opts.EdgeOffset
	if end < 0 {
		end = 0
	}
	for i := start; i < end; i++ {
		if i < opts.EdgeOffset {
			continue
		}
		if _, ok := seen[i]; ok {
			continue
		}
		out = append(out, normalized[i])
	}
	return out
}

func NormalizePreviewText(text string, maxChars int) string {
	text = strings.Join(strings.Fields(text), " ")
	if text == "" {
		return ""
	}
	if maxChars <= 0 {
		maxChars = DefaultPreviewMaxChars
	}
	runes := []rune(text)
	if len(runes) <= maxChars {
		return text
	}
	return string(runes[:maxChars])
}

func (opts PreviewOptions) withDefaults() PreviewOptions {
	if opts.UserMessagesPerEdge <= 0 {
		opts.UserMessagesPerEdge = DefaultPreviewMessagesPerEdge
	}
	if opts.MaxChars <= 0 {
		opts.MaxChars = DefaultPreviewMaxChars
	}
	if opts.EdgeOffset < 0 {
		opts.EdgeOffset = 0
	}
	return opts
}

func previewInWindow(message MessagePreview, opts PreviewOptions) bool {
	if opts.Since.IsZero() && opts.Before.IsZero() {
		return true
	}
	// Report previews are evidence for a time window. If a provider cannot
	// date an individual message, do not let that undated text imply work
	// happened inside the requested period.
	if message.At.IsZero() {
		return false
	}
	if !opts.Since.IsZero() && message.At.Before(opts.Since) {
		return false
	}
	if !opts.Before.IsZero() && !message.At.Before(opts.Before) {
		return false
	}
	return true
}
