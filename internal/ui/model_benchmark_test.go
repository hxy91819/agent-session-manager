package ui

import (
	"fmt"
	"testing"
	"time"

	"github.com/hxy91819/agent-session-manager/internal/session"
)

func BenchmarkNewModel2000Sessions(b *testing.B) {
	items := make([]session.Session, 0, 2000)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 2000; i++ {
		items = append(items, session.Session{
			ID:        fmt.Sprintf("session-%04d", i),
			Provider:  "codex",
			CWD:       fmt.Sprintf("/repo/%02d", i%50),
			Title:     fmt.Sprintf("normalized benchmark title %04d", i),
			UpdatedAt: base.Add(time.Duration(i) * time.Second),
		})
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		model := New(items)
		if len(model.projects) != 50 {
			b.Fatalf("projects = %d", len(model.projects))
		}
	}
}
