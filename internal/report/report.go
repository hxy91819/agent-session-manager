package report

import (
	"fmt"
	"time"

	"github.com/hxy91819/agent-session-manager/internal/index"
	"github.com/hxy91819/agent-session-manager/internal/session"
)

const (
	PeriodToday     = "today"
	PeriodYesterday = "yesterday"
	PeriodLastWeek  = "last-week"
	PeriodLast7Days = "last-7-days"
	PeriodCustom    = "custom"
)

type Window struct {
	Period   string
	Start    time.Time
	End      time.Time
	Timezone string
}

type Totals struct {
	Sessions           int            `json:"sessions"`
	Projects           int            `json:"projects"`
	Providers          map[string]int `json:"providers"`
	UnverifiedSessions int            `json:"unverified_sessions"`
}

type ProviderCoverage struct {
	Status string `json:"status"`
	Note   string `json:"note"`
}

type UnverifiedSession struct {
	ID              string    `json:"id"`
	Provider        string    `json:"provider"`
	CWD             string    `json:"cwd,omitempty"`
	UpdatedAt       time.Time `json:"updated_at"`
	ReasonCode      string    `json:"reason_code"`
	MayHideUserWork bool      `json:"may_hide_user_work"`
	Reason          string    `json:"reason"`
}

type Payload struct {
	Period             string                      `json:"period"`
	Start              time.Time                   `json:"start"`
	End                time.Time                   `json:"end"`
	Timezone           string                      `json:"timezone"`
	EvidenceRule       string                      `json:"evidence_rule"`
	Totals             Totals                      `json:"totals"`
	Projects           []session.Project           `json:"projects"`
	Sessions           []session.Session           `json:"sessions"`
	Coverage           map[string]ProviderCoverage `json:"coverage,omitempty"`
	UnverifiedSessions []UnverifiedSession         `json:"unverified_sessions,omitempty"`
}

func WindowForPeriod(period string, now time.Time, loc *time.Location) (Window, error) {
	if loc == nil {
		loc = time.Local
	}
	today := localMidnight(now.In(loc))
	switch period {
	case PeriodToday:
		return Window{Period: period, Start: today, End: now.In(loc), Timezone: loc.String()}, nil
	case PeriodYesterday:
		start := today.AddDate(0, 0, -1)
		return Window{Period: period, Start: start, End: today, Timezone: loc.String()}, nil
	case PeriodLastWeek:
		weekStartOffset := (int(today.Weekday()) + 6) % 7
		end := today.AddDate(0, 0, -weekStartOffset)
		start := end.AddDate(0, 0, -7)
		return Window{Period: period, Start: start, End: end, Timezone: loc.String()}, nil
	case PeriodLast7Days:
		return Window{Period: period, Start: now.In(loc).AddDate(0, 0, -7), End: now.In(loc), Timezone: loc.String()}, nil
	default:
		return Window{}, fmt.Errorf("unsupported report period %q", period)
	}
}

func WindowForRange(startValue, endValue string, loc *time.Location) (Window, error) {
	if loc == nil {
		loc = time.Local
	}
	start, err := ParseBoundary(startValue, loc)
	if err != nil {
		return Window{}, fmt.Errorf("invalid report start %q: %w", startValue, err)
	}
	end, err := ParseBoundary(endValue, loc)
	if err != nil {
		return Window{}, fmt.Errorf("invalid report end %q: %w", endValue, err)
	}
	// Report windows are half-open so adjacent daily or weekly ranges can be
	// queried without double-counting sessions that land exactly on a boundary.
	if !start.Before(end) {
		return Window{}, fmt.Errorf("report start must be before end")
	}
	return Window{Period: PeriodCustom, Start: start, End: end, Timezone: loc.String()}, nil
}

func ParseBoundary(value string, loc *time.Location) (time.Time, error) {
	if loc == nil {
		loc = time.Local
	}
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"2006-01-02T15:04:05",
		"2006-01-02T15:04",
		"2006-01-02",
	}
	for _, layout := range layouts {
		var (
			t   time.Time
			err error
		)
		if layout == time.RFC3339Nano || layout == time.RFC3339 {
			t, err = time.Parse(layout, value)
		} else {
			t, err = time.ParseInLocation(layout, value, loc)
		}
		if err == nil {
			return t.In(loc), nil
		}
	}
	return time.Time{}, fmt.Errorf("use YYYY-MM-DD, local YYYY-MM-DD HH:MM[:SS], or RFC3339")
}

