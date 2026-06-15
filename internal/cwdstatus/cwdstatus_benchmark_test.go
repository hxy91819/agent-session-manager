package cwdstatus

import (
	"testing"

	"github.com/hxy91819/agent-session-manager/internal/session"
)

func BenchmarkRepeatedCWDStatus(b *testing.B) {
	const sessionCount = 500
	cwd := b.TempDir()
	sessions := make([]session.Session, sessionCount)
	for i := range sessions {
		sessions[i] = session.Session{CWD: cwd, Metadata: make(map[string]string)}
	}

	b.Run("uncached", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			for i := range sessions {
				markUncached(&sessions[i])
			}
		}
	})

	b.Run("cached", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			checker := NewChecker()
			for i := range sessions {
				checker.Mark(&sessions[i])
			}
		}
	})
}

func markUncached(s *session.Session) {
	delete(s.Metadata, "cwd_missing")
	delete(s.Metadata, "cwd_error")
	status := check(s.CWD)
	if status.err != "" {
		s.Metadata["cwd_error"] = status.err
		return
	}
	if status.missing {
		s.Metadata["cwd_missing"] = "true"
	}
}
