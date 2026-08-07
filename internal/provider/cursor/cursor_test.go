package cursor

import (
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
	cacheDir, err := os.MkdirTemp("", "asm-cursor-cache-*")
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

func TestDiscoverIndexesMainTranscriptAndSkipsSubagents(t *testing.T) {
	home := t.TempDir()
	repoParent := filepath.Join(home, "workspace")
	repo := filepath.Join(repoParent, "app")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	projectKey := "workspace-app"
	chatID := "chat-123"
	transcript := filepath.Join(home, "projects", projectKey, "agent-transcripts", chatID, chatID+".jsonl")
	writeFile(t, filepath.Join(home, "projects", projectKey, "worker.log"), `[info] Getting tree structure for workspacePath=`+repo+`
`)
	writeFile(t, transcript, `{"role":"user","message":{"content":[{"type":"text","text":"first cursor prompt"}]}}
{"role":"assistant","message":{"content":[{"type":"text","text":"ok"}]}}
{"role":"user","message":{"content":[{"type":"text","text":"last cursor prompt"}]}}
`)
	writeFile(t, filepath.Join(home, "projects", projectKey, "agent-transcripts", chatID, "subagents", "child", "child.jsonl"), `{"role":"user","content":"ignored"}
`)

	got, err := New(home).Discover(session.DiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1: %#v", len(got), got)
	}
	if got[0].ID != chatID || got[0].Provider != Name || got[0].CWD != repo {
		t.Fatalf("unexpected session identity: %#v", got[0])
	}
	if got[0].Title != "first cursor prompt" || got[0].Metadata["title_source"] != "first_user" {
		t.Fatalf("unexpected title: %#v", got[0])
	}
	if got[0].Metadata["cwd_error"] != "" || got[0].Metadata["cwd_missing"] != "" {
		t.Fatalf("metadata = %#v", got[0].Metadata)
	}
}

func TestDiscoverReadsPastOversizedJSONLRecord(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "workspace")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	projectKey := "workspace"
	chatID := "chat-oversized"
	transcript := filepath.Join(home, "projects", projectKey, "agent-transcripts", chatID, chatID+".jsonl")
	writeFile(t, filepath.Join(home, "projects", projectKey, "worker.log"), "workspacePath="+repo+"\n")
	writeFile(t, transcript, `{"role":"user","timestamp":"2026-06-13T01:00:00Z","content":"before large output"}
{"role":"assistant","timestamp":"2026-06-13T01:00:01Z","content":"`+strings.Repeat("x", 8*1024*1024)+`"}
{"role":"user","timestamp":"2026-06-13T01:00:02Z","content":"after large output"}
`)

	got, err := New(home).Discover(session.DiscoverOptions{
		Preview: session.PreviewOptions{UserMessagesPerEdge: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Title != "before large output" {
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

func TestDiscoverMarksAutoreviewTempSessionNonInteractive(t *testing.T) {
	home := t.TempDir()
	projectKey := "tmp-autoreview-cursor-agent-fixture"
	chatID := "automated-chat"
	autoreviewCWD := filepath.Join(os.TempDir(), "autoreview-cursor-agent.fixture")
	projectDir := filepath.Join(home, "projects", projectKey)
	writeFile(t, filepath.Join(projectDir, "worker.log"), "workspacePath="+autoreviewCWD+"\n")
	writeFile(t, filepath.Join(projectDir, "agent-transcripts", chatID, chatID+".jsonl"), `{"role":"user","message":{"content":[{"type":"text","text":"review this change"}]}}
{"role":"assistant","message":{"content":[{"type":"tool_use","name":"Shell"}]}}
`)

	got, err := New(home).Discover(session.DiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("sessions = %#v, want one", got)
	}
	if got[0].Metadata["automation"] != "autoreview" || got[0].Metadata["interaction_mode"] != "non_interactive" {
		t.Fatalf("metadata = %#v", got[0].Metadata)
	}
}

func TestIsAutoreviewTempCWDIsStableAcrossTempRoots(t *testing.T) {
	if !isAutoreviewTempCWD(filepath.Join(os.TempDir(), "autoreview-cursor-agent.fixture")) {
		t.Fatal("expected autoreview temp cwd to match")
	}
	if !isAutoreviewTempCWD(filepath.Join(t.TempDir(), "nested", "autoreview-fixture.fixture")) {
		t.Fatal("expected autoreview cwd from a different temp root to match")
	}
	if isAutoreviewTempCWD(filepath.Join(os.TempDir(), "ordinary-project")) ||
		isAutoreviewTempCWD("autoreview-cursor-agent.relative") {
		t.Fatal("unexpected non-autoreview cwd match")
	}
}

func TestParseSessionDoesNotInferNonInteractiveFromOneTurnWrapper(t *testing.T) {
	input := strings.NewReader(`{"role":"user","message":{"content":[{"type":"text","text":"<timestamp>Wednesday, Jun 24, 2026, 2:27 AM (UTC)</timestamp>\n<user_query>\nnon interactive prompt\n</user_query>"}]}}
{"role":"assistant","message":{"content":[{"type":"text","text":"ok"}]}}
{"type":"turn_ended","status":"success"}
`)

	got, err := parseSession(input)
	if err != nil {
		t.Fatal(err)
	}
	if got.Metadata["interaction_mode"] != "" {
		t.Fatalf("ambiguous one-turn sessions must remain interactive: %#v", got.Metadata)
	}
}

func TestParseSessionReadsInputTextBlocks(t *testing.T) {
	input := strings.NewReader(`{"role":"user","message":{"content":[{"type":"input_text","text":"<timestamp>Wednesday, Jun 24, 2026, 2:27 AM (UTC)</timestamp>\n<user_query>\ninput text prompt\n</user_query>"}]}}
{"role":"assistant","message":{"content":[{"type":"text","text":"ok"}]}}
`)

	got, err := parseSession(input)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Title, "input text prompt") {
		t.Fatalf("Title = %q", got.Title)
	}
	if got.Metadata["interaction_mode"] != "" {
		t.Fatalf("input_text does not prove a non-interactive entrypoint: %#v", got.Metadata)
	}
	if got.Title != "input text prompt" {
		t.Fatalf("Title = %q, want unwrapped prompt", got.Title)
	}
}

func TestReadUserPreviewsUsesCursorEmbeddedTimestamp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	writeFile(t, path, `{"role":"user","message":{"content":[{"type":"text","text":"<timestamp>Friday, Jul 24, 2026, 2:16 PM (UTC+8)</timestamp>\n<user_query>\nreview the release\n</user_query>"}]}}
`)

	start := time.Date(2026, 7, 24, 14, 0, 0, 0, time.FixedZone("UTC+8", 8*60*60))
	got, oversized, err := readUserPreviews(path, session.PreviewOptions{
		UserMessagesPerEdge: 1,
		MaxChars:            500,
		Since:               start,
		Before:              start.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if oversized != 0 {
		t.Fatalf("oversized = %d, want 0", oversized)
	}
	if len(got) != 1 {
		t.Fatalf("previews = %#v, want one", got)
	}
	if got[0].Text != "review the release" {
		t.Fatalf("Text = %q, want unwrapped prompt", got[0].Text)
	}
	if !got[0].At.Equal(time.Date(2026, 7, 24, 6, 16, 0, 0, time.UTC)) {
		t.Fatalf("At = %s, want 2026-07-24T06:16:00Z", got[0].At)
	}
}

func TestDiscoverMarksDecodedMissingCWD(t *testing.T) {
	home := t.TempDir()
	chatID := "chat-missing"
	writeFile(t, filepath.Join(home, "projects", "tmp-missing-repo", "agent-transcripts", chatID, chatID+".jsonl"), `{"role":"user","content":"missing cwd"}
`)

	got, err := New(home).Discover(session.DiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].CWD != "" {
		t.Fatalf("CWD = %q", got[0].CWD)
	}
	if got[0].Metadata["cwd_error"] != "cursor project cwd encoding is ambiguous" {
		t.Fatalf("metadata = %#v", got[0].Metadata)
	}
}

func TestDiscoverCacheLifecycleSinceLimitAndWorkspaceRefresh(t *testing.T) {
	home := t.TempDir()
	repoBefore := filepath.Join(home, "repo-before")
	repoAfter := filepath.Join(home, "repo-after")
	if err := os.MkdirAll(repoBefore, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(repoAfter, 0o755); err != nil {
		t.Fatal(err)
	}
	projectDir := filepath.Join(home, "projects", "project")
	workerLog := filepath.Join(projectDir, "worker.log")
	writeFile(t, workerLog, "[info] Getting tree structure for workspacePath="+repoBefore+"\n")
	path := filepath.Join(projectDir, "agent-transcripts", "current", "current.jsonl")
	writeCursorCacheFixture(t, path, "before primary change")
	cachePath := filepath.Join(t.TempDir(), "cache.json")
	provider := Provider{Home: home, CachePath: cachePath}

	cold, err := provider.Discover(session.DiscoverOptions{})
	if err != nil || len(cold) != 1 || cold[0].Title != "before primary change" || cold[0].CWD != repoBefore {
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
	writeFile(t, workerLog, "[info] Getting tree structure for workspacePath="+repoAfter+"\n")
	refreshed, err := provider.Discover(session.DiscoverOptions{})
	if err != nil || refreshed[0].CWD != repoAfter {
		t.Fatalf("workspace refresh = %#v err=%v", refreshed, err)
	}
	writeCursorCacheFixture(t, path, "after primary file changed and grew")
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

	oldPath := filepath.Join(projectDir, "agent-transcripts", "old", "old.jsonl")
	writeCursorCacheFixture(t, oldPath, "old title")
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

func TestDecodeProjectCWDMarksHyphenatedKeysUnavailable(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "workspace", "my-app")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	projectKey := strings.TrimPrefix(strings.ReplaceAll(repo, string(os.PathSeparator), "-"), "-")

	got := decodeProjectCWD(projectKey)
	if got.CWD != "" || got.Error != "cursor project cwd encoding is ambiguous" {
		t.Fatalf("decodeProjectCWD(%q) = %#v, want unavailable fallback", projectKey, got)
	}
}

func TestDecodeProjectCWDRestoresLeadingSlashForSingleSegmentKeys(t *testing.T) {
	got := decodeProjectCWD("asmcursormissing")
	if got.CWD != "/asmcursormissing" || !got.Missing || got.Error != "" {
		t.Fatalf("decodeProjectCWD(asmcursormissing) = %#v, want missing /asmcursormissing", got)
	}
}

func TestDecodeProjectCWDAcceptsEscapedPOSIXAbsolutePath(t *testing.T) {
	if _, err := os.Stat("/tmp"); err != nil {
		t.Skip("/tmp is not available")
	}

	got := decodeProjectCWD("%2Ftmp")
	if got.CWD != "/tmp" || got.Missing || got.Error != "" {
		t.Fatalf("decodeProjectCWD(%%2Ftmp) = %#v, want /tmp", got)
	}
}

func TestReadWorkspacePathPreservesSpaces(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "worker.log")
	writeFile(t, path, `[info] Getting tree structure for workspacePath=/tmp/Code Review/repo
`)

	got := readWorkspacePath(path)
	if got != "/tmp/Code Review/repo" {
		t.Fatalf("workspacePath = %q", got)
	}
}

func TestCheckedCWDReportsNonMissingStatErrors(t *testing.T) {
	got := checkedCWD("bad\x00path")

	if got.Error == "" || got.Missing {
		t.Fatalf("checkedCWD returned %#v, want cwd_error", got)
	}
}

func TestResumeCommandUsesCursorAgentResumeFromSessionCWD(t *testing.T) {
	spec := New("").ResumeCommand(session.Session{ID: "chat-123", CWD: "/repo"})

	if spec.Dir != "/repo" {
		t.Fatalf("Dir = %q", spec.Dir)
	}
	if strings.Join(spec.Args, " ") != "cursor-agent --resume chat-123" {
		t.Fatalf("Args = %#v", spec.Args)
	}
}

func TestResumeCommandRejectsUnavailableCursorCWD(t *testing.T) {
	spec := New("").ResumeCommand(session.Session{
		ID:       "chat-123",
		CWD:      "/repo",
		Metadata: map[string]string{"cwd_error": "cursor project cwd is ambiguous"},
	})

	if spec.UnsupportedReason != "Cursor resume cwd is unavailable or ambiguous" {
		t.Fatalf("UnsupportedReason = %q", spec.UnsupportedReason)
	}
	if len(spec.Args) != 0 {
		t.Fatalf("Args = %#v", spec.Args)
	}
}

func TestNewCommandUsesProjectCWD(t *testing.T) {
	spec := New("").NewCommand("/repo")

	if spec.Dir != "/repo" {
		t.Fatalf("Dir = %q", spec.Dir)
	}
	if strings.Join(spec.Args, " ") != "cursor-agent" {
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

func writeCursorCacheFixture(t *testing.T, path, title string) {
	t.Helper()
	writeFile(t, path, `{"role":"user","message":{"content":[{"type":"text","text":"`+title+`"}]}}`+"\n")
}
