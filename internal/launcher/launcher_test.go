package launcher

import (
	"bytes"
	"context"
	"fmt"
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

func TestRunPrintExecWithoutWorkingDirectory(t *testing.T) {
	var out bytes.Buffer
	restore := captureStdout(t, &out)

	err := Run(context.Background(), session.ExecSpec{
		Args: []string{"herdr", "agent", "focus", "w1:p2"},
	}, true)
	if err != nil {
		restore()
		t.Fatal(err)
	}
	restore()

	if got := out.String(); got != "'herdr' 'agent' 'focus' 'w1:p2'\n" {
		t.Fatalf("command = %q", got)
	}
}

func TestRunPrintExecIncludesSortedEnvironment(t *testing.T) {
	var out bytes.Buffer
	restore := captureStdout(t, &out)

	err := Run(context.Background(), session.ExecSpec{
		Args: []string{"codex", "resume", "sid"},
		Env: map[string]string{
			"Z_HOME": "/tmp/z",
			"A_HOME": "/tmp/a'b",
		},
	}, true)
	if err != nil {
		restore()
		t.Fatal(err)
	}
	restore()

	want := "'env' 'A_HOME=/tmp/a'\\''b' 'Z_HOME=/tmp/z' 'codex' 'resume' 'sid'\n"
	if got := out.String(); got != want {
		t.Fatalf("command = %q, want %q", got, want)
	}
}

func TestRunAppliesEnvironmentOverrides(t *testing.T) {
	t.Setenv("CODEX_HOME", "/ambient")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	restore := captureStdout(t, &out)
	err = Run(context.Background(), session.ExecSpec{
		Dir:  t.TempDir(),
		Args: []string{executable, "-test.run=^TestRunEnvironmentHelper$"},
		Env: map[string]string{
			"ASM_LAUNCHER_ENV_HELPER": "1",
			"CODEX_HOME":              "/selected",
		},
	}, false)
	if err != nil {
		restore()
		t.Fatal(err)
	}
	restore()

	if got := out.String(); got != "/selected" {
		t.Fatalf("CODEX_HOME = %q", got)
	}
}

func TestRunEnvironmentHelper(t *testing.T) {
	if os.Getenv("ASM_LAUNCHER_ENV_HELPER") != "1" {
		return
	}
	fmt.Fprint(os.Stdout, os.Getenv("CODEX_HOME"))
	os.Exit(0)
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
