package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/hxy91819/agent-session-manager/internal/index"
	"github.com/hxy91819/agent-session-manager/internal/launcher"
	"github.com/hxy91819/agent-session-manager/internal/provider/codex"
	"github.com/hxy91819/agent-session-manager/internal/session"
	"github.com/hxy91819/agent-session-manager/internal/ui"
)

type output struct {
	Projects []session.Project `json:"projects"`
	Sessions []session.Session `json:"sessions"`
}

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("session-manager", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		codexHome = fs.String("codex-home", "", "Codex home directory")
		jsonMode  = fs.Bool("json", false, "print indexed sessions as JSON")
		query     = fs.String("query", "", "filter sessions")
		sortMode  = fs.String("sort", string(index.SortActive), "sort mode: active, created, project")
		limit     = fs.Int("limit", 2000, "maximum session files to scan per provider")
		sinceDays = fs.Int("since-days", 30, "only scan session files modified in the last N days; 0 scans all")
		resumeID  = fs.String("resume", "", "resume a session id without opening the TUI")
		printExec = fs.Bool("print-exec", false, "print resume command instead of executing it")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}

	provider := codex.New(*codexHome)
	loadSessions := func(days int) ([]session.Session, error) {
		return discoverSessions(provider, *limit, days)
	}
	sessions, err := loadSessions(*sinceDays)
	if err != nil {
		return err
	}
	sessions = index.FilterAndSort(sessions, index.Query{
		Search: *query,
		Sort:   index.SortMode(*sortMode),
	})

	if *resumeID != "" {
		for _, s := range sessions {
			if s.ID == *resumeID {
				return launcher.Run(ctx, provider.ResumeCommand(s), *printExec)
			}
		}
		return fmt.Errorf("session not found: %s", *resumeID)
	}

	if *jsonMode {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(output{
			Projects: index.GroupProjects(sessions),
			Sessions: sessions,
		})
	}

	model, err := tea.NewProgram(ui.NewWithLoader(sessions, *sinceDays, 30, loadSessions), tea.WithAltScreen()).Run()
	if err != nil {
		return err
	}
	finalModel, ok := model.(ui.Model)
	if !ok {
		return nil
	}
	selected, ok := finalModel.Selected()
	if !ok {
		return nil
	}
	return launcher.Run(ctx, provider.ResumeCommand(selected), *printExec)
}

type codexProvider interface {
	Discover(session.DiscoverOptions) ([]session.Session, error)
}

func discoverSessions(provider codexProvider, limit int, sinceDays int) ([]session.Session, error) {
	var since time.Time
	if sinceDays > 0 {
		since = time.Now().Add(-time.Duration(sinceDays) * 24 * time.Hour)
	}
	return provider.Discover(session.DiscoverOptions{LimitFiles: limit, Since: since})
}
