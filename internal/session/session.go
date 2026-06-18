package session

import "time"

type Session struct {
	ID        string            `json:"id"`
	Provider  string            `json:"provider"`
	CWD       string            `json:"cwd"`
	Title     string            `json:"title,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
	Path      string            `json:"path"`
	Metadata  map[string]string `json:"metadata,omitempty"`
	Previews  []MessagePreview  `json:"previews,omitempty"`
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

type ExecSpec struct {
	Dir  string   `json:"dir"`
	Args []string `json:"args"`
}

type Provider interface {
	Name() string
	Discover(opts DiscoverOptions) ([]Session, error)
	ResumeCommand(Session) ExecSpec
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
