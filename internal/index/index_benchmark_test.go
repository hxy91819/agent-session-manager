package index

import (
	"fmt"
	"testing"
	"time"

	"github.com/hxy91819/agent-session-manager/internal/session"
)

func BenchmarkFilterAndGroup2000Sessions(b *testing.B) {
	sessions := benchmarkSessions(2000)
	b.ReportAllocs()
	for b.Loop() {
		filtered := FilterAndSort(sessions, Query{Search: "benchmark", Sort: SortActive})
		if projects := GroupProjects(filtered); len(projects) != 50 {
			b.Fatalf("projects = %d", len(projects))
		}
	}
}

func benchmarkSessions(count int) []session.Session {
	items := make([]session.Session, 0, count)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < count; i++ {
		items = append(items, session.Session{
			ID:        fmt.Sprintf("session-%04d", i),
			Provider:  "codex",
			CWD:       fmt.Sprintf("/repo/%02d", i%50),
			Title:     fmt.Sprintf("benchmark title %04d", i),
			CreatedAt: base.Add(time.Duration(i) * time.Second),
			UpdatedAt: base.Add(time.Duration(count-i) * time.Second),
			Metadata:  map[string]string{"model": "gpt-5"},
		})
	}
	return items
}
