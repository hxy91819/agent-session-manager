package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/hxy91819/agent-session-manager/internal/index"
	"github.com/hxy91819/agent-session-manager/internal/launcher"
	"github.com/hxy91819/agent-session-manager/internal/provider/claude"
	"github.com/hxy91819/agent-session-manager/internal/provider/codex"
	"github.com/hxy91819/agent-session-manager/internal/session"
	"github.com/hxy91819/agent-session-manager/internal/ui"
)

type config struct {
	codexHome  string
	claudeHome string
	query      string
	sortMode   index.SortMode
	resumeID   string
	json       bool
	printExec  bool
	sinceDays  int
	limit      int
}

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
	cfg, err := parseFlags(args)
	if err != nil {
		return err
	}

	providers := []session.Provider{
		codex.New(cfg.codexHome),
		claude.New(cfg.claudeHome),
	}
	loadSessions := func(days int) ([]session.Session, error) {
		items, err := discoverAll(providers, cfg.limit, days)
		if err != nil {
			return nil, err
		}
		return filterSessions(items, cfg.query, cfg.sortMode), nil
	}
	sessions, err := loadSessions(cfg.sinceDays)
	if err != nil {
		return err
	}

	if cfg.resumeID != "" {
		selected, err := findSession(sessions, cfg.resumeID)
		if err != nil {
			return err
		}
		provider := providerByName(providers, selected.Provider)
		if provider == nil {
			return fmt.Errorf("no provider registered for %q", selected.Provider)
		}
		return launcher.Run(ctx, provider.ResumeCommand(selected), cfg.printExec)
	}

	if cfg.json {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(output{
			Projects: index.GroupProjects(sessions),
			Sessions: sessions,
		})
	}

	model, err := tea.NewProgram(ui.NewWithLoader(sessions, cfg.sinceDays, 30, loadSessions), tea.WithAltScreen()).Run()
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
	provider := providerByName(providers, selected.Provider)
	if provider == nil {
		return fmt.Errorf("no provider registered for %q", selected.Provider)
	}
	return launcher.Run(ctx, provider.ResumeCommand(selected), cfg.printExec)
}

func parseFlags(args []string) (config, error) {
	var cfg config
	var sortMode string
	fs := flag.NewFlagSet("session-manager", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&cfg.codexHome, "codex-home", "", "Codex home directory")
	fs.StringVar(&cfg.claudeHome, "claude-home", "", "Claude Code home directory")
	fs.BoolVar(&cfg.json, "json", false, "print indexed sessions as JSON")
	fs.StringVar(&cfg.query, "query", "", "filter sessions")
	fs.StringVar(&sortMode, "sort", string(index.SortActive), "sort mode: active, created, project")
	fs.IntVar(&cfg.limit, "limit", 2000, "maximum session files to scan per provider")
	fs.IntVar(&cfg.sinceDays, "since-days", 30, "only scan session files modified in the last N days; 0 scans all")
	fs.StringVar(&cfg.resumeID, "resume", "", "resume a session id without opening the TUI")
	fs.BoolVar(&cfg.printExec, "print-exec", false, "print resume command instead of executing it")
	if err := fs.Parse(args); err != nil {
		return config{}, err
	}
	if fs.NArg() > 0 {
		return config{}, fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	if cfg.sinceDays < 0 {
		return config{}, errors.New("since-days must be >= 0")
	}
	if cfg.limit < 0 {
		return config{}, errors.New("limit must be >= 0")
	}
	cfg.sortMode = index.SortMode(sortMode)
	return cfg, nil
}

func discoverAll(providers []session.Provider, limit int, sinceDays int) ([]session.Session, error) {
	var since time.Time
	if sinceDays > 0 {
		since = time.Now().Add(-time.Duration(sinceDays) * 24 * time.Hour)
	}
	opts := session.DiscoverOptions{LimitFiles: limit, Since: since}
	var out []session.Session
	for _, provider := range providers {
		items, err := provider.Discover(opts)
		if err != nil {
			return nil, fmt.Errorf("%s discover: %w", provider.Name(), err)
		}
		out = append(out, items...)
	}
	return out, nil
}

func filterSessions(sessions []session.Session, query string, sortMode index.SortMode) []session.Session {
	return index.FilterAndSort(sessions, index.Query{
		Search: query,
		Sort:   sortMode,
	})
}

func findSession(sessions []session.Session, id string) (session.Session, error) {
	var matches []session.Session
	for _, item := range sessions {
		if item.ID == id {
			matches = append(matches, item)
		}
	}
	if len(matches) == 0 {
		return session.Session{}, fmt.Errorf("session not found: %s", id)
	}
	if len(matches) > 1 {
		return session.Session{}, fmt.Errorf("session id %q is ambiguous across providers", id)
	}
	return matches[0], nil
}

func providerByName(providers []session.Provider, name string) session.Provider {
	for _, provider := range providers {
		if provider.Name() == name {
			return provider
		}
	}
	return nil
}
