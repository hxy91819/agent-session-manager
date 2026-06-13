package ui

import (
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
