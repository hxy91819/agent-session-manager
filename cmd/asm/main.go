package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/hxy91819/agent-session-manager/internal/index"
	"github.com/hxy91819/agent-session-manager/internal/launcher"
	"github.com/hxy91819/agent-session-manager/internal/provider/claude"
	"github.com/hxy91819/agent-session-manager/internal/provider/codebuddy"
	"github.com/hxy91819/agent-session-manager/internal/provider/codex"
	"github.com/hxy91819/agent-session-manager/internal/provider/cursor"
	"github.com/hxy91819/agent-session-manager/internal/provider/kimi"
	"github.com/hxy91819/agent-session-manager/internal/provider/openclaw"
	"github.com/hxy91819/agent-session-manager/internal/provider/opencode"
	"github.com/hxy91819/agent-session-manager/internal/provider/zcode"
	reportpkg "github.com/hxy91819/agent-session-manager/internal/report"
	"github.com/hxy91819/agent-session-manager/internal/session"
	"github.com/hxy91819/agent-session-manager/internal/skillinstall"
	"github.com/hxy91819/agent-session-manager/internal/ui"
)

type config struct {
	codexHome     string
	claudeHome    string
	kimiHome      string
	opencodeHome  string
	codebuddyHome string
	cursorHome    string
	openclawHome  string
	zcodeHome     string
	query         string
	sortMode      index.SortMode
	resumeID      string
	json          bool
	printExec     bool
	sinceDays     int
	limit         int
}

type reportConfig struct {
	codexHome     string
	claudeHome    string
	kimiHome      string
	opencodeHome  string
	codebuddyHome string
	cursorHome    string
	openclawHome  string
	zcodeHome     string
	query         string
	sortMode      index.SortMode
	period        string
	limit         int
	previewEdges  int
	previewChars  int
	previewOffset int
}

type resumeConfig struct {
	codexHome     string
	claudeHome    string
	kimiHome      string
	opencodeHome  string
	codebuddyHome string
	cursorHome    string
	openclawHome  string
	zcodeHome     string
	provider      string
	sessionID     string
	printExec     bool
	sinceDays     int
	limit         int
}

type skillsInstallConfig struct {
	source  string
	ref     string
	path    string
	scope   string
	target  string
	skill   string
	version string
	all     bool
	yes     bool
	dryRun  bool
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
	if len(args) > 0 && args[0] == "report" {
		return runReport(args[1:])
	}
	if len(args) > 0 && args[0] == "resume" {
		return runResume(ctx, args[1:])
	}
	if len(args) > 0 && args[0] == "skills" {
		return runSkills(ctx, args[1:])
	}

	cfg, err := parseFlags(args)
	if err != nil {
		return err
	}

	providers := newProviders(cfg.codexHome, cfg.claudeHome, cfg.kimiHome, cfg.opencodeHome, cfg.codebuddyHome, cfg.cursorHome, cfg.openclawHome, cfg.zcodeHome)
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
		selected, err := findSession(sessions, cfg.resumeID, "")
		if err != nil {
			return err
		}
		provider := providerByName(providers, selected.Provider)
		if provider == nil {
			return fmt.Errorf("no provider registered for %q", selected.Provider)
		}
		return resumeSession(ctx, provider, selected, cfg.printExec)
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
	return dispatchSelection(ctx, providers, selected, cfg.printExec)
}

func runResume(ctx context.Context, args []string) error {
	cfg, err := parseResumeFlags(args)
	if err != nil {
		return err
	}
	providers := newProviders(cfg.codexHome, cfg.claudeHome, cfg.kimiHome, cfg.opencodeHome, cfg.codebuddyHome, cfg.cursorHome, cfg.openclawHome, cfg.zcodeHome)
	sessions, err := discoverAll(providers, cfg.limit, cfg.sinceDays)
	if err != nil {
		return err
	}
	selected, err := findSession(sessions, cfg.sessionID, cfg.provider)
	if err != nil {
		return err
	}
	provider := providerByName(providers, selected.Provider)
	if provider == nil {
		return fmt.Errorf("no provider registered for %q", selected.Provider)
	}
	return resumeSession(ctx, provider, selected, cfg.printExec)
}

