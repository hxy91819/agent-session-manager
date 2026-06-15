package cwdstatus

import (
	"path/filepath"
	"testing"

	"github.com/hxy91819/agent-session-manager/internal/session"
)

func TestCheckerMarksMissingCWD(t *testing.T) {
	s := session.Session{
		CWD:      filepath.Join(t.TempDir(), "missing"),
		Metadata: map[string]string{"title_source": "test"},
	}

	NewChecker().Mark(&s)

	if s.Metadata["cwd_missing"] != "true" {
		t.Fatalf("cwd_missing = %q", s.Metadata["cwd_missing"])
	}
	if s.Metadata["title_source"] != "test" {
		t.Fatalf("unrelated metadata was removed: %#v", s.Metadata)
	}
}

func TestCheckerClearsStaleCWDMetadata(t *testing.T) {
	s := session.Session{
		CWD: t.TempDir(),
		Metadata: map[string]string{
			"cwd_missing": "true",
			"cwd_error":   "old error",
		},
	}

	NewChecker().Mark(&s)

	if s.Metadata["cwd_missing"] != "" || s.Metadata["cwd_error"] != "" {
		t.Fatalf("stale cwd metadata remains: %#v", s.Metadata)
	}
}
