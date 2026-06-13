package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/hxy91819/agent-session-manager/internal/session"
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

func TestModelSearchFiltersProvider(t *testing.T) {
	m := New([]session.Session{
		{ID: "codex-one", Provider: "codex", CWD: "/repo", Title: "review", UpdatedAt: time.Now()},
		{ID: "claude-one", Provider: "claude", CWD: "/repo", Title: "review", UpdatedAt: time.Now()},
	})

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = next.(Model)
	for _, r := range "claude" {
		next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = next.(Model)
	}

	if len(m.sessions) != 1 || m.sessions[0].Provider != "claude" {
		t.Fatalf("filtered sessions = %#v", m.sessions)
	}
}

func TestSearchViewShowsMatchingSessionsAcrossProjects(t *testing.T) {
	now := time.Now()
	m := New([]session.Session{
		{ID: "one", Provider: "codex", CWD: "/repo/a", Title: "needle first", UpdatedAt: now},
		{ID: "two", Provider: "claude", CWD: "/repo/b", Title: "needle second", UpdatedAt: now.Add(-time.Minute)},
		{ID: "three", Provider: "kimi", CWD: "/repo/c", Title: "unrelated", UpdatedAt: now.Add(-2 * time.Minute)},
	})

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = next.(Model)
	for _, r := range "needle" {
		next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = next.(Model)
	}

	view := m.sessionsView(16, 96)

	if !strings.Contains(view, "Search results") {
		t.Fatalf("view missing search header:\n%s", view)
	}
	for _, want := range []string{"needle first", "needle second", "/repo/a", "/repo/b"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "unrelated") {
		t.Fatalf("view should not include unrelated session:\n%s", view)
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

func TestModelLoadMoreDisabledForUnboundedWindow(t *testing.T) {
	m := NewWithLoader(
		[]session.Session{{ID: "all", CWD: "/repo", UpdatedAt: time.Now()}},
		0,
		30,
		func(days int) ([]session.Session, error) {
			t.Fatalf("loader should not run for unbounded window, got days=%d", days)
			return nil, nil
		},
	)

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("m")})
	m = next.(Model)

	if cmd != nil {
		t.Fatal("expected no load-more command")
	}
	if m.loading {
		t.Fatal("expected loading to remain false")
	}
	if strings.Contains(m.View(), "m +30d") {
		t.Fatalf("unbounded view should not advertise load more:\n%s", m.View())
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

func TestSessionPageSizeIsCapped(t *testing.T) {
	sessions := make([]session.Session, 0, 30)
	now := time.Now()
	for i := range 30 {
		sessions = append(sessions, session.Session{
			ID:        string(rune('a' + i%26)),
			CWD:       "/repo",
			UpdatedAt: now.Add(-time.Duration(i) * time.Minute),
		})
	}
	m := New(sessions)
	m.height = 80

	if got := m.sessionPageSize(); got != maxSessionsPerPage {
		t.Fatalf("sessionPageSize = %d, want %d", got, maxSessionsPerPage)
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

func TestSessionsViewShowsProviderTags(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	m := New([]session.Session{
		{ID: "codex-session", Provider: "codex", CWD: "/repo", Title: "codex work", UpdatedAt: base},
		{ID: "claude-session", Provider: "claude", CWD: "/repo", Title: "claude work", UpdatedAt: base.Add(time.Hour)},
	})

	view := m.sessionsView(14, 80)

	if !strings.Contains(view, "claude") || !strings.Contains(view, "codex") {
		t.Fatalf("view missing provider tags:\n%s", view)
	}
	if !strings.Contains(view, "provider: claude") {
		t.Fatalf("view missing selected provider detail:\n%s", view)
	}
}

func TestProjectsViewShowsRangeWhenClipped(t *testing.T) {
	now := time.Now()
	sessions := make([]session.Session, 0, 30)
	for i := range 30 {
		sessions = append(sessions, session.Session{
			ID:        string(rune('a' + i%26)),
			CWD:       "/repo/project-" + string(rune('a'+i%26)),
			UpdatedAt: now.Add(-time.Duration(i) * time.Minute),
		})
	}
	m := New(sessions)

	view := m.projectsView(8, 32)

	if !strings.Contains(view, "showing 1-5/26") {
		t.Fatalf("view missing clipped project range:\n%s", view)
	}
	if got := lipgloss.Height(view); got > 8 {
		t.Fatalf("height = %d, want <= 8\n%s", got, view)
	}
	for _, line := range strings.Split(view, "\n") {
		if got := lipgloss.Width(line); got > 32 {
			t.Fatalf("line width = %d, want <= 32\n%s", got, line)
		}
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

func TestModelViewFitsVeryNarrowViewport(t *testing.T) {
	m := New([]session.Session{{
		ID:        "tiny",
		CWD:       "/repo",
		Title:     "tiny",
		UpdatedAt: time.Now(),
	}})
	m.width = 8
	m.height = 10

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

func TestModelViewFitsShortViewport(t *testing.T) {
	m := New([]session.Session{{
		ID:        "short",
		CWD:       "/repo",
		Title:     "short viewport",
		UpdatedAt: time.Now(),
	}})
	m.width = 80
	m.height = 5

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

	if !strings.Contains(view, "cwd missing") && !strings.Contains(view, "! unknown missing") {
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
