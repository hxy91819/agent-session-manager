package launcher

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"session-manager/internal/session"
)

func Run(ctx context.Context, spec session.ExecSpec, printOnly bool) error {
	if len(spec.Args) == 0 {
		return fmt.Errorf("empty command")
	}
	if !printOnly {
		info, err := os.Stat(spec.Dir)
		if err != nil {
			return fmt.Errorf("resume cwd unavailable: %s: %w", spec.Dir, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("resume cwd is not a directory: %s", spec.Dir)
		}
	}
	if printOnly {
		fmt.Printf("cd %q &&", spec.Dir)
		for _, arg := range spec.Args {
			fmt.Printf(" %q", arg)
		}
		fmt.Println()
		return nil
	}
	cmd := exec.CommandContext(ctx, spec.Args[0], spec.Args[1:]...)
	cmd.Dir = spec.Dir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
