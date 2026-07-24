package codebuddy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hxy91819/agent-session-manager/internal/session"
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

func TestParseSessionReadsNumericTimestampAndNonInteractiveAgent(t *testing.T) {
	got, err := parseSession(strings.NewReader(`{"sessionId":"sid","cwd":"/repo","timestamp":1782266739927,"providerData":{"agent":"cli"},"role":"user","content":[{"type":"input_text","text":"non interactive prompt"}]}
{"sessionId":"sid","cwd":"/repo","timestamp":1782266746757,"providerData":{"agent":"cli"},"role":"assistant","content":"ok"}
`))
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "non interactive prompt" {
		t.Fatalf("Title = %q", got.Title)
	}
	if got.Metadata["agent"] != "cli" || got.Metadata["interaction_mode"] != "non_interactive" {
		t.Fatalf("metadata = %#v", got.Metadata)
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
	if got[0].Metadata[session.MetadataReportEvidenceStatus] != session.ReportEvidencePartial ||
		got[0].Metadata[session.MetadataReportEvidenceNote] == "" {
		t.Fatalf("metadata = %#v", got[0].Metadata)
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

func jsonString(value string) string {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(data)
}
