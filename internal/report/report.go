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
	Sessions  int            `json:"sessions"`
	Projects  int            `json:"projects"`
	Providers map[string]int `json:"providers"`
}

type Payload struct {
	Period       string            `json:"period"`
	Start        time.Time         `json:"start"`
	End          time.Time         `json:"end"`
	Timezone     string            `json:"timezone"`
	EvidenceRule string            `json:"evidence_rule"`
	Totals       Totals            `json:"totals"`
	Projects     []session.Project `json:"projects"`
	Sessions     []session.Session `json:"sessions"`
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
	windowSessions := withEvidence(FilterWindow(sessions, window.Start, window.End))
	projects := index.GroupProjects(windowSessions)
	return Payload{
		Period:       window.Period,
		Start:        window.Start,
		End:          window.End,
		Timezone:     window.Timezone,
		EvidenceRule: "Use sessions[].evidence as the only proof of work inside the report window; title, cwd, and path are labels only.",
		Totals: Totals{
			Sessions:  len(windowSessions),
			Projects:  len(projects),
			Providers: providerTotals(windowSessions),
		},
		Projects: projects,
		Sessions: windowSessions,
	}
}

func withEvidence(sessions []session.Session) []session.Session {
	for i := range sessions {
		if len(sessions[i].Previews) == 0 {
			continue
		}
		sessions[i].Evidence = append([]session.MessagePreview(nil), sessions[i].Previews...)
		sessions[i].EvidenceCount = len(sessions[i].Evidence)
	}
	return sessions
}

func FilterWindow(sessions []session.Session, start, end time.Time) []session.Session {
	out := make([]session.Session, 0, len(sessions))
	for _, item := range sessions {
		if item.UpdatedAt.Before(start) || !item.UpdatedAt.Before(end) {
			continue
		}
		out = append(out, item)
	}
	return out
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
