package codebuddy

import (
	"encoding/json"
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
	cacheDir, err := os.MkdirTemp("", "asm-codebuddy-cache-*")
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

func TestParseSessionPrefersAITitleThenSummaryThenUser(t *testing.T) {
	input := strings.NewReader(`{"sessionId":"sid","cwd":"/repo","timestamp":"2026-06-13T01:00:00Z","role":"user","content":"first user title"}
{"sessionId":"sid","cwd":"/repo","timestamp":"2026-06-13T01:01:00Z","summary":"summary title"}
{"sessionId":"sid","cwd":"/repo","timestamp":"2026-06-13T01:02:00Z","ai-title":"AI title","message":{"role":"assistant","model":"codebuddy-model","content":"ok"}}
`)

	got, err := parseSession(input)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "sid" || got.CWD != "/repo" {
		t.Fatalf("unexpected session identity: %#v", got)
	}
	if got.Title != "AI title" || got.Metadata["title_source"] != "ai-title" {
		t.Fatalf("unexpected title metadata: %#v", got)
	}
	if got.Metadata["model"] != "codebuddy-model" {
		t.Fatalf("model = %q", got.Metadata["model"])
	}
}

func TestParseSessionUsesSummaryFallback(t *testing.T) {
	got, err := parseSession(strings.NewReader(`{"sessionId":"sid","cwd":"/repo","summary":"summary title","role":"user","content":"user title"}
`))
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "summary title" || got.Metadata["title_source"] != "summary" {
		t.Fatalf("unexpected title: %#v", got)
	}
}

func TestParseSessionUsesLastUserFallback(t *testing.T) {
	got, err := parseSession(strings.NewReader(`{"sessionId":"sid","cwd":"/repo","role":"user","content":"first title"}
{"sessionId":"sid","cwd":"/repo","role":"user","content":[{"type":"text","text":"last title"}]}
`))
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "last title" || got.Metadata["title_source"] != "user" {
		t.Fatalf("unexpected title: %#v", got)
	}
}

func TestParseSessionReadsNumericTimestampAndAgentMetadata(t *testing.T) {
	got, err := parseSession(strings.NewReader(`{"sessionId":"sid","cwd":"/repo","timestamp":1782266739927,"providerData":{"agent":"cli"},"role":"user","content":[{"type":"input_text","text":"non interactive prompt"}]}
{"sessionId":"sid","cwd":"/repo","timestamp":1782266746757,"providerData":{"agent":"cli"},"role":"assistant","content":"ok"}
`))
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "non interactive prompt" {
		t.Fatalf("Title = %q", got.Title)
	}
	if got.Metadata["agent"] != "cli" {
		t.Fatalf("metadata = %#v", got.Metadata)
	}
	if got.Metadata["interaction_mode"] != "" {
		t.Fatalf("agent=cli does not prove a non-interactive session: %#v", got.Metadata)
	}
	if got.CreatedAt.UTC().Format(time.RFC3339) != "2026-06-24T02:05:39Z" {
		t.Fatalf("CreatedAt = %s", got.CreatedAt.UTC().Format(time.RFC3339))
	}
	if got.UpdatedAt.UTC().Format(time.RFC3339) != "2026-06-24T02:05:46Z" {
		t.Fatalf("UpdatedAt = %s", got.UpdatedAt.UTC().Format(time.RFC3339))
	}
}

func TestReadUserPreviewsReadsNumericTimestamp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	writeFile(t, path, `{"sessionId":"sid","cwd":"/repo","timestamp":1782266739927,"providerData":{"agent":"cli"},"role":"user","content":[{"type":"input_text","text":"numeric timestamp prompt"}]}
`)

	got, oversized, err := readUserPreviews(path, session.PreviewOptions{
		UserMessagesPerEdge: 2,
		MaxChars:            500,
		Since:               time.UnixMilli(1782266739000),
		Before:              time.UnixMilli(1782266741000),
	})
	if err != nil {
		t.Fatal(err)
	}
	if oversized != 0 {
		t.Fatalf("oversized records = %d, want 0", oversized)
	}
	if len(got) != 1 || got[0].Text != "numeric timestamp prompt" {
		t.Fatalf("previews = %#v", got)
	}
}

