package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hxy91819/agent-session-manager/internal/session"
	"github.com/hxy91819/agent-session-manager/internal/sessioncache"
	"github.com/hxy91819/agent-session-manager/internal/sessiontest"
)

func TestMain(m *testing.M) {
	cacheDir, err := os.MkdirTemp("", "asm-claude-cache-*")
	if err != nil {
		panic(err)
	}
	if err := os.Setenv("XDG_CACHE_HOME", cacheDir); err != nil {
		panic(err)
	}
	code := m.Run()
	_ = os.RemoveAll(cacheDir)
	os.Exit(code)
}

func TestParseSessionExtractsClaudeFields(t *testing.T) {
	input := strings.NewReader(`{"type":"user","sessionId":"sid","cwd":"/repo","timestamp":"2026-06-13T01:00:00Z","gitBranch":"main","message":{"role":"user","content":[{"type":"text","text":"first prompt"}]}}
{"type":"assistant","sessionId":"sid","cwd":"/repo","timestamp":"2026-06-13T01:01:00Z","message":{"role":"assistant","model":"claude-sonnet-4","content":[]}}
{"type":"user","sessionId":"sid","cwd":"/repo","timestamp":"2026-06-13T01:02:00Z","message":{"role":"user","content":"latest   user prompt"}}
{"type":"summary","sessionId":"sid","summary":"Native Claude Title"}
`)

	got, err := parseSession(input)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "sid" {
		t.Fatalf("ID = %q", got.ID)
	}
	if got.CWD != "/repo" {
		t.Fatalf("CWD = %q", got.CWD)
	}
	if got.Title != "Native Claude Title" {
		t.Fatalf("Title = %q", got.Title)
	}
	if got.Metadata["title_source"] != "summary" {
		t.Fatalf("title_source = %q", got.Metadata["title_source"])
	}
	if got.Metadata["model"] != "claude-sonnet-4" {
		t.Fatalf("model = %q", got.Metadata["model"])
	}
	if got.Metadata["git_branch"] != "main" {
		t.Fatalf("git_branch = %q", got.Metadata["git_branch"])
	}
	if got.CreatedAt.Format(time.RFC3339) != "2026-06-13T01:00:00Z" {
		t.Fatalf("CreatedAt = %s", got.CreatedAt.Format(time.RFC3339))
	}
	if got.UpdatedAt.Format(time.RFC3339) != "2026-06-13T01:02:00Z" {
		t.Fatalf("UpdatedAt = %s", got.UpdatedAt.Format(time.RFC3339))
	}
}

