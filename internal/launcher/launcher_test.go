package launcher

import (
	"context"
	"strings"
	"testing"

	"session-manager/internal/session"
)

func TestRunReportsMissingCWD(t *testing.T) {
	err := Run(context.Background(), session.ExecSpec{
		Dir:  "/definitely/missing/session-manager-test",
		Args: []string{"codex", "resume", "sid"},
	}, false)

	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "resume cwd unavailable") {
		t.Fatalf("error = %v", err)
	}
}