func runSkills(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] != "install" {
		return fmt.Errorf("usage: asm skills install [flags] [skill-name|github-url]")
	}
	return runSkillsInstall(ctx, args[1:])
}

func runSkillsInstall(ctx context.Context, args []string) error {
	cfg, err := parseSkillsInstallFlags(args)
	if err != nil {
		return err
	}
	fetcher := skillinstall.Fetcher{Token: os.Getenv("GITHUB_TOKEN")}
	var skills []skillinstall.Skill
	if cfg.source != "" {
		var source skillinstall.GitHubSource
		source, err = skillinstall.ParseGitHubSource(cfg.source, cfg.ref, cfg.path)
		if err != nil {
			return err
		}
		skills, err = fetcher.Fetch(ctx, source)
	} else {
		skills, err = fetcher.FetchRelease(ctx, cfg.version)
	}
	if err != nil {
		return err
	}
	reader := bufio.NewReader(os.Stdin)
	selected, err := chooseSkills(skills, cfg, reader)
	if err != nil {
		return err
	}
	scopes, err := chooseInstallScopes(cfg.scope, cfg.yes, reader)
	if err != nil {
		return err
	}
	targets, err := chooseInstallTargets(cfg.target, cfg.yes, reader)
	if err != nil {
		return err
	}
	results, err := skillinstall.Install(selected, skillinstall.InstallOptions{
		Scopes:  scopes,
		Targets: targets,
		DryRun:  cfg.dryRun,
	})
	if err != nil {
		return err
	}
	for _, result := range results {
		action := "installed"
		if cfg.dryRun {
			action = "would install"
		}
		if _, err := fmt.Fprintf(os.Stdout, "%s %s -> %s\n", action, result.Skill, result.Path); err != nil {
			return err
		}
	}
	return nil
}

func runReport(args []string) error {
	cfg, err := parseReportFlags(args)
	if err != nil {
		return err
	}
	window, err := reportpkg.WindowForPeriod(cfg.period, time.Now(), time.Local)
	if err != nil {
		return err
	}
	providers := newProviders(cfg.codexHome, cfg.claudeHome, cfg.kimiHome, cfg.opencodeHome, cfg.codebuddyHome, cfg.cursorHome, cfg.openclawHome, cfg.zcodeHome)
	items, err := discoverAllWithOptions(providers, session.DiscoverOptions{
		LimitFiles: cfg.limit,
		Since:      window.Start,
		Preview: session.PreviewOptions{
			UserMessagesPerEdge: cfg.previewEdges,
			MaxChars:            cfg.previewChars,
			EdgeOffset:          cfg.previewOffset,
			Since:               window.Start,
			Before:              window.End,
		},
	})
	if err != nil {
		return err
	}
	sessions := withResumeCommands(filterSessions(items, cfg.query, cfg.sortMode))
	payload := reportpkg.BuildPayload(window, sessions)
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(payload)
}

