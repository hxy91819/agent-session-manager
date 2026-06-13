package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"session-manager/internal/session"
)

func TestModelSelectsMostRecentSessionByDefault(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	m := New([]session.Session{
		{ID: "old", CWD: "/repo", UpdatedAt: base, CreatedAt: base},
		{ID: "new", CWD: "/repo", UpdatedAt: base.Add(time.Hour), CreatedAt: base},
	})

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got, ok := next.(Model).Selected()

	if !ok {
		t.Fatal("expected a selected session")
	}
	if got.ID != "new" {
		t.Fatalf("selected %q, want new", got.ID)
	}
}

func TestModelSearchFiltersSessions(t *testing.T) {
	m := New([]session.Session{
		{ID: "one", CWD: "/repo/openclaw", Title: "review", UpdatedAt: time.Now()},
		{ID: "two", CWD: "/repo/lighthouse", Title: "deploy", UpdatedAt: time.Now()},
	})

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = next.(Model)
	for _, r := range "light" {
		next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = next.(Model)
	}

	if len(m.sessions) != 1 || m.sessions[0].ID != "two" {
		t.Fatalf("filtered sessions = %#v", m.sessions)
	}
	if len(m.projects) != 1 || m.projects[0].CWD != "/repo/lighthouse" {
		t.Fatalf("filtered projects = %#v", m.projects)
	}
}

func TestModelCyclesSortMode(t *testing.T) {
	m := New([]session.Session{{ID: "one", CWD: "/repo", UpdatedAt: time.Now()}})

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	m = next.(Model)

	if m.sortMode != "created" {
		t.Fatalf("sortMode = %q, want created", m.sortMode)
	}
}

func TestModelDefaultsToThirtyDayWindow(t *testing.T) {
	m := New([]session.Session{{ID: "one", CWD: "/repo", UpdatedAt: time.Now()}})

	if m.windowDays != 30 {
		t.Fatalf("windowDays = %d, want 30", m.windowDays)
	}
	if m.stepDays != 30 {
		t.Fatalf("stepDays = %d, want 30", m.stepDays)
	}
}

func TestModelLoadMoreExtendsWindow(t *testing.T) {
	now := time.Now()
	m := NewWithLoader(
		[]session.Session{{ID: "recent", CWD: "/repo", UpdatedAt: now}},
		30,
		30,
		func(days int) ([]session.Session, error) {
			if days != 60 {
				t.Fatalf("days = %d, want 60", days)
			}
			return []session.Session{
				{ID: "recent", CWD: "/repo", UpdatedAt: now},
				{ID: "older", CWD: "/repo", UpdatedAt: now.Add(-60 * 24 * time.Hour)},
			}, nil
		},
	)

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("m")})
	m = next.(Model)
	if !m.loading {
		t.Fatal("expected loading after pressing m")
	}

	msg := cmd().(loadedSessionsMsg)
	next, _ = m.Update(msg)
	m = next.(Model)

	if m.loading {
		t.Fatal("expected loading to finish")
	}
	if m.windowDays != 60 {
		t.Fatalf("windowDays = %d, want 60", m.windowDays)
	}
	if len(m.sessions) != 2 {
		t.Fatalf("sessions = %#v", m.sessions)
	}
}

func TestModelPageDownMovesByVisiblePage(t *testing.T) {
	sessions := make([]session.Session, 0, 20)
	now := time.Now()
	for i := range 20 {
		sessions = append(sessions, session.Session{
			ID:        string(rune('a' + i)),
			CWD:       "/repo",
			UpdatedAt: now.Add(-time.Duration(i) * time.Minute),
		})
	}
	m := New(sessions)
	m.height = 20

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	m = next.(Model)

	if m.sessionIdx != sessionListLimit(m.height-7) {
		t.Fatalf("sessionIdx = %d, want %d", m.sessionIdx, sessionListLimit(m.height-7))
	}
	if !strings.Contains(m.View(), "page 2/") {
		t.Fatalf("view missing page status:\n%s", m.View())
	}
}

func TestModelEndMovesToLastSession(t *testing.T) {
	now := time.Now()
	m := New([]session.Session{
		{ID: "first", CWD: "/repo", UpdatedAt: now},
		{ID: "last", CWD: "/repo", UpdatedAt: now.Add(-time.Minute)},
	})

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnd})
	m = next.(Model)

	if m.sessionIdx != 1 {
		t.Fatalf("sessionIdx = %d, want 1", m.sessionIdx)
	}
}

func TestModelDoesNotSelectMissingCWDSession(t *testing.T) {
	m := New([]session.Session{{
		ID:        "missing",
		CWD:       "/repo/missing",
		UpdatedAt: time.Now(),
		Metadata:  map[string]string{"cwd_missing": "true"},
	}})

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := next.(Model)

	if _, ok := got.Selected(); ok {
		t.Fatal("expected missing cwd session not to be selected")
	}
	if !strings.Contains(got.View(), "cwd missing: /repo/missing") {
		t.Fatalf("view missing cwd warning:\n%s", got.View())
	}
}

func TestModelViewMarksMissingCWDSessions(t *testing.T) {
	m := New([]session.Session{{
		ID:        "missing",
		CWD:       "/repo/missing",
		UpdatedAt: time.Now(),
		Metadata:  map[string]string{"cwd_missing": "true"},
	}})

	view := m.View()

	if !strings.Contains(view, "missing cwd") && !strings.Contains(view, "! missing") {
		t.Fatalf("view missing unavailable marker:\n%s", view)
	}
}

func TestModelViewShowsNavigationHints(t *testing.T) {
	m := New([]session.Session{{ID: "one", CWD: "/repo", UpdatedAt: time.Now()}})

	view := m.View()

	for _, want := range []string{"←/→ projects", "↑/↓ sessions"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
}