func BuildPayload(window Window, sessions []session.Session) Payload {
	windowSessions, unverified := partitionWindow(sessions, window.Start, window.End)
	windowSessions = withEvidence(windowSessions)
	projects := index.GroupProjects(windowSessions)
	return Payload{
		Period:       window.Period,
		Start:        window.Start,
		End:          window.End,
		Timezone:     window.Timezone,
		EvidenceRule: "Only sessions[].evidence proves work inside the report window. Session titles are omitted. unverified_sessions means a session file changed without an in-window timestamped user message; only entries with may_hide_user_work=true indicate a known source limitation.",
		Totals: Totals{
			Sessions:           len(windowSessions),
			Projects:           len(projects),
			Providers:          providerTotals(windowSessions),
			UnverifiedSessions: len(unverified),
		},
		Projects:           projects,
		Sessions:           windowSessions,
		Coverage:           providerCoverage(sessions),
		UnverifiedSessions: unverified,
	}
}

func withEvidence(sessions []session.Session) []session.Session {
	for i := range sessions {
		// Titles can originate outside the requested window. Omitting them from
		// report sessions prevents old prompts from becoming accidental evidence.
		sessions[i].Title = ""
		sessions[i].Evidence = nil
		sessions[i].EvidenceCount = 0
		if len(sessions[i].Previews) == 0 {
			continue
		}
		sessions[i].Evidence = append([]session.MessagePreview(nil), sessions[i].Previews...)
		sessions[i].EvidenceCount = len(sessions[i].Evidence)
	}
	return sessions
}

func FilterWindow(sessions []session.Session, start, end time.Time) []session.Session {
	verified, _ := partitionWindow(sessions, start, end)
	return verified
}

func partitionWindow(sessions []session.Session, start, end time.Time) ([]session.Session, []UnverifiedSession) {
	out := make([]session.Session, 0, len(sessions))
	var unverified []UnverifiedSession
	for _, item := range sessions {
		// Subagent threads inherit their parent's history. Keep them discoverable
		// and resumable, but do not count them as independent user work.
		if item.Metadata[session.MetadataParentThreadID] != "" {
			continue
		}
		previews := make([]session.MessagePreview, 0, len(item.Previews))
		for _, preview := range item.Previews {
			if preview.At.IsZero() || preview.At.Before(start) || !preview.At.Before(end) {
				continue
			}
			previews = append(previews, preview)
		}
		item.Previews = previews
		if len(item.Previews) > 0 {
			out = append(out, item)
			continue
		}
		if item.UpdatedAt.Before(start) || !item.UpdatedAt.Before(end) {
			continue
		}
		unverified = append(unverified, UnverifiedSession{
			ID:              item.ID,
			Provider:        item.Provider,
			CWD:             item.CWD,
			UpdatedAt:       item.UpdatedAt,
			ReasonCode:      unverifiedReasonCode(item),
			MayHideUserWork: providerMayHideUserWork(item),
			Reason:          unverifiedReason(item),
		})
	}
	return out, unverified
}

func unverifiedReason(item session.Session) string {
	if note := item.Metadata[session.MetadataReportEvidenceNote]; note != "" {
		return note
	}
	return "the transcript file changed in the report window, but parsing found no user-authored message whose original timestamp falls inside the window; this diagnostic is not itself a missing work item"
}

func unverifiedReasonCode(item session.Session) string {
	if providerMayHideUserWork(item) {
		return "provider_coverage_limit"
	}
	return "updated_without_in_window_user_message"
}

func providerMayHideUserWork(item session.Session) bool {
	switch item.Metadata[session.MetadataReportEvidenceStatus] {
	case session.ReportEvidencePartial, session.ReportEvidenceUnavailable:
		return true
	default:
		return false
	}
}

func providerCoverage(sessions []session.Session) map[string]ProviderCoverage {
	out := make(map[string]ProviderCoverage)
	for _, item := range sessions {
		status := item.Metadata[session.MetadataReportEvidenceStatus]
		if status == "" {
			continue
		}
		coverage := ProviderCoverage{
			Status: status,
			Note:   item.Metadata[session.MetadataReportEvidenceNote],
		}
		current, ok := out[item.Provider]
		if !ok || coverageRank(coverage.Status) > coverageRank(current.Status) {
			out[item.Provider] = coverage
		}
	}
	return out
}

func coverageRank(status string) int {
	switch status {
	case session.ReportEvidenceUnavailable:
		return 2
	case session.ReportEvidencePartial:
		return 1
	default:
		return 0
	}
}

func localMidnight(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

func providerTotals(sessions []session.Session) map[string]int {
	out := make(map[string]int)
	for _, item := range sessions {
		out[item.Provider]++
	}
	return out
}