func parseFlags(args []string) (config, error) {
	var cfg config
	var sortMode string
	fs := flag.NewFlagSet("asm", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&cfg.codexHome, "codex-home", "", "Codex home directory")
	fs.StringVar(&cfg.claudeHome, "claude-home", "", "Claude Code home directory")
	fs.StringVar(&cfg.kimiHome, "kimi-home", "", "Kimi Code home directory")
	fs.StringVar(&cfg.opencodeHome, "opencode-home", "", "opencode home directory")
	fs.StringVar(&cfg.codebuddyHome, "codebuddy-home", "", "CodeBuddy home directory")
	fs.StringVar(&cfg.cursorHome, "cursor-home", "", "Cursor home directory")
	fs.StringVar(&cfg.openclawHome, "openclaw-home", "", "OpenClaw state directory")
	fs.StringVar(&cfg.zcodeHome, "zcode-home", "", "ZCode home directory")
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

func parseResumeFlags(args []string) (resumeConfig, error) {
	cfg := resumeConfig{
		limit:     2000,
		sinceDays: 30,
	}
	fs := flag.NewFlagSet("asm resume", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&cfg.codexHome, "codex-home", "", "Codex home directory")
	fs.StringVar(&cfg.claudeHome, "claude-home", "", "Claude Code home directory")
	fs.StringVar(&cfg.kimiHome, "kimi-home", "", "Kimi Code home directory")
	fs.StringVar(&cfg.opencodeHome, "opencode-home", "", "opencode home directory")
	fs.StringVar(&cfg.codebuddyHome, "codebuddy-home", "", "CodeBuddy home directory")
	fs.StringVar(&cfg.cursorHome, "cursor-home", "", "Cursor home directory")
	fs.StringVar(&cfg.openclawHome, "openclaw-home", "", "OpenClaw state directory")
	fs.StringVar(&cfg.zcodeHome, "zcode-home", "", "ZCode home directory")
	fs.StringVar(&cfg.provider, "provider", "", "provider name for disambiguating session ids")
	fs.BoolVar(&cfg.printExec, "print-exec", false, "print resume command instead of executing it")
	fs.IntVar(&cfg.limit, "limit", 2000, "maximum session files to scan per provider")
	fs.IntVar(&cfg.sinceDays, "since-days", 30, "only scan session files modified in the last N days; 0 scans all")
	if err := fs.Parse(args); err != nil {
		return resumeConfig{}, err
	}
	if fs.NArg() != 1 {
		return resumeConfig{}, fmt.Errorf("usage: asm resume [flags] <session-id>")
	}
	if cfg.sinceDays < 0 {
		return resumeConfig{}, errors.New("since-days must be >= 0")
	}
	if cfg.limit < 0 {
		return resumeConfig{}, errors.New("limit must be >= 0")
	}
	cfg.sessionID = fs.Arg(0)
	return cfg, nil
}

func parseSkillsInstallFlags(args []string) (skillsInstallConfig, error) {
	var cfg skillsInstallConfig
	normalized, err := normalizeSkillsInstallArgs(args)
	if err != nil {
		return skillsInstallConfig{}, err
	}
	fs := flag.NewFlagSet("asm skills install", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&cfg.ref, "ref", "", "git ref to download; defaults to the repository default branch")
	fs.StringVar(&cfg.path, "path", "", "repository path to a skill directory or parent skills directory")
	fs.StringVar(&cfg.scope, "scope", "", "install scope: current, user, both")
	fs.StringVar(&cfg.target, "target", "", "install target: agents, claude, both")
	fs.StringVar(&cfg.skill, "skill", "", "skill name to install when the source contains multiple skills")
	fs.StringVar(&cfg.version, "version", "", "asm release tag to install skills from; defaults to latest release")
	fs.BoolVar(&cfg.all, "all", false, "install all skills found in the source")
	fs.BoolVar(&cfg.yes, "yes", false, "use defaults for omitted choices")
	fs.BoolVar(&cfg.dryRun, "dry-run", false, "print install destinations without writing files")
	if err := fs.Parse(normalized); err != nil {
		return skillsInstallConfig{}, err
	}
	if fs.NArg() > 1 {
		return skillsInstallConfig{}, fmt.Errorf("usage: asm skills install [flags] [skill-name|github-url]")
	}
	if cfg.all && cfg.skill != "" {
		return skillsInstallConfig{}, fmt.Errorf("--all and --skill cannot be used together")
	}
	if fs.NArg() == 1 {
		arg := fs.Arg(0)
		if looksLikeGitHubSource(arg) {
			cfg.source = arg
		} else {
			if cfg.skill != "" {
				return skillsInstallConfig{}, fmt.Errorf("skill name provided twice")
			}
			cfg.skill = arg
		}
	}
	if cfg.source != "" && cfg.version != "" {
		return skillsInstallConfig{}, fmt.Errorf("--version is only supported for default release installs")
	}
	if cfg.source == "" && (cfg.ref != "" || cfg.path != "") {
		return skillsInstallConfig{}, fmt.Errorf("--ref and --path require a github source")
	}
	return cfg, nil
}

func normalizeSkillsInstallArgs(args []string) ([]string, error) {
	valueFlags := map[string]struct{}{
		"-ref":      {},
		"--ref":     {},
		"-path":     {},
		"--path":    {},
		"-scope":    {},
		"--scope":   {},
		"-target":   {},
		"--target":  {},
		"-skill":    {},
		"--skill":   {},
		"-version":  {},
		"--version": {},
	}
	var out []string
	var source string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			if i+2 != len(args) {
				return nil, fmt.Errorf("usage: asm skills install [flags] <github-url>")
			}
			if source != "" {
				return nil, fmt.Errorf("usage: asm skills install [flags] <github-url>")
			}
			source = args[i+1]
			break
		}
		if strings.HasPrefix(arg, "-") {
			out = append(out, arg)
			name := arg
			if before, _, ok := strings.Cut(arg, "="); ok {
				name = before
			}
			if _, ok := valueFlags[name]; ok && !strings.Contains(arg, "=") {
				if i+1 >= len(args) {
					return nil, fmt.Errorf("flag needs an argument: %s", arg)
				}
				out = append(out, args[i+1])
				i++
			}
			continue
		}
		if source != "" {
			return nil, fmt.Errorf("usage: asm skills install [flags] <github-url>")
		}
		source = arg
	}
	if source != "" {
		out = append(out, source)
	}
	return out, nil
}

