package session

import (
	"errors"
	"time"
)

const (
	MetadataReportEvidenceStatus = "report_evidence_status"
	MetadataReportEvidenceNote   = "report_evidence_note"
	MetadataParentThreadID       = "parent_thread_id"
	ReportEvidencePartial        = "partial"
	ReportEvidenceUnavailable    = "unavailable"
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

// Message is one normalized conversation turn extracted from a provider's
// native transcript. Text keeps the original wording; only surrounding blank
// space is trimmed so downstream analysis sees what the agent saw.
type Message struct {
	Role string    `json:"role"`
	Text string    `json:"text"`
	At   time.Time `json:"at,omitempty"`
	// Index is the message's stable position within the full transcript,
	// populated by show output so callers can cite and revisit exact turns.
	// No omitempty: index 0 is a valid citation and must round-trip.
	Index int `json:"index"`
	// MatchOffsets reports byte offsets of the --grep match in the original
	// message text. It is populated only for messages that matched a grep.
	MatchOffsets []MatchOffset `json:"match_offsets,omitempty"`
}

type MatchOffset struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// Transcript pairs the normalized session header with its full message flow.
type Transcript struct {
	Session  Session
	Messages []Message
}

// ErrSessionNotFound is returned by TranscriptReader implementations when the
// id does not resolve to any stored session.
var ErrSessionNotFound = errors.New("session not found")

// TranscriptReader is an optional provider capability for reading the full
// message flow of one session by id. Providers whose stores cannot support
// cheap single-session reads simply do not implement it; callers must type
// assert instead of assuming it.
type TranscriptReader interface {
	ReadTranscript(id string) (Transcript, error)
}

// SelectedTranscriptReader lets a provider reuse the exact source location
// returned by discovery instead of searching its entire store by id.
type SelectedTranscriptReader interface {
	ReadSelectedTranscript(selected Session) (Transcript, error)
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
