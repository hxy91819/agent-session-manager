package launcher

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/hxy91819/agent-session-manager/internal/session"
)

func TestRunReportsMissingCWD(t *testing.T) {
	err := Run(context.Background(), session.ExecSpec{
		Dir:  "/definitely/missing/asm-test",
		Args: []string{"codex", "resume", "sid"},
	}, false)

	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "resume cwd unavailable") {
		t.Fatalf("error = %v", err)
	}
}

func TestRunRejectsUnsupportedResumeBeforeCommandChecks(t *testing.T) {
	err := Run(context.Background(), session.ExecSpec{
		UnsupportedReason: "OpenClaw resume is not supported by asm yet",
	}, true)

	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() != "OpenClaw resume is not supported by asm yet" {
		t.Fatalf("error = %v", err)
	}
}

func TestRunPrintExecUsesShellSafeQuoting(t *testing.T) {
	var out bytes.Buffer
	restore := captureStdout(t, &out)

	err := Run(context.Background(), session.ExecSpec{
		Dir:  "/tmp/$(touch pwned)'repo",
		Args: []string{"codex", "resume", "abc'$(touch nope)"},
	}, true)
	if err != nil {
		restore()
		t.Fatal(err)
	}
	restore()

	got := out.String()
	if strings.Contains(got, "\"") {
		t.Fatalf("print-exec used double quotes: %q", got)
	}
	if !strings.Contains(got, `cd '/tmp/$(touch pwned)'\''repo' && 'codex' 'resume' 'abc'\''$(touch nope)'`) {
		t.Fatalf("unexpected command: %q", got)
	}
}

func captureStdout(t *testing.T, out *bytes.Buffer) func() {
	t.Helper()
	original := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(out, r)
		close(done)
	}()
	return func() {
		_ = w.Close()
		<-done
		os.Stdout = original
		_ = r.Close()
	}
}