func looksLikeGitHubSource(value string) bool {
	return strings.Contains(value, "/") || strings.Contains(value, "github.com") || strings.HasPrefix(value, "git@github.com:")
}

func parseReportFlags(args []string) (reportConfig, error) {
	cfg := reportConfig{
		period:       reportpkg.PeriodYesterday,
		limit:        2000,
		previewEdges: session.DefaultPreviewMessagesPerEdge,
		previewChars: session.DefaultPreviewMaxChars,
	}
	var sortMode string
	fs := flag.NewFlagSet("asm report", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&cfg.codexHome, "codex-home", "", "Codex home directory")
	fs.StringVar(&cfg.claudeHome, "claude-home", "", "Claude Code home directory")
	fs.StringVar(&cfg.kimiHome, "kimi-home", "", "Kimi Code home directory")
	fs.StringVar(&cfg.opencodeHome, "opencode-home", "", "opencode home directory")
	fs.StringVar(&cfg.codebuddyHome, "codebuddy-home", "", "CodeBuddy home directory")
	fs.StringVar(&cfg.cursorHome, "cursor-home", "", "Cursor home directory")
	fs.StringVar(&cfg.openclawHome, "openclaw-home", "", "OpenClaw state directory")
	fs.StringVar(&cfg.zcodeHome, "zcode-home", "", "ZCode home directory")
	fs.StringVar(&cfg.query, "query", "", "filter sessions")
	fs.StringVar(&sortMode, "sort", string(index.SortActive), "sort mode: active, created, project")
	fs.IntVar(&cfg.limit, "limit", 2000, "maximum session files to scan per provider")
	fs.StringVar(&cfg.period, "period", reportpkg.PeriodYesterday, "report period: today, yesterday, last-week, last-7-days")
	fs.IntVar(&cfg.previewEdges, "preview-messages-per-edge", session.DefaultPreviewMessagesPerEdge, "user message previews to include from both the start and end of each session")
	fs.IntVar(&cfg.previewChars, "preview-max-chars", session.DefaultPreviewMaxChars, "maximum characters per message preview")
	fs.IntVar(&cfg.previewOffset, "preview-edge-offset", 0, "skip this many user messages from both preview edges before selecting previews")
	if err := fs.Parse(args); err != nil {
		return reportConfig{}, err
	}
	if fs.NArg() > 0 {
		return reportConfig{}, fmt.Errorf("unexpected report arguments: %v", fs.Args())
	}
	if cfg.limit < 0 {
		return reportConfig{}, errors.New("limit must be >= 0")
	}
	if cfg.previewEdges < 0 {
		return reportConfig{}, errors.New("preview-messages-per-edge must be >= 0")
	}
	if cfg.previewChars < 0 {
		return reportConfig{}, errors.New("preview-max-chars must be >= 0")
	}
	if cfg.previewOffset < 0 {
		return reportConfig{}, errors.New("preview-edge-offset must be >= 0")
	}
	cfg.sortMode = index.SortMode(sortMode)
	return cfg, nil
}

func newProviders(codexHome, claudeHome, kimiHome, opencodeHome, codebuddyHome, cursorHome, openclawHome, zcodeHome string) []session.Provider {
	return []session.Provider{
		codex.New(codexHome),
		claude.New(claudeHome),
		kimi.New(kimiHome),
		opencode.New(opencodeHome),
		codebuddy.New(codebuddyHome),
		cursor.New(cursorHome),
		openclaw.New(openclawHome),
		zcode.New(zcodeHome),
	}
}

