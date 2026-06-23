package report

import (
	"testing"
	"time"

	"github.com/hxy91819/agent-session-manager/internal/session"
)

func TestWindowForPeriodYesterdayUsesLocalNaturalDay(t *testing.T) {
	loc := time.FixedZone("Local", 8*60*60)
	now := time.Date(2026, 6, 18, 15, 30, 0, 0, loc)

	got, err := WindowForPeriod(PeriodYesterday, now, loc)
	if err != nil {
		t.Fatal(err)
	}
	assertTime(t, got.Start, time.Date(2026, 6, 17, 0, 0, 0, 0, loc))
	assertTime(t, got.End, time.Date(2026, 6, 18, 0, 0, 0, 0, loc))
}

func TestWindowForPeriodTodayUsesLocalMidnightToNow(t *testing.T) {
	loc := time.FixedZone("Local", 8*60*60)
	now := time.Date(2026, 6, 18, 15, 30, 0, 0, loc)

	got, err := WindowForPeriod(PeriodToday, now, loc)
	if err != nil {
		t.Fatal(err)
	}
	assertTime(t, got.Start, time.Date(2026, 6, 18, 0, 0, 0, 0, loc))
	assertTime(t, got.End, now)
}

func TestWindowForPeriodLastWeekUsesMondayStart(t *testing.T) {
	loc := time.FixedZone("Local", 8*60*60)
	now := time.Date(2026, 6, 18, 15, 30, 0, 0, loc)

	got, err := WindowForPeriod(PeriodLastWeek, now, loc)
	if err != nil {
		t.Fatal(err)
	}
	assertTime(t, got.Start, time.Date(2026, 6, 8, 0, 0, 0, 0, loc))
	assertTime(t, got.End, time.Date(2026, 6, 15, 0, 0, 0, 0, loc))
}

func TestWindowForPeriodLast7DaysUsesRollingWindow(t *testing.T) {
	loc := time.FixedZone("Local", 8*60*60)
	now := time.Date(2026, 6, 18, 15, 30, 0, 0, loc)

	got, err := WindowForPeriod(PeriodLast7Days, now, loc)
	if err != nil {
		t.Fatal(err)
	}
	assertTime(t, got.Start, time.Date(2026, 6, 11, 15, 30, 0, 0, loc))
	assertTime(t, got.End, now)
}

func TestWindowForRangeParsesLocalDateAndTime(t *testing.T) {
	loc := time.FixedZone("Local", 8*60*60)

	got, err := WindowForRange("2026-06-17", "2026-06-18 18:30", loc)
	if err != nil {
		t.Fatal(err)
	}
	if got.Period != PeriodCustom {
		t.Fatalf("period = %q, want custom", got.Period)
	}
	assertTime(t, got.Start, time.Date(2026, 6, 17, 0, 0, 0, 0, loc))
	assertTime(t, got.End, time.Date(2026, 6, 18, 18, 30, 0, 0, loc))
}

func TestWindowForRangeRejectsEmptyOrInvertedRange(t *testing.T) {
	loc := time.FixedZone("Local", 8*60*60)

	if _, err := WindowForRange("2026-06-18", "2026-06-18", loc); err == nil {
		t.Fatal("expected equal boundaries to fail")
	}
	if _, err := WindowForRange("2026-06-19", "2026-06-18", loc); err == nil {
		t.Fatal("expected inverted boundaries to fail")
	}
}

func TestParseBoundaryParsesRFC3339IntoLocalTimezone(t *testing.T) {
	loc := time.FixedZone("Local", 8*60*60)

	got, err := ParseBoundary("2026-06-17T16:00:00Z", loc)
	if err != nil {
		t.Fatal(err)
	}
	assertTime(t, got, time.Date(2026, 6, 18, 0, 0, 0, 0, loc))
}

func TestBuildPayloadFiltersWindowAndCountsTotals(t *testing.T) {
	loc := time.FixedZone("Local", 8*60*60)
	start := time.Date(2026, 6, 17, 0, 0, 0, 0, loc)
	end := time.Date(2026, 6, 18, 0, 0, 0, 0, loc)
	payload := BuildPayload(Window{
		Period:   PeriodYesterday,
		Start:    start,
		End:      end,
		Timezone: loc.String(),
	}, []session.Session{
		{ID: "before", Provider: "codex", CWD: "/repo/a", UpdatedAt: start.Add(-time.Nanosecond)},
		{ID: "middle", Provider: "claude", CWD: "/repo/b", UpdatedAt: start.Add(time.Hour), Previews: []session.MessagePreview{
			{Text: "middle evidence", At: start.Add(time.Hour)},
		}},
		{ID: "start", Provider: "codex", CWD: "/repo/a", UpdatedAt: start, Previews: []session.MessagePreview{
			{Text: "start evidence", At: start},
			{Text: "start follow-up", At: start.Add(time.Minute)},
		}},
		{ID: "end", Provider: "kimi", CWD: "/repo/c", UpdatedAt: end},
	})

	if payload.EvidenceRule == "" {
		t.Fatal("evidence rule should explain report evidence semantics")
	}
	if payload.Totals.Sessions != 2 {
		t.Fatalf("sessions = %d, want 2", payload.Totals.Sessions)
	}
	if payload.Totals.Projects != 2 {
		t.Fatalf("projects = %d, want 2", payload.Totals.Projects)
	}
	if payload.Totals.Providers["codex"] != 1 || payload.Totals.Providers["claude"] != 1 {
		t.Fatalf("providers = %#v", payload.Totals.Providers)
	}
	if len(payload.Sessions) != 2 || payload.Sessions[0].ID != "middle" || payload.Sessions[1].ID != "start" {
		t.Fatalf("sessions = %#v", payload.Sessions)
	}
	if payload.Sessions[0].EvidenceCount != 1 || payload.Sessions[0].Evidence[0].Text != "middle evidence" {
		t.Fatalf("middle evidence = %#v", payload.Sessions[0].Evidence)
	}
	if payload.Sessions[1].EvidenceCount != 2 || payload.Sessions[1].Evidence[1].Text != "start follow-up" {
		t.Fatalf("start evidence = %#v", payload.Sessions[1].Evidence)
	}
	if payload.Projects[0].Sessions[0].EvidenceCount == 0 && payload.Projects[1].Sessions[0].EvidenceCount == 0 {
		t.Fatalf("project sessions should carry report evidence: %#v", payload.Projects)
	}
}

func assertTime(t *testing.T, got, want time.Time) {
	t.Helper()
	if !got.Equal(want) {
		t.Fatalf("time = %s, want %s", got, want)
	}
}
