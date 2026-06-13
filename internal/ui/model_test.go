package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

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

	if m.sessionIdx != m.sessionPageSize() {
		t.Fatalf("sessionIdx = %d, want %d", m.sessionIdx, m.sessionPageSize())
	}
	if !strings.Contains(m.View(), "page 2/") {
		t.Fatalf("view missing page status:\n%s", m.View())
	}
}

func TestSessionsViewFitsContentHeightAndWidth(t *testing.T) {
	now := time.Now()
	longTitle := strings.Repeat("帮我review下ctl里的qqbot配置下发功能", 8)
	sessions := make([]session.Session, 0, 50)
	for i := range 50 {
		sessions = append(sessions, session.Session{
			ID:        string(rune('a' + i%26)),
			CWD:       "/root/code/mono",
			Title:     longTitle,
			UpdatedAt: now.Add(-time.Duration(i) * time.Minute),
			Path:      "/root/.codex/sessions/2026/04/02/rollout-2026-04-02T13-06-10-019d4c95-ac99-7201-bdd9-1a95bd8a8f8b.jsonl",
			Metadata:  map[string]string{"cwd_missing": "true"},
		})
	}
	m := New(sessions)

	view := m.sessionsView(12, 50)

	if got := lipgloss.Height(view); got > 12 {
		t.Fatalf("height = %d, want <= 12\n%s", got, view)
	}
	for _, line := range strings.Split(view, "\n") {
		if got := lipgloss.Width(line); got > 50 {
			t.Fatalf("line width = %d, want <= 50\n%s", got, line)
		}
	}
	if !strings.Contains(view, "page 1/") {
		t.Fatalf("view missing page status:\n%s", view)
	}
}

func TestModelViewFitsConfiguredViewport(t *testing.T) {
	now := time.Now()
	longTitle := strings.Repeat("记录两个文档，一个是这个问题，另一个是后续微信通道的排障、跟进指引。", 5)
	sessions := make([]session.Session, 0, 50)
	for i := range 50 {
		sessions = append(sessions, session.Session{
			ID:        string(rune('a' + i%26)),
			CWD:       "/root/code/mono",
			Title:     longTitle,
			UpdatedAt: now.Add(-time.Duration(i) * time.Minute),
			Path:      "/root/.codex/sessions/2026/04/02/rollout-2026-04-02T13-06-10-019d4c95-ac99-7201-bdd9-1a95bd8a8f8b.jsonl",
			Metadata:  map[string]string{"cwd_missing": "true"},
		})
	}
	m := New(sessions)
	m.width = 120
	m.height = 24

	view := m.View()

	if got := lipgloss.Height(view); got > m.height {
		t.Fatalf("height = %d, want <= %d\n%s", got, m.height, view)
	}
	for _, line := range strings.Split(view, "\n") {
		if got := lipgloss.Width(line); got > m.width {
			t.Fatalf("line width = %d, want <= %d\n%s", got, m.width, line)
		}
	}
}

func TestModelViewFitsNarrowViewport(t *testing.T) {
	now := time.Now()
	longTitle := strings.Repeat("帮我看下这个特别长的中文会话标题", 6)
	m := New([]session.Session{{
		ID:        "narrow",
		CWD:       "/repo/very-long-project-name",
		Title:     longTitle,
		UpdatedAt: now,
		Path:      "/root/.codex/sessions/2026/04/02/rollout-very-long-file-name.jsonl",
	}})
	m.width = 60
	m.height = 20

	view := m.View()

	if got := lipgloss.Height(view); got > m.height {
		t.Fatalf("height = %d, want <= %d\n%s", got, m.height, view)
	}
	for _, line := range strings.Split(view, "\n") {
		if got := lipgloss.Width(line); got > m.width {
			t.Fatalf("line width = %d, want <= %d\n%s", got, m.width, line)
		}
	}
	if strings.Contains(view, "Projects") {
		t.Fatalf("narrow view should not render project panel:\n%s", view)
	}
}

func TestTruncateUsesDisplayWidth(t *testing.T) {
	got := truncate("帮我review下ctl里的qqbot配置下发功能", 12)

	if width := lipgloss.Width(got); width > 12 {
		t.Fatalf("width = %d, want <= 12 for %q", width, got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("got %q, want ellipsis", got)
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
