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
	Period   string            `json:"period"`
	Start    time.Time         `json:"start"`
	End      time.Time         `json:"end"`
	Timezone string            `json:"timezone"`
	Totals   Totals            `json:"totals"`
	Projects []session.Project `json:"projects"`
	Sessions []session.Session `json:"sessions"`
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

func BuildPayload(window Window, sessions []session.Session) Payload {
	windowSessions := FilterWindow(sessions, window.Start, window.End)
	projects := index.GroupProjects(windowSessions)
	return Payload{
		Period:   window.Period,
		Start:    window.Start,
		End:      window.End,
		Timezone: window.Timezone,
		Totals: Totals{
			Sessions:  len(windowSessions),
			Projects:  len(projects),
			Providers: providerTotals(windowSessions),
		},
		Projects: projects,
		Sessions: windowSessions,
	}
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