func TestParseSessionReadsDecimalNumericTimestamp(t *testing.T) {
	got, err := parseSession(strings.NewReader(`{"sessionId":"sid","cwd":"/repo","timestamp":1782266739927.0,"role":"user","content":"decimal timestamp"}
`))
	if err != nil {
		t.Fatal(err)
	}
	if got.CreatedAt.UTC().Format(time.RFC3339) != "2026-06-24T02:05:39Z" {
		t.Fatalf("CreatedAt = %s", got.CreatedAt.UTC().Format(time.RFC3339))
	}
}

func TestParseSessionKeepsContentWithMalformedTimestamp(t *testing.T) {
	got, err := parseSession(strings.NewReader(`{"sessionId":"sid","cwd":"/repo","timestamp":"not-a-time","role":"user","content":"keep this title"}
`))
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "keep this title" || !got.CreatedAt.IsZero() {
		t.Fatalf("session = %#v", got)
	}
}

func TestDiscoverIndexesProjectsAndMarksCWD(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	sessionPath := filepath.Join(home, "projects", "encoded", "sid.jsonl")
	writeFile(t, sessionPath, `{"sessionId":"sid","cwd":`+jsonString(repo)+`,"timestamp":"2026-06-13T01:00:00Z","ai-title":"CodeBuddy title","model":"codebuddy-v1"}
`)

	got, err := New(home).Discover(session.DiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].ID != "sid" || got[0].Provider != Name || got[0].CWD != repo || got[0].Title != "CodeBuddy title" {
		t.Fatalf("unexpected session: %#v", got[0])
	}
	if got[0].Metadata["source_home"] != home || got[0].Metadata["model"] != "codebuddy-v1" {
		t.Fatalf("metadata = %#v", got[0].Metadata)
	}
}

