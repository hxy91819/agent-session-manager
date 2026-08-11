package launcher

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
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
	env, err := environmentEntries(spec.Env)
	if err != nil {
		return err
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
		command := make([]string, 0, len(spec.Args)+len(env)+1)
		if len(env) > 0 {
			command = append(command, "env")
			command = append(command, env...)
		}
		command = append(command, spec.Args...)
		for i, arg := range command {
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
	if len(env) > 0 {
		cmd.Env = append(cmd.Environ(), env...)
	}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func environmentEntries(values map[string]string) ([]string, error) {
	keys := make([]string, 0, len(values))
	for key := range values {
		if key == "" || strings.Contains(key, "=") {
			return nil, fmt.Errorf("invalid environment key %q", key)
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)

	entries := make([]string, 0, len(keys))
	for _, key := range keys {
		entries = append(entries, key+"="+values[key])
	}
	return entries, nil
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
