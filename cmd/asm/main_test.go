package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hxy91819/agent-session-manager/internal/session"
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