func TestDiscoverReadsPastOversizedJSONLRecord(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	sessionPath := filepath.Join(home, "projects", "encoded", "sid.jsonl")
	writeFile(t, sessionPath, `{"sessionId":"sid","cwd":`+jsonString(repo)+`,"timestamp":"2026-06-13T01:00:00Z","role":"user","content":"before large output"}
{"sessionId":"sid","cwd":`+jsonString(repo)+`,"timestamp":"2026-06-13T01:00:01Z","role":"assistant","content":`+jsonString(strings.Repeat("x", 8*1024*1024))+`}
{"sessionId":"sid","cwd":`+jsonString(repo)+`,"timestamp":"2026-06-13T01:00:02Z","role":"user","content":"after large output"}
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
	var previews []string
	for _, preview := range got[0].Previews {
		previews = append(previews, preview.Text)
	}
	want := []string{"before large output", "after large output"}
	if strings.Join(previews, "|") != strings.Join(want, "|") {
		t.Fatalf("previews = %#v, want %#v", previews, want)
	}
	if got[0].Metadata[session.MetadataReportEvidenceStatus] != "" {
		t.Fatalf("known oversized assistant output should not reduce coverage: %#v", got[0].Metadata)
	}
}

func TestDiscoverKeepsSessionsWithMissingCWD(t *testing.T) {
	home := t.TempDir()
	sessionPath := filepath.Join(home, "projects", "encoded", "sid.jsonl")
	writeFile(t, sessionPath, `{"sessionId":"sid","timestamp":"2026-06-13T01:00:00Z","ai-title":"CodeBuddy title"}
`)

	got, err := New(home).Discover(session.DiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].ID != "sid" || got[0].CWD != "" || got[0].Metadata["cwd_missing"] != "true" {
		t.Fatalf("unexpected session: %#v", got[0])
	}
}

func TestDiscoverCacheLifecycleSinceLimitAndCWDRefresh(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "repo-created-after-warm")
	path := filepath.Join(home, "projects", "repo", "current.jsonl")
	writeCodeBuddyCacheFixture(t, path, "current", repo, "before primary change")
	cachePath := filepath.Join(t.TempDir(), "cache.json")
	provider := Provider{Home: home, CachePath: cachePath}

	cold, err := provider.Discover(session.DiscoverOptions{})
	if err != nil || len(cold) != 1 || cold[0].Title != "before primary change" || cold[0].Metadata["cwd_missing"] != "true" {
		t.Fatalf("cold = %#v err=%v", cold, err)
	}
	warm, err := provider.Discover(session.DiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	sessiontest.RequireEqual(t, cold, warm)
	withPreview, err := provider.Discover(session.DiscoverOptions{Preview: session.PreviewOptions{UserMessagesPerEdge: 1, MaxChars: 100}})
	if err != nil || len(withPreview[0].Previews) != 1 || withPreview[0].Previews[0].Text != "before primary change" {
		t.Fatalf("dynamic previews = %#v err=%v", withPreview, err)
	}
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	refreshed, err := provider.Discover(session.DiscoverOptions{})
	if err != nil || refreshed[0].Metadata["cwd_missing"] != "" {
		t.Fatalf("cwd refresh = %#v err=%v", refreshed, err)
	}
	writeCodeBuddyCacheFixture(t, path, "current", repo, "after primary file changed and grew")
	invalidated, err := provider.Discover(session.DiscoverOptions{})
	if err != nil || invalidated[0].Title != "after primary file changed and grew" {
		t.Fatalf("invalidated = %#v err=%v", invalidated, err)
	}
	if err := os.WriteFile(cachePath, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if recovered, err := provider.Discover(session.DiscoverOptions{}); err != nil || len(recovered) != 1 {
		t.Fatalf("corrupt-cache recovery = %#v err=%v", recovered, err)
	}

	oldPath := filepath.Join(home, "projects", "repo", "old.jsonl")
	writeCodeBuddyCacheFixture(t, oldPath, "old", repo, "old title")
	oldTime := time.Now().AddDate(0, 0, -60)
	if err := os.Chtimes(oldPath, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Discover(session.DiscoverOptions{}); err != nil {
		t.Fatal(err)
	}
	limited, err := provider.Discover(session.DiscoverOptions{Since: time.Now().AddDate(0, 0, -30), LimitFiles: 1})
	if err != nil || len(limited) != 1 || limited[0].ID != "current" {
		t.Fatalf("bounded limit = %#v err=%v", limited, err)
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

func TestResumeCommandUsesCodeBuddyResumeFromSessionCWD(t *testing.T) {
	spec := New("").ResumeCommand(session.Session{ID: "sid", CWD: "/repo"})

	if spec.Dir != "/repo" {
		t.Fatalf("Dir = %q", spec.Dir)
	}
	if strings.Join(spec.Args, " ") != "codebuddy --resume sid" {
		t.Fatalf("Args = %#v", spec.Args)
	}
}

func TestNewCommandUsesProjectCWD(t *testing.T) {
	spec := New("").NewCommand("/repo")

	if spec.Dir != "/repo" {
		t.Fatalf("Dir = %q", spec.Dir)
	}
	if strings.Join(spec.Args, " ") != "codebuddy" {
		t.Fatalf("Args = %#v", spec.Args)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeCodeBuddyCacheFixture(t *testing.T, path, id, cwd, title string) {
	t.Helper()
	writeFile(t, path, `{"sessionId":`+jsonString(id)+`,"cwd":`+jsonString(cwd)+`,"timestamp":"2026-06-13T01:00:00Z","role":"user","content":`+jsonString(title)+`,"ai-title":`+jsonString(title)+`}`+"\n")
}

func jsonString(value string) string {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(data)
}
