package index

import (
	"testing"
	"time"

	"github.com/hxy91819/agent-session-manager/internal/session"
)

func TestFilterAndSortByActive(t *testing.T) {
	oldTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	newTime := oldTime.Add(time.Hour)
	sessions := []session.Session{
		{ID: "old", CWD: "/repo/old", Title: "fix bug", UpdatedAt: oldTime},
		{ID: "new", CWD: "/repo/new", Title: "add search", UpdatedAt: newTime},
	}

	got := FilterAndSort(sessions, Query{Search: "repo", Sort: SortActive})

	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].ID != "new" || got[1].ID != "old" {
		t.Fatalf("unexpected order: %#v", got)
	}
}

func TestFilterMatchesMetadata(t *testing.T) {
	sessions := []session.Session{
		{ID: "one", Metadata: map[string]string{"model": "gpt-5"}},
		{ID: "two", Metadata: map[string]string{"model": "other"}},
	}

	got := FilterAndSort(sessions, Query{Search: "gpt-5"})

	if len(got) != 1 || got[0].ID != "one" {
		t.Fatalf("got %#v", got)
	}
}

func TestGroupProjectsSortsByMostRecentSession(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	sessions := []session.Session{
		{ID: "a", CWD: "/repo/a", UpdatedAt: base},
		{ID: "b", CWD: "/repo/b", UpdatedAt: base.Add(time.Hour)},
		{ID: "a2", CWD: "/repo/a", UpdatedAt: base.Add(2 * time.Hour)},
	}

	got := GroupProjects(FilterAndSort(sessions, Query{Sort: SortActive}))

	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].CWD != "/repo/a" || got[0].Count != 2 || got[0].Sessions[0].ID != "a2" {
		t.Fatalf("unexpected projects: %#v", got)
	}
}

func TestGroupProjectsMixesProvidersByCWD(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	sessions := []session.Session{
		{ID: "codex", Provider: "codex", CWD: "/repo", UpdatedAt: base},
		{ID: "claude", Provider: "claude", CWD: "/repo", UpdatedAt: base.Add(time.Hour)},
	}

	got := GroupProjects(FilterAndSort(sessions, Query{Sort: SortActive}))

	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].Count != 2 {
		t.Fatalf("Count = %d, want 2", got[0].Count)
	}
	if got[0].Sessions[0].ID != "claude" || got[0].Sessions[1].ID != "codex" {
		t.Fatalf("unexpected session order: %#v", got[0].Sessions)
	}
}