func discoverAll(providers []session.Provider, limit int, sinceDays int) ([]session.Session, error) {
	var since time.Time
	if sinceDays > 0 {
		since = time.Now().Add(-time.Duration(sinceDays) * 24 * time.Hour)
	}
	return discoverAllWithOptions(providers, session.DiscoverOptions{LimitFiles: limit, Since: since})
}

func discoverAllWithOptions(providers []session.Provider, opts session.DiscoverOptions) ([]session.Session, error) {
	type result struct {
		items []session.Session
		err   error
	}
	results := make([]result, len(providers))
	var wg sync.WaitGroup
	wg.Add(len(providers))
	for i, provider := range providers {
		go func(i int, provider session.Provider) {
			defer wg.Done()
			items, err := provider.Discover(opts)
			if err != nil {
				results[i].err = fmt.Errorf("%s discover: %w", provider.Name(), err)
				return
			}
			results[i].items = items
		}(i, provider)
	}
	wg.Wait()

	var out []session.Session
	for _, result := range results {
		if result.err != nil {
			return nil, result.err
		}
		out = append(out, result.items...)
	}
	return out, nil
}

func filterSessions(sessions []session.Session, query string, sortMode index.SortMode) []session.Session {
	return index.FilterAndSort(sessions, index.Query{
		Search: query,
		Sort:   sortMode,
	})
}

func findSession(sessions []session.Session, id string, provider string) (session.Session, error) {
	var matches []session.Session
	for _, item := range sessions {
		if item.ID == id && (provider == "" || item.Provider == provider) {
			matches = append(matches, item)
		}
	}
	if len(matches) == 0 {
		if provider != "" {
			return session.Session{}, fmt.Errorf("session not found: %s for provider %s", id, provider)
		}
		return session.Session{}, fmt.Errorf("session not found: %s", id)
	}
	if len(matches) > 1 {
		return session.Session{}, fmt.Errorf("session id %q is ambiguous across providers", id)
	}
	return matches[0], nil
}

func withResumeCommands(sessions []session.Session) []session.Session {
	out := make([]session.Session, len(sessions))
	for i, item := range sessions {
		if sessionSupportsResume(item) {
			item.ResumeCommand = resumeCLICommand(item)
		}
		out[i] = item
	}
	return out
}

func sessionSupportsResume(s session.Session) bool {
	if s.Provider == "openclaw" {
		return false
	}
	return s.Metadata["cwd_missing"] != "true" && s.Metadata["cwd_error"] == "" && s.Metadata["resume_unsupported"] == ""
}

