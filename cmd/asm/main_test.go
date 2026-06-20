package main

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

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

	done := make(chan struct {
		items []session.Session
		err   error
	}, 1)
	go func() {
		items, err := discoverAll(providers, 10, 30)
		done <- struct {
			items []session.Session
			err   error
		}{items: items, err: err}
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
		if got.err != nil {
			t.Fatal(got.err)
		}
		if len(got.items) != 2 {
			t.Fatalf("len = %d, want 2", len(got.items))
		}
		if got.items[0].Provider != "first" || got.items[1].Provider != "second" {
			t.Fatalf("items out of provider order: %#v", got.items)
		}
	case <-time.After(time.Second):
		t.Fatal("discoverAll did not return after providers were released")
	}
}

func TestDiscoverAllWrapsProviderErrorWithName(t *testing.T) {
	providers := []session.Provider{
		staticProvider{name: "ok"},
		staticProvider{name: "bad", err: errors.New("boom")},
	}

	_, err := discoverAll(providers, 10, 30)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "bad discover: boom") {
		t.Fatalf("unexpected error: %v", err)
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
