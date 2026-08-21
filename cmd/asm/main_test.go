package main

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/hxy91819/agent-session-manager/internal/session"
	"github.com/hxy91819/agent-session-manager/internal/ui"
)

func TestResumeNoticeIncludesProviderSessionAndCWD(t *testing.T) {
	got := resumeNotice(session.Session{
		ID:       "sid",
		Provider: "codex",
		CWD:      "/repo",
	})

	for _, want := range []string{"codex", "sid", "/repo", "few seconds"} {
		if !strings.Contains(got, want) {
			t.Fatalf("resumeNotice missing %q: %s", want, got)
		}
	}
}

func TestDiscoverAllRunsProvidersConcurrentlyAndPreservesOrder(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan string, 2)
	providers := []session.Provider{
		blockingProvider{name: "first", entered: entered, release: release},
		blockingProvider{name: "second", entered: entered, release: release},
	}

	done := make(chan session.DiscoveryResult, 1)
	go func() {
		done <- discoverAll(providers, 10, 30)
	}()

	seen := map[string]bool{}
	for len(seen) < len(providers) {
		select {
		case name := <-entered:
			seen[name] = true
		case <-time.After(time.Second):
			t.Fatalf("providers did not enter Discover concurrently; seen %#v", seen)
		}
	}
	close(release)

	select {
	case got := <-done:
		if len(got.Sessions) != 2 {
			t.Fatalf("len = %d, want 2", len(got.Sessions))
		}
		if got.Sessions[0].Provider != "first" || got.Sessions[1].Provider != "second" {
			t.Fatalf("items out of provider order: %#v", got.Sessions)
		}
		if len(got.ProviderErrors) != 0 {
			t.Fatalf("unexpected provider errors: %#v", got.ProviderErrors)
		}
	case <-time.After(time.Second):
		t.Fatal("discoverAll did not return after providers were released")
	}
}

func TestDiscoverAllKeepsSuccessfulResultsAndProviderErrors(t *testing.T) {
	providers := []session.Provider{
		staticProvider{name: "ok"},
		staticProvider{name: "bad", err: errors.New("boom")},
	}

	got := discoverAll(providers, 10, 30)
	if len(got.Sessions) != 1 || got.Sessions[0].Provider != "ok" {
		t.Fatalf("sessions = %#v", got.Sessions)
	}
	if len(got.ProviderErrors) != 1 ||
		got.ProviderErrors[0].Provider != "bad" ||
		got.ProviderErrors[0].Error != "boom" {
		t.Fatalf("provider errors = %#v", got.ProviderErrors)
	}
}

func TestDiscoverAllEmptyProviderDoesNotBlockHealthyResults(t *testing.T) {
	got := discoverAll([]session.Provider{
		staticProvider{name: "empty", items: []session.Session{}},
		staticProvider{name: "healthy", items: []session.Session{{ID: "healthy", Provider: "healthy"}}},
	}, 10, 30)

	if len(got.Sessions) != 1 || got.Sessions[0].ID != "healthy" {
		t.Fatalf("sessions = %#v", got.Sessions)
	}
	if len(got.ProviderErrors) != 0 {
		t.Fatalf("provider errors = %#v", got.ProviderErrors)
	}
}

func TestDiscoverAllWithOptionsPassesSameWindowAndLimitToEveryProvider(t *testing.T) {
	since := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	first := &optionsProvider{name: "first"}
	second := &optionsProvider{name: "second"}
	discoverAllWithOptions([]session.Provider{first, second}, session.DiscoverOptions{
		LimitFiles: 7,
		Since:      since,
	})

	for _, provider := range []*optionsProvider{first, second} {
		if provider.got.LimitFiles != 7 || !provider.got.Since.Equal(since) {
			t.Fatalf("%s options = %#v", provider.name, provider.got)
		}
	}
}

func TestFindSessionFiltersProvider(t *testing.T) {
	sessions := []session.Session{
		{ID: "sid", Provider: "codex"},
		{ID: "sid", Provider: "claude"},
	}

	got, err := findSession(sessions, "sid", "claude")
	if err != nil {
		t.Fatal(err)
	}
	if got.Provider != "claude" {
		t.Fatalf("provider = %q", got.Provider)
	}
}

