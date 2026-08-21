package session

import (
	"strings"
	"time"
	"unicode/utf8"
)

const (
	MetadataReportEvidenceStatus = "report_evidence_status"
	MetadataReportEvidenceNote   = "report_evidence_note"
	MetadataParentThreadID       = "parent_thread_id"
	ReportEvidencePartial        = "partial"
	ReportEvidenceUnavailable    = "unavailable"
)

const (
	// SearchMessageMaxBytes caps one user message inside SearchMessages. Real
	// prompts are short; the cap keeps pasted log dumps findable by their
	// opening lines without letting one paste crowd out later user decisions.
	SearchMessageMaxBytes = 4 * 1024
	// SearchContentMaxBytes bounds the per-session searchable text so a large
	// session store stays cheap to hold in memory and persist in the cache.
	SearchContentMaxBytes = 32 * 1024
	// SearchMessageMaxCount additionally bounds the message count so even
	// tiny-message floods stay small in memory and cache.
	SearchMessageMaxCount = 512
)

type Session struct {
	ID               string            `json:"id"`
	Provider         string            `json:"provider"`
	CWD              string            `json:"cwd"`
	Title            string            `json:"title,omitempty"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
	Path             string            `json:"path"`
	Metadata         map[string]string `json:"metadata,omitempty"`
	Previews         []MessagePreview  `json:"previews,omitempty"`
	RuntimeLocations []RuntimeLocation `json:"runtime_locations,omitempty"`
	// Evidence is populated by report output only. It duplicates the in-window
	// user previews under a decision-oriented name so report agents do not treat
	// stale session titles as proof of work in the requested period.
	Evidence      []MessagePreview `json:"evidence,omitempty"`
	EvidenceCount int              `json:"evidence_count,omitempty"`
	// ResumeCommand is a user-facing asm command, populated only for report
	// output so agents can hand users a precise way back into a session.
	ResumeCommand string `json:"resume_command,omitempty"`
	// SearchMessages is the bounded "key content" of a session: the denoised
	// text of every user-authored message (asks, corrections, and added
	// constraints), with injected contexts and tool output already filtered
	// by the provider. Each entry carries its timestamp and the byte offset
	// of its record in the raw session file, so search consumers can both
	// match against the text and slice the original transcript without
	// re-parsing provider-specific formats. Providers build it during their
	// primary session parse so internal/sessioncache persists it across
	// discovery passes. It participates in query matching via internal/index
	// but is stripped from user-facing JSON output so --json and report
	// payloads stay lean.
	SearchMessages []SearchMessage `json:"search_messages,omitempty"`
}

// SearchMessage is one denoised user-authored message kept for search.
type SearchMessage struct {
	Text   string    `json:"text"`
	At     time.Time `json:"at,omitempty"`
	Offset int64     `json:"offset"`
}

// AppendSearchMessage appends one denoised user message to the search
// corpus. The text is trimmed and truncated at a rune boundary to
// SearchMessageMaxBytes; the corpus stops growing once it holds
// SearchMessageMaxCount messages or SearchContentMaxBytes of text. It
// reports full=true once either cap is reached so incremental parsers can
// skip further work.
func AppendSearchMessage(corpus []SearchMessage, message SearchMessage) (next []SearchMessage, full bool) {
	if len(corpus) >= SearchMessageMaxCount || searchCorpusBytes(corpus) >= SearchContentMaxBytes {
		return corpus, true
	}
	message.Text = truncateUTF8(strings.TrimSpace(message.Text), SearchMessageMaxBytes)
	if message.Text == "" {
		return corpus, false
	}
	remaining := SearchContentMaxBytes - searchCorpusBytes(corpus)
	if len(message.Text) > remaining {
		message.Text = truncateUTF8(message.Text, remaining)
	}
	corpus = append(corpus, message)
	return corpus, len(corpus) >= SearchMessageMaxCount || searchCorpusBytes(corpus) >= SearchContentMaxBytes
}

func searchCorpusBytes(corpus []SearchMessage) int {
	total := 0
	for _, message := range corpus {
		total += len(message.Text)
	}
	return total
}

// StripSearchCorpus returns sessions with their internal search corpus
// removed. SearchMessages exists for query matching only; user-facing JSON
// and report payloads must stay lean and free of transcript excerpts.
func StripSearchCorpus(sessions []Session) []Session {
	for i := range sessions {
		sessions[i].SearchMessages = nil
	}
	return sessions
}

func truncateUTF8(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	value = value[:maxBytes]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

type Project struct {
	CWD      string    `json:"cwd"`
	Count    int       `json:"count"`
	Updated  time.Time `json:"updated"`
	Sessions []Session `json:"sessions,omitempty"`
}

type ProviderError struct {
	Provider string `json:"provider"`
	Error    string `json:"error"`
}

type DiscoveryResult struct {
	Sessions       []Session
	ProviderErrors []ProviderError
	RuntimeErrors  []RuntimeError
}

type RuntimeLocation struct {
	Runtime     string `json:"runtime"`
	WorkspaceID string `json:"workspace_id"`
	TabID       string `json:"tab_id"`
	PaneID      string `json:"pane_id"`
	AgentStatus string `json:"agent_status,omitempty"`
}

type RuntimeError struct {
	Runtime string `json:"runtime"`
	Error   string `json:"error"`
}

type ExecSpec struct {
	Dir               string            `json:"dir"`
	Args              []string          `json:"args"`
	Env               map[string]string `json:"env,omitempty"`
	UnsupportedReason string            `json:"unsupported_reason,omitempty"`
}

type Provider interface {
	Name() string
	Discover(opts DiscoverOptions) ([]Session, error)
	ResumeCommand(Session) ExecSpec
	NewCommand(cwd string) ExecSpec
}

type DiscoverOptions struct {
	LimitFiles int
	Since      time.Time
	Preview    PreviewOptions
}

type MessagePreview struct {
	Text   string    `json:"text"`
	At     time.Time `json:"at,omitempty"`
	Source string    `json:"source,omitempty"`
}

type PreviewOptions struct {
	UserMessagesPerEdge int
	MaxChars            int
	EdgeOffset          int
	Since               time.Time
	Before              time.Time
}
