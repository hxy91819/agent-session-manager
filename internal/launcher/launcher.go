package launcher

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/hxy91819/agent-session-manager/internal/session"
)

func Run(ctx context.Context, spec session.ExecSpec, printOnly bool) error {
	if spec.UnsupportedReason != "" {
		return fmt.Errorf("%s", spec.UnsupportedReason)
	}
	if len(spec.Args) == 0 {
		return fmt.Errorf("empty command")
	}
	if !printOnly && spec.Dir != "" {
		info, err := os.Stat(spec.Dir)
		if err != nil {
			return fmt.Errorf("resume cwd unavailable: %s: %w", spec.Dir, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("resume cwd is not a directory: %s", spec.Dir)
		}
	}
	if printOnly {
		if spec.Dir != "" {
			fmt.Printf("cd %s &&", shellQuote(spec.Dir))
		}
		for i, arg := range spec.Args {
			if spec.Dir != "" || i > 0 {
				fmt.Print(" ")
			}
			fmt.Print(shellQuote(arg))
		}
		fmt.Println()
		return nil
	}
	cmd := exec.CommandContext(ctx, spec.Args[0], spec.Args[1:]...)
	if spec.Dir != "" {
		cmd.Dir = spec.Dir
	}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