func resumeCLICommand(s session.Session) string {
	return "asm resume --provider " + shellQuote(s.Provider) + " " + shellQuote(s.ID)
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func chooseSkills(skills []skillinstall.Skill, cfg skillsInstallConfig, reader *bufio.Reader) ([]skillinstall.Skill, error) {
	if len(skills) == 0 {
		return nil, fmt.Errorf("no skills found")
	}
	if cfg.all {
		return skills, nil
	}
	if cfg.skill != "" {
		for _, skill := range skills {
			if skill.Name == cfg.skill {
				return []skillinstall.Skill{skill}, nil
			}
		}
		return nil, fmt.Errorf("skill %q not found", cfg.skill)
	}
	if len(skills) == 1 {
		return skills, nil
	}
	if cfg.yes {
		return nil, fmt.Errorf("multiple skills found; pass --skill <name> or --all")
	}
	fmt.Fprintln(os.Stderr, "Select skill to install:")
	fmt.Fprintln(os.Stderr, "  0) all")
	for i, skill := range skills {
		fmt.Fprintf(os.Stderr, "  %d) %s\n", i+1, skill.Name)
	}
	choice, err := promptInt(reader, "Choice")
	if err != nil {
		return nil, err
	}
	if choice == 0 {
		return skills, nil
	}
	if choice < 1 || choice > len(skills) {
		return nil, fmt.Errorf("invalid skill choice %d", choice)
	}
	return []skillinstall.Skill{skills[choice-1]}, nil
}

func chooseInstallScopes(value string, yes bool, reader *bufio.Reader) ([]string, error) {
	if value != "" {
		return parseInstallSelection(value, skillinstall.ValidScope, skillinstall.ScopeCurrent, skillinstall.ScopeUser)
	}
	if yes {
		return []string{skillinstall.ScopeCurrent}, nil
	}
	return promptSelection(reader, "Install location", []string{
		"current directory",
		"user directory",
		"both",
	}, [][]string{
		{skillinstall.ScopeCurrent},
		{skillinstall.ScopeUser},
		{skillinstall.ScopeCurrent, skillinstall.ScopeUser},
	})
}

func chooseInstallTargets(value string, yes bool, reader *bufio.Reader) ([]string, error) {
	if value != "" {
		return parseInstallSelection(value, skillinstall.ValidTarget, skillinstall.TargetAgents, skillinstall.TargetClaude)
	}
	if yes {
		return []string{skillinstall.TargetAgents}, nil
	}
	return promptSelection(reader, "Install target", []string{
		".agents",
		".claude",
		"both",
	}, [][]string{
		{skillinstall.TargetAgents},
		{skillinstall.TargetClaude},
		{skillinstall.TargetAgents, skillinstall.TargetClaude},
	})
}

func parseInstallSelection(value string, valid func(string) bool, first string, second string) ([]string, error) {
	value = strings.TrimSpace(value)
	if value == "both" {
		return []string{first, second}, nil
	}
	if valid(value) {
		return []string{value}, nil
	}
	return nil, fmt.Errorf("invalid install selection %q", value)
}

func promptSelection(reader *bufio.Reader, label string, labels []string, values [][]string) ([]string, error) {
	fmt.Fprintf(os.Stderr, "%s:\n", label)
	for i, item := range labels {
		fmt.Fprintf(os.Stderr, "  %d) %s\n", i+1, item)
	}
	choice, err := promptInt(reader, "Choice")
	if err != nil {
		return nil, err
	}
	if choice < 1 || choice > len(values) {
		return nil, fmt.Errorf("invalid %s choice %d", strings.ToLower(label), choice)
	}
	return values[choice-1], nil
}

func promptInt(reader *bufio.Reader, label string) (int, error) {
	fmt.Fprintf(os.Stderr, "%s: ", label)
	var choice int
	if _, err := fmt.Fscan(reader, &choice); err != nil {
		return 0, err
	}
	return choice, nil
}

func providerByName(providers []session.Provider, name string) session.Provider {
	for _, provider := range providers {
		if provider.Name() == name {
			return provider
		}
	}
	return nil
}

func resumeSession(ctx context.Context, provider session.Provider, selected session.Session, printOnly bool) error {
	spec := provider.ResumeCommand(selected)
	if spec.UnsupportedReason != "" {
		return launcher.Run(ctx, spec, printOnly)
	}
	if !sessionSupportsResume(selected) {
		return fmt.Errorf("session %s cwd is unavailable", selected.ID)
	}
	if !printOnly {
		fmt.Fprintln(os.Stderr, resumeNotice(selected))
	}
	return launcher.Run(ctx, spec, printOnly)
}

func dispatchSelection(ctx context.Context, providers []session.Provider, selected ui.Selection, printOnly bool) error {
	provider := providerByName(providers, selected.Provider)
	if provider == nil {
		return fmt.Errorf("no provider registered for %q", selected.Provider)
	}
	switch selected.Kind {
	case ui.SelectionResume:
		return resumeSession(ctx, provider, selected.Session, printOnly)
	case ui.SelectionNew:
		return newSession(ctx, provider, selected.CWD, printOnly)
	default:
		return fmt.Errorf("unknown selection kind %q", selected.Kind)
	}
}

func newSession(ctx context.Context, provider session.Provider, cwd string, printOnly bool) error {
	spec := provider.NewCommand(cwd)
	if !printOnly {
		fmt.Fprintln(os.Stderr, newNotice(provider.Name(), cwd))
	}
	return launcher.Run(ctx, spec, printOnly)
}

func resumeNotice(selected session.Session) string {
	return fmt.Sprintf("Starting %s session %s from %s ... this can take a few seconds.", selected.Provider, selected.ID, selected.CWD)
}

func newNotice(provider string, cwd string) string {
	return fmt.Sprintf("Starting new %s session from %s ... this can take a few seconds.", provider, cwd)
}