func TestResumeCLICommandQuotesProviderAndID(t *testing.T) {
	got := resumeCLICommand(session.Session{ID: "abc'123", Provider: "codex"})
	if got != "asm resume --provider 'codex' 'abc'\\''123'" {
		t.Fatalf("command = %q", got)
	}
}

func TestParseSkillsInstallFlagsAllowsURLBeforeFlags(t *testing.T) {
	got, err := parseSkillsInstallFlags([]string{
		"hxy91819/agent-session-manager",
		"--path", "skills/agent-work-report",
		"--scope", "current",
		"--target", "agents",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.source != "hxy91819/agent-session-manager" || got.path != "skills/agent-work-report" || got.scope != "current" || got.target != "agents" {
		t.Fatalf("config = %#v", got)
	}
}

func TestParseSkillsInstallFlagsTreatsSingleNameAsReleaseSkill(t *testing.T) {
	got, err := parseSkillsInstallFlags([]string{"agent-work-report", "--scope", "current", "--target", "agents"})
	if err != nil {
		t.Fatal(err)
	}
	if got.source != "" || got.skill != "agent-work-report" {
		t.Fatalf("config = %#v", got)
	}
}

func TestDispatchSelectionPrintsNewCommand(t *testing.T) {
	out := captureStdout(t, func() {
		err := dispatchSelection(context.Background(), []session.Provider{
			staticProvider{name: "codex"},
		}, ui.Selection{
			Kind:     ui.SelectionNew,
			Provider: "codex",
			CWD:      "/repo with spaces",
		}, true)
		if err != nil {
			t.Fatal(err)
		}
	})

	if !strings.Contains(out, `cd '/repo with spaces' && 'codex'`) {
		t.Fatalf("unexpected command: %s", out)
	}
}

func TestNewSessionProviderNamesIncludesOnlyLaunchableProviders(t *testing.T) {
	got := newSessionProviderNames(newProviders("", "", "", "", "", "", "", "", "", "", ""))
	want := []string{"codex", "claude", "kimi", "kiro", "opencode", "codebuddy", "cursor", "pi"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("new-session providers = %#v, want %#v", got, want)
	}
}

func TestTUIProviderChoiceDispatchesNewCommand(t *testing.T) {
	// A process-level TUI test is not portable in this repository because
	// Bubble Tea opens /dev/tty. Drive the complete model-selection-to-command
	// boundary here instead, without executing the selected agent command.
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	m := ui.NewWithDiscoveryOptions(session.DiscoveryResult{Sessions: []session.Session{
		{ID: "codex-session", Provider: "codex", CWD: "/repo with spaces", UpdatedAt: base},
		{ID: "claude-session", Provider: "claude", CWD: "/repo with spaces", UpdatedAt: base.Add(time.Hour)},
	}}, ui.ModelOptions{
		WindowDays:          30,
		StepDays:            30,
		NewSessionProviders: []string{"codex", "claude"},
	})

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	m = next.(ui.Model)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(ui.Model)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	selected, ok := next.(ui.Model).Selected()
	if !ok {
		t.Fatal("expected provider chooser selection")
	}

	out := captureStdout(t, func() {
		err := dispatchSelection(context.Background(), []session.Provider{
			staticProvider{name: "codex"},
			staticProvider{name: "claude"},
		}, selected, true)
		if err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, `cd '/repo with spaces' && 'codex'`) {
		t.Fatalf("unexpected selected new-session command: %s", out)
	}
}

func TestWithResumeCommandsSkipsUnsupportedProviders(t *testing.T) {
	got := withResumeCommands([]session.Session{
		{ID: "codex-session", Provider: "codex"},
		{ID: "agent:main:main", Provider: "openclaw"},
		{ID: "cursor-session", Provider: "cursor", Metadata: map[string]string{"cwd_error": "cursor project cwd is ambiguous"}},
	})

	if got[0].ResumeCommand == "" {
		t.Fatalf("codex resume command was not populated: %#v", got[0])
	}
	if got[1].ResumeCommand != "" {
		t.Fatalf("openclaw resume command = %q, want empty", got[1].ResumeCommand)
	}
	if got[2].ResumeCommand != "" {
		t.Fatalf("unavailable cursor resume command = %q, want empty", got[2].ResumeCommand)
	}
}

func TestFilterReportSessionsExcludesNonInteractiveByDefault(t *testing.T) {
	items := []session.Session{
		{ID: "human", Provider: "codex", Title: "human work"},
		{ID: "generated", Provider: "codebuddy", Title: "generated report", Metadata: map[string]string{"interaction_mode": "non_interactive"}},
	}

	got := filterReportSessions(items, reportConfig{})
	if len(got) != 1 || got[0].ID != "human" {
		t.Fatalf("sessions = %#v, want only human session", got)
	}

	got = filterReportSessions(items, reportConfig{includeNonInteractive: true})
	if len(got) != 2 {
		t.Fatalf("sessions = %#v, want both sessions", got)
	}
}

func TestFilterVisibleSessionsExcludesNonInteractiveByDefault(t *testing.T) {
	items := []session.Session{
		{ID: "human", Provider: "codex"},
		{ID: "automated", Provider: "codex", Metadata: map[string]string{"interaction_mode": "non_interactive"}},
	}

	got := filterVisibleSessions(items, false)
	if len(got) != 1 || got[0].ID != "human" {
		t.Fatalf("sessions = %#v, want only interactive session", got)
	}
	if got := filterVisibleSessions(items, true); len(got) != 2 {
		t.Fatalf("sessions = %#v, want all sessions", got)
	}
}

func TestResumeSessionRejectsUnavailableSession(t *testing.T) {
	err := resumeSession(context.Background(), executableProvider{name: "cursor"}, session.Session{
		ID:       "sid",
		Provider: "cursor",
		Metadata: map[string]string{"cwd_error": "cursor project cwd encoding is ambiguous"},
	}, true)

	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "session sid cwd is unavailable") {
		t.Fatalf("error = %v", err)
	}
}

type staticProvider struct {
	name  string
	err   error
	items []session.Session
}

func (p staticProvider) Name() string {
	return p.name
}

func (p staticProvider) Discover(session.DiscoverOptions) ([]session.Session, error) {
	if p.err != nil {
		return nil, p.err
	}
	if p.items != nil {
		return p.items, nil
	}
	return []session.Session{{ID: p.name + "-session", Provider: p.name}}, nil
}

func (p staticProvider) ResumeCommand(s session.Session) session.ExecSpec {
	return session.ExecSpec{Dir: s.CWD}
}

func (p staticProvider) NewCommand(cwd string) session.ExecSpec {
	return session.ExecSpec{Dir: cwd, Args: []string{p.name}}
}

type blockingProvider struct {
	name    string
	entered chan<- string
	release <-chan struct{}
}

type optionsProvider struct {
	name string
	got  session.DiscoverOptions
}

func (p *optionsProvider) Name() string { return p.name }

func (p *optionsProvider) Discover(opts session.DiscoverOptions) ([]session.Session, error) {
	p.got = opts
	return nil, nil
}

func (p *optionsProvider) ResumeCommand(session.Session) session.ExecSpec { return session.ExecSpec{} }

func (p *optionsProvider) NewCommand(string) session.ExecSpec { return session.ExecSpec{} }

func (p blockingProvider) Name() string {
	return p.name
}

func (p blockingProvider) Discover(session.DiscoverOptions) ([]session.Session, error) {
	p.entered <- p.name
	<-p.release
	return []session.Session{{ID: p.name + "-session", Provider: p.name}}, nil
}

func (p blockingProvider) ResumeCommand(s session.Session) session.ExecSpec {
	return session.ExecSpec{Dir: s.CWD}
}

func (p blockingProvider) NewCommand(cwd string) session.ExecSpec {
	return session.ExecSpec{Dir: cwd, Args: []string{p.name}}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() {
		os.Stdout = old
	}()

	fn()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

type executableProvider struct {
	name string
}

func (p executableProvider) Name() string {
	return p.name
}

func (p executableProvider) Discover(session.DiscoverOptions) ([]session.Session, error) {
	return nil, nil
}

func (p executableProvider) ResumeCommand(s session.Session) session.ExecSpec {
	return session.ExecSpec{Dir: s.CWD, Args: []string{"agent", "resume", s.ID}}
}

func (p executableProvider) NewCommand(cwd string) session.ExecSpec {
	return session.ExecSpec{Dir: cwd, Args: []string{"agent"}}
}