func TestMetadataParseMatchesFullParseAcrossProducerFieldOrders(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "message before outer metadata",
			body: `{"message":{"role":"assistant","model":"claude-sonnet-4","content":"spoofed \\"role\\":\\"user\\""},"type":"assistant","uuid":"a","timestamp":"2026-06-13T01:01:00Z","entrypoint":"cli","promptSource":"interactive","cwd":"/latest","sessionId":"sid","gitBranch":"main"}
{"type":"user","message":{"role":"user","content":[{"type":"text","text":"real user title"}]},"timestamp":"2026-06-13T01:02:00Z","entrypoint":"cli","cwd":"/latest","sessionId":"sid","gitBranch":"main"}
{"type":"summary","summary":"Native Claude Title","sessionId":"sid"}
`,
		},
		{
			name: "nested unknown fields and escaped content",
			body: ` { "unknown" : [{"nested":[1,true,null,{"text":"} ] ,"}]}], "message" : {"content":[{"type":"tool_result","content":"large ignored value"}],"model":"claude-opus-4","role":"assistant"}, "sessionId":"nested", "cwd":"/repo", "timestamp":"2026-06-13T01:00:00Z", "type":"assistant" }
{"message":{"content":"fallback title","role":"user"},"isMeta":false,"type":"user","sessionId":"nested","cwd":"/repo","timestamp":"2026-06-13T01:01:00Z"}
`,
		},
		{
			name: "uncertain message shape falls back",
			body: `{"type":"user","message":"not-an-object","sessionId":"fallback","cwd":"/repo","timestamp":"2026-06-13T01:00:00Z"}
{"type":"title","title":"fallback title","sessionId":"fallback"}
`,
		},
		{
			name: "malformed duplicate message falls back",
			body: `{"type":"assistant","sessionId":"duplicate","cwd":"/repo","message":{"content":[1 2]},"message":{"role":"assistant","model":"claude-sonnet-4"}}
`,
		},
		{
			name: "outer trailing comma falls back",
			body: `{"type":"assistant","sessionId":"outer-comma","cwd":"/repo",}
`,
		},
		{
			name: "message trailing comma falls back",
			body: `{"type":"assistant","sessionId":"message-comma","cwd":"/repo","message":{"role":"assistant",}}
`,
		},
		{
			name: "case variant struct fields fall back",
			body: `{"TYPE":"user","sessionID":"case-variant","CWD":"/repo","Timestamp":"2026-06-13T01:00:00Z","Message":{"Role":"user","Content":"case title"}}
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			full, fullErr := parseSession(strings.NewReader(tt.body))
			metadata, metadataErr := parseSessionMetadata(strings.NewReader(tt.body))
			if fullErr != nil || metadataErr != nil {
				t.Fatalf("full err=%v metadata err=%v", fullErr, metadataErr)
			}
			sessiontest.RequireEqual(t, []session.Session{full}, []session.Session{metadata})
		})
	}
}

func TestParseSessionUsesLastHumanUserTitle(t *testing.T) {
	input := strings.NewReader(`{"type":"user","sessionId":"sid","cwd":"/repo","timestamp":"2026-06-13T01:00:00Z","isMeta":true,"message":{"role":"user","content":"ignored meta"}}
{"type":"user","sessionId":"sid","cwd":"/repo","timestamp":"2026-06-13T01:01:00Z","message":{"role":"user","content":"<system-reminder>ignore me</system-reminder>"}}
{"type":"user","sessionId":"sid","cwd":"/repo","timestamp":"2026-06-13T01:02:00Z","message":{"role":"user","content":[{"type":"text","text":"real\nprompt"}]}}
`)

	got, err := parseSession(input)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "real prompt" {
		t.Fatalf("Title = %q", got.Title)
	}
	if got.Metadata["title_source"] != "user" {
		t.Fatalf("title_source = %q", got.Metadata["title_source"])
	}
}

func TestParseSessionMarksPrintSessionNonInteractive(t *testing.T) {
	input := strings.NewReader(`{"type":"user","sessionId":"sid","cwd":"/repo","timestamp":"2026-06-24T02:28:31Z","entrypoint":"sdk-cli","promptSource":"sdk","message":{"role":"user","content":"non interactive prompt"}}
{"type":"assistant","sessionId":"sid","cwd":"/repo","timestamp":"2026-06-24T02:28:32Z","entrypoint":"sdk-cli","message":{"role":"assistant","model":"claude-sonnet-4","content":[]}}
`)

	got, err := parseSession(input)
	if err != nil {
		t.Fatal(err)
	}
	if got.Metadata["entrypoint"] != "sdk-cli" {
		t.Fatalf("entrypoint = %q, want sdk-cli", got.Metadata["entrypoint"])
	}
	if got.Metadata["prompt_source"] != "sdk" {
		t.Fatalf("prompt_source = %q, want sdk", got.Metadata["prompt_source"])
	}
	if got.Metadata["interaction_mode"] != "non_interactive" {
		t.Fatalf("interaction_mode = %q, want non_interactive", got.Metadata["interaction_mode"])
	}
}

func TestParseSessionKeepsInteractiveSessionWithSDKPromptInteractive(t *testing.T) {
	input := strings.NewReader(`{"type":"user","sessionId":"sid","cwd":"/repo","timestamp":"2026-06-24T02:28:31Z","entrypoint":"claude-vscode","promptSource":"sdk","message":{"role":"user","content":"prompt forwarded by an interactive client"}}
{"type":"user","sessionId":"sid","cwd":"/repo","timestamp":"2026-06-24T02:29:31Z","entrypoint":"cli","promptSource":"typed","message":{"role":"user","content":"typed follow up"}}
`)

	got, err := parseSession(input)
	if err != nil {
		t.Fatal(err)
	}
	if got.Metadata["interaction_mode"] != "" {
		t.Fatalf("interaction_mode = %q, want interactive", got.Metadata["interaction_mode"])
	}
	if got.Metadata["entrypoint"] != "cli" || got.Metadata["prompt_source"] != "typed" {
		t.Fatalf("final interaction metadata = %#v", got.Metadata)
	}
}

func TestParseSessionIgnoresContinuationSummaryTitle(t *testing.T) {
	input := strings.NewReader(`{"type":"user","sessionId":"sid","cwd":"/repo","timestamp":"2026-06-13T01:00:00Z","message":{"role":"user","content":"real follow up"}}
{"type":"summary","sessionId":"sid","summary":"This session is being continued from a previous conversation that ran out of context. Summary: OpenClaw testing hierarchy"}
`)

	got, err := parseSession(input)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "real follow up" {
		t.Fatalf("Title = %q, want real user title", got.Title)
	}
	if got.Metadata["title_source"] != "user" {
		t.Fatalf("title_source = %q, want user", got.Metadata["title_source"])
	}
}

func TestDiscoverReadsUserPreviews(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	projectDir := filepath.Join(home, "projects", "-repo")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(projectDir, "session.jsonl"), `{"type":"user","sessionId":"sid","cwd":`+jsonString(repo)+`,"timestamp":"2026-06-13T01:00:00Z","isMeta":true,"message":{"role":"user","content":"ignored meta"}}
{"type":"user","sessionId":"sid","cwd":`+jsonString(repo)+`,"timestamp":"2026-06-13T01:00:01Z","message":{"role":"user","content":"<system-reminder>ignore me</system-reminder>"}}
{"type":"user","sessionId":"sid","cwd":`+jsonString(repo)+`,"timestamp":"2026-06-13T01:00:02Z","message":{"role":"user","content":"first prompt"}}
{"type":"user","sessionId":"sid","cwd":`+jsonString(repo)+`,"timestamp":"2026-06-13T01:00:03Z","message":{"role":"user","content":"second prompt"}}
{"type":"user","sessionId":"sid","cwd":`+jsonString(repo)+`,"timestamp":"2026-06-13T01:00:04Z","message":{"role":"user","content":"third prompt"}}
{"type":"user","sessionId":"sid","cwd":`+jsonString(repo)+`,"timestamp":"2026-06-13T01:00:05Z","message":{"role":"user","content":"fourth prompt"}}
{"type":"user","sessionId":"sid","cwd":`+jsonString(repo)+`,"timestamp":"2026-06-13T01:00:06Z","message":{"role":"user","content":"fifth prompt"}}
`)

	got, err := New(home).Discover(session.DiscoverOptions{
		Preview: session.PreviewOptions{UserMessagesPerEdge: 2, MaxChars: 500},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	want := []string{"first prompt", "second prompt", "fourth prompt", "fifth prompt"}
	if texts := previewTexts(got[0].Previews); strings.Join(texts, "|") != strings.Join(want, "|") {
		t.Fatalf("previews = %#v, want %#v", texts, want)
	}
}

func TestDiscoverReadsPastOversizedJSONLRecord(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	sessionPath := filepath.Join(home, "projects", "-repo", "session.jsonl")
	if err := os.MkdirAll(filepath.Dir(sessionPath), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, sessionPath, `{"type":"user","sessionId":"sid","cwd":`+jsonString(repo)+`,"timestamp":"2026-06-13T01:00:00Z","message":{"role":"user","content":"before large output"}}
{"type":"assistant","sessionId":"sid","cwd":`+jsonString(repo)+`,"timestamp":"2026-06-13T01:00:01Z","message":{"role":"assistant","content":`+jsonString(strings.Repeat("x", 8*1024*1024))+`}}
{"type":"user","sessionId":"sid","cwd":`+jsonString(repo)+`,"timestamp":"2026-06-13T01:00:02Z","message":{"role":"user","content":"after large output"}}
`)

	got, err := New(home).Discover(session.DiscoverOptions{
		Preview: session.PreviewOptions{UserMessagesPerEdge: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Title != "after large output" {
		t.Fatalf("sessions = %#v", got)
	}
	want := []string{"before large output", "after large output"}
	if texts := previewTexts(got[0].Previews); strings.Join(texts, "|") != strings.Join(want, "|") {
		t.Fatalf("previews = %#v, want %#v", texts, want)
	}
	if got[0].Metadata[session.MetadataReportEvidenceStatus] != "" {
		t.Fatalf("known oversized assistant output should not reduce coverage: %#v", got[0].Metadata)
	}
}

func TestDiscoverFiltersAndLimitsByFileModTime(t *testing.T) {
	home := t.TempDir()
	projectDir := filepath.Join(home, "projects", "-repo")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	oldPath := filepath.Join(projectDir, "old.jsonl")
	newPath := filepath.Join(projectDir, "new.jsonl")
	writeClaudeSession(t, oldPath, "old", repo, "old title")
	writeClaudeSession(t, newPath, "new", repo, "new title")

	since := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	oldTime := since.Add(-time.Hour)
	newTime := since.Add(time.Hour)
	if err := os.Chtimes(oldPath, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(newPath, newTime, newTime); err != nil {
		t.Fatal(err)
	}

	got, err := New(home).Discover(session.DiscoverOptions{Since: since, LimitFiles: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].ID != "new" || got[0].Title != "new title" {
		t.Fatalf("unexpected session: %#v", got[0])
	}
	if !got[0].UpdatedAt.Equal(newTime) {
		t.Fatalf("UpdatedAt = %s, want %s", got[0].UpdatedAt, newTime)
	}
}

func TestDiscoverDeduplicatesSessionIDByNewestFile(t *testing.T) {
	home := t.TempDir()
	projectA := filepath.Join(home, "projects", "-repo-a")
	projectB := filepath.Join(home, "projects", "-repo-b")
	if err := os.MkdirAll(projectA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(projectB, 0o755); err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	oldPath := filepath.Join(projectA, "sid.jsonl")
	newPath := filepath.Join(projectB, "sid-copy.jsonl")
	writeClaudeSession(t, oldPath, "sid", repo, "old title")
	writeClaudeSession(t, newPath, "sid", repo, "new title")

	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(oldPath, base, base); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(newPath, base.Add(time.Hour), base.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	got, err := New(home).Discover(session.DiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].ID != "sid" || got[0].Title != "new title" || got[0].Path != newPath {
		t.Fatalf("unexpected session: %#v", got[0])
	}
}

func TestDiscoverMergesExtraHomesAndDeduplicatesByNewestID(t *testing.T) {
	defaultHome := t.TempDir()
	extraHome := t.TempDir()
	repo := t.TempDir()
	t.Setenv("CLAUDE_HOME", defaultHome)
	t.Setenv("ASM_CLAUDE_EXTRA_HOMES", extraHome)

	defaultProject := filepath.Join(defaultHome, "projects", "-repo-a")
	extraProject := filepath.Join(extraHome, "projects", "-repo-b")
	if err := os.MkdirAll(defaultProject, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(extraProject, 0o755); err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(defaultProject, "sid.jsonl")
	newPath := filepath.Join(extraProject, "sid-copy.jsonl")
	extraPath := filepath.Join(extraProject, "extra.jsonl")
	writeClaudeSession(t, oldPath, "sid", repo, "old title")
	writeClaudeSession(t, newPath, "sid", repo, "new title")
	writeClaudeSession(t, extraPath, "extra", repo, "extra title")

	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(oldPath, base, base); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(extraPath, base.Add(time.Hour), base.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(newPath, base.Add(2*time.Hour), base.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}

	provider := Provider{CachePath: filepath.Join(t.TempDir(), "cache.json")}
	got, err := provider.Discover(session.DiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2: %#v", len(got), got)
	}
	if got[0].ID != "sid" || got[0].Title != "new title" || got[0].Path != newPath {
		t.Fatalf("unexpected newest duplicate: %#v", got[0])
	}
	if got[0].Metadata["source_home"] != extraHome {
		t.Fatalf("source_home = %q", got[0].Metadata["source_home"])
	}
}

func TestDiscoverExplicitHomeIgnoresExtraHomes(t *testing.T) {
	flagHome := t.TempDir()
	extraHome := t.TempDir()
	repo := t.TempDir()
	t.Setenv("CLAUDE_HOME", t.TempDir())
	t.Setenv("ASM_CLAUDE_EXTRA_HOMES", extraHome)

	flagProject := filepath.Join(flagHome, "projects", "-repo-a")
	extraProject := filepath.Join(extraHome, "projects", "-repo-b")
	if err := os.MkdirAll(flagProject, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(extraProject, 0o755); err != nil {
		t.Fatal(err)
	}
	writeClaudeSession(t, filepath.Join(flagProject, "flag.jsonl"), "flag", repo, "flag title")
	writeClaudeSession(t, filepath.Join(extraProject, "extra.jsonl"), "extra", repo, "extra title")

	provider := Provider{Home: flagHome, CachePath: filepath.Join(t.TempDir(), "cache.json")}
	got, err := provider.Discover(session.DiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "flag" {
		t.Fatalf("unexpected sessions: %#v", got)
	}
}

func TestDiscoverMarksMissingCWD(t *testing.T) {
	home := t.TempDir()
	projectDir := filepath.Join(home, "projects", "-repo")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(home, "missing")
	writeClaudeSession(t, filepath.Join(projectDir, "session.jsonl"), "sid", missing, "title")

	got, err := New(home).Discover(session.DiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].Metadata["cwd_missing"] != "true" {
		t.Fatalf("cwd_missing = %q", got[0].Metadata["cwd_missing"])
	}
}

func TestDiscoverRefreshesCWDStatusWhenUsingCache(t *testing.T) {
	home := t.TempDir()
	projectDir := filepath.Join(home, "projects", "-repo")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(home, "repo")
	writeClaudeSession(t, filepath.Join(projectDir, "session.jsonl"), "sid", repo, "title")
	provider := Provider{
		Home:      home,
		CachePath: filepath.Join(t.TempDir(), "cache.json"),
	}

	got, err := provider.Discover(session.DiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Metadata["cwd_missing"] != "true" {
		t.Fatalf("first discovery did not mark missing cwd: %#v", got)
	}

	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err = provider.Discover(session.DiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].Metadata["cwd_missing"] != "" || got[0].Metadata["cwd_error"] != "" {
		t.Fatalf("cached discovery kept stale cwd metadata: %#v", got[0].Metadata)
	}
}

func TestDiscoverCacheLifecycleAndBoundedScanPreservesHistory(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	projectDir := filepath.Join(home, "projects", "repo")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(projectDir, "current.jsonl")
	writeClaudeSession(t, path, "current", repo, "before primary change")
	cachePath := filepath.Join(t.TempDir(), "cache.json")
	provider := Provider{Home: home, CachePath: cachePath}

	cold, err := provider.Discover(session.DiscoverOptions{})
	if err != nil || len(cold) != 1 || cold[0].Title != "before primary change" {
		t.Fatalf("cold = %#v err=%v", cold, err)
	}
	warm, err := provider.Discover(session.DiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	sessiontest.RequireEqual(t, cold, warm)
	writeClaudeSession(t, path, "current", repo, "after primary file changed and grew")
	invalidated, err := provider.Discover(session.DiscoverOptions{})
	if err != nil || invalidated[0].Title != "after primary file changed and grew" {
		t.Fatalf("invalidated = %#v err=%v", invalidated, err)
	}
	if err := os.WriteFile(cachePath, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	recovered, err := provider.Discover(session.DiscoverOptions{})
	if err != nil || len(recovered) != 1 || recovered[0].Title != "after primary file changed and grew" {
		t.Fatalf("corrupt-cache recovery = %#v err=%v", recovered, err)
	}

	oldPath := filepath.Join(projectDir, "old.jsonl")
	writeClaudeSession(t, oldPath, "old", repo, "old title")
	oldTime := time.Now().AddDate(0, 0, -60)
	if err := os.Chtimes(oldPath, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Discover(session.DiscoverOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Discover(session.DiscoverOptions{Since: time.Now().AddDate(0, 0, -30)}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(oldPath)
	if err != nil {
		t.Fatal(err)
	}
	_, ok := sessioncache.Load(cachePath).Get(sessioncache.FileIdentity{
		Provider: Name, Path: oldPath, Size: info.Size(), ModTime: info.ModTime(),
	})
	if !ok {
		t.Fatal("bounded scan pruned the historical cache entry")
	}
}

func TestDiscoverCacheMissWorkersProduceEquivalentResults(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	root := filepath.Join(home, "projects", "repo")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 8, 10, 1, 0, 0, 0, time.UTC)
	var changedPath string
	for i := range 12 {
		path := filepath.Join(root, fmt.Sprintf("session-%02d.jsonl", i))
		if i == 0 {
			changedPath = path
		}
		writeClaudeSession(t, path, fmt.Sprintf("session-%02d", i), repo, fmt.Sprintf("request %02d", i))
		modTime := base.Add(time.Duration(10+i) * time.Minute)
		if err := os.Chtimes(path, modTime, modTime); err != nil {
			t.Fatal(err)
		}
	}
	duplicate := filepath.Join(root, "duplicate-old.jsonl")
	writeClaudeSession(t, duplicate, "session-01", repo, "older duplicate")
	if err := os.Chtimes(duplicate, base.Add(time.Minute), base.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	invalid := filepath.Join(root, "invalid.jsonl")
	writeFile(t, invalid, "not-json\n")
	if err := os.Chtimes(invalid, base.Add(25*time.Minute), base.Add(25*time.Minute)); err != nil {
		t.Fatal(err)
	}

	serial := Provider{Home: home, CachePath: filepath.Join(t.TempDir(), "serial.json"), parseWorkers: 1}
	parallel := Provider{Home: home, CachePath: filepath.Join(t.TempDir(), "parallel.json"), parseWorkers: 8}

	serialCold, err := serial.Discover(session.DiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	parallelCold, err := parallel.Discover(session.DiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	sessiontest.RequireEqual(t, serialCold, parallelCold)
	if len(serialCold) != 12 {
		t.Fatalf("sessions = %d, want 12", len(serialCold))
	}

	serialWarm, err := serial.Discover(session.DiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	parallelWarm, err := parallel.Discover(session.DiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	sessiontest.RequireEqual(t, serialCold, serialWarm)
	sessiontest.RequireEqual(t, parallelCold, parallelWarm)

	writeClaudeSession(t, changedPath, "session-00", repo, "request changed after cache warmup")
	serialChanged, err := serial.Discover(session.DiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	parallelChanged, err := parallel.Discover(session.DiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	sessiontest.RequireEqual(t, serialChanged, parallelChanged)

	limitedOpts := session.DiscoverOptions{LimitFiles: 7}
	limitedSerial := Provider{Home: home, CachePath: filepath.Join(t.TempDir(), "limited-serial.json"), parseWorkers: 1}
	limitedParallel := Provider{Home: home, CachePath: filepath.Join(t.TempDir(), "limited-parallel.json"), parseWorkers: 8}
	serialLimited, err := limitedSerial.Discover(limitedOpts)
	if err != nil {
		t.Fatal(err)
	}
	parallelLimited, err := limitedParallel.Discover(limitedOpts)
	if err != nil {
		t.Fatal(err)
	}
	sessiontest.RequireEqual(t, serialLimited, parallelLimited)

	previewOpts := session.DiscoverOptions{Preview: session.PreviewOptions{UserMessagesPerEdge: 2}}
	serialReport, err := serial.Discover(previewOpts)
	if err != nil {
		t.Fatal(err)
	}
	parallelReport, err := parallel.Discover(previewOpts)
	if err != nil {
		t.Fatal(err)
	}
	sessiontest.RequireEqual(t, serialReport, parallelReport)
}

func TestReportDiscoveryReplacesMetadataOnlyCache(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	path := filepath.Join(home, "projects", "repo", "report.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	writeClaudeSession(t, path, "report", repo, "report title")
	cachePath := filepath.Join(t.TempDir(), "cache.json")
	provider := Provider{Home: home, CachePath: cachePath}

	ordinary, err := provider.Discover(session.DiscoverOptions{})
	if err != nil || len(ordinary) != 1 {
		t.Fatalf("ordinary = %#v err=%v", ordinary, err)
	}
	if ordinary[0].Metadata[metadataParseCacheKey] != "" {
		t.Fatalf("parse mode leaked from ordinary discovery: %#v", ordinary[0].Metadata)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	id := sessioncache.FileIdentity{Provider: Name, Path: path, Size: info.Size(), ModTime: info.ModTime()}
	cached, ok := sessioncache.Load(cachePath).Get(id)
	if !ok || cached.Metadata[metadataParseCacheKey] != metadataParseCacheValue {
		t.Fatalf("ordinary cache = %#v, ok=%v", cached, ok)
	}

	preview := session.PreviewOptions{UserMessagesPerEdge: 1, MaxChars: 200, Since: time.Unix(0, 0)}
	report, err := provider.Discover(session.DiscoverOptions{Preview: preview})
	if err != nil || len(report) != 1 || len(report[0].Previews) != 1 {
		t.Fatalf("report = %#v err=%v", report, err)
	}
	cached, ok = sessioncache.Load(cachePath).Get(id)
	if !ok || cached.Metadata[metadataParseCacheKey] != "" {
		t.Fatalf("report cache = %#v, ok=%v", cached, ok)
	}
}

func TestResumeCommandUsesClaudeResumeFromSessionCWD(t *testing.T) {
	spec := New("").ResumeCommand(session.Session{ID: "sid", CWD: "/repo"})

	if spec.Dir != "/repo" {
		t.Fatalf("Dir = %q", spec.Dir)
	}
	if strings.Join(spec.Args, " ") != "claude --resume sid" {
		t.Fatalf("Args = %#v", spec.Args)
	}
}

func TestNewCommandUsesProjectCWD(t *testing.T) {
	spec := New("").NewCommand("/repo")

	if spec.Dir != "/repo" {
		t.Fatalf("Dir = %q", spec.Dir)
	}
	if strings.Join(spec.Args, " ") != "claude" {
		t.Fatalf("Args = %#v", spec.Args)
	}
}

func writeClaudeSession(t *testing.T, path, id, cwd, title string) {
	t.Helper()
	writeFile(t, path, `{"type":"user","sessionId":`+jsonString(id)+`,"cwd":`+jsonString(cwd)+`,"timestamp":"2026-06-13T01:00:00Z","message":{"role":"user","content":`+jsonString(title)+`}}
`)
}

func jsonString(value string) string {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(data)
}

func previewTexts(previews []session.MessagePreview) []string {
	out := make([]string, 0, len(previews))
	for _, preview := range previews {
		out = append(out, preview.Text)
	}
	return out
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
