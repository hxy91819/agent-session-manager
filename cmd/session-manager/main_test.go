package main

import (
	"strings"
	"testing"

	"github.com/hxy91819/agent-session-manager/internal/session"
)

func TestResumeNoticeIncludesProviderSessionAndCWD(t *testing.T) {
	got := resumeNotice(session.Session{
		ID:       "sid",
		Provider: "codex",
		CWD:      "/repo",
	})

	for _, want := range []string{"codex", "sid", "/repo", "few seconds"} {
		if !strings.Contains(got, want) {
			t.Fatalf("resumeNotice missing %q: %s", want, got)
		}
	}
}
