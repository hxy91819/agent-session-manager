package pi

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
	cacheDir, err := os.MkdirTemp("", "asm-pi-cache-*")
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

func TestDiscoverReadsHeaderNameAndActivity(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	path := writePiSession(t, home, "ses-one", repo, []string{
		piHeader("ses-one", repo, "2026-06-13T01:00:00.000Z", "/other/parent.jsonl"),
		piUserMessage("a1", "2026-06-13T01:00:01.000Z", 1781312401000, "first task"),
		piAssistantMessage("a2", "2026-06-13T01:00:05.000Z", 1781312405000, "working on it"),
		piSessionInfo("a3", "2026-06-13T01:02:00.000Z", ""),
		piSessionInfo("a4", "2026-06-13T01:03:00.000Z", "named session"),
	})

	got, err := New(home).Discover(session.DiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	item := got[0]
	if item.ID != "ses-one" || item.Provider != Name || item.CWD != repo || item.Title != "named session" {
		t.Fatalf("session = %#v", item)
	}
	if item.Path != path {
		t.Fatalf("Path = %q, want %q", item.Path, path)
	}
	if item.Metadata["title_source"] != "session_info" || item.Metadata["parent_session"] != "/other/parent.jsonl" {
		t.Fatalf("metadata = %#v", item.Metadata)
	}
	if item.CreatedAt.Format(time.RFC3339) != "2026-06-13T01:00:00Z" {
		t.Fatalf("CreatedAt = %s", item.CreatedAt.Format(time.RFC3339))
	}
	// The last user/assistant activity timestamp wins over the header and mtime.
	if item.UpdatedAt.Format(time.RFC3339) != "2026-06-13T01:00:05Z" {
		t.Fatalf("UpdatedAt = %s", item.UpdatedAt.Format(time.RFC3339))
	}
}

func TestDiscoverEmptySessionInfoClearsNameAndFallsBackToFirstUserMessage(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	writePiSession(t, home, "ses-one", repo, []string{
		piHeader("ses-one", repo, "2026-06-13T01:00:00.000Z", ""),
		piSessionInfo("a1", "2026-06-13T01:01:00.000Z", "temporary name"),
		piUserMessage("a2", "2026-06-13T01:02:00.000Z", 1781312520000, "real first task"),
		piSessionInfo("a3", "2026-06-13T01:03:00.000Z", ""),
	})

	got, err := New(home).Discover(session.DiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Title != "real first task" || got[0].Metadata["title_source"] != "message" {
		t.Fatalf("session = %#v", got)
	}
}

func TestDiscoverSkipsInjectedContextBeforeChoosingTitle(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	writePiSession(t, home, "ses-one", repo, []string{
		piHeader("ses-one", repo, "2026-06-13T01:00:00.000Z", ""),
		piUserMessage("a1", "2026-06-13T01:00:01.000Z", 1781312401000, "<system-reminder>\ninjected context"),
		piUserMessage("a2", "2026-06-13T01:00:02.000Z", 1781312402000, "second\nreal task"),
	})

	got, err := New(home).Discover(session.DiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Title != "second real task" {
		t.Fatalf("session = %#v", got)
	}
}

func TestDiscoverReadsPreviewsWithTimestamps(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	writePiSession(t, home, "ses-one", repo, []string{
		piHeader("ses-one", repo, "2026-06-13T01:00:00.000Z", ""),
		piUserMessage("a1", "2026-06-13T01:01:00.000Z", 1781312460000, "first prompt"),
		piAssistantMessage("a2", "2026-06-13T01:01:30.000Z", 1781312490000, "assistant reply"),
		piUserMessageNoTimestamp("a3", "2026-06-13T01:02:00.000Z", "second prompt"),
		piToolResult("a4", "2026-06-13T01:02:30.000Z", "tool output"),
		piUserMessage("a5", "2026-06-13T01:03:00.000Z", 1781312580000, "third prompt"),
	})

	got, err := New(home).Discover(session.DiscoverOptions{
		Preview: session.PreviewOptions{UserMessagesPerEdge: 2, MaxChars: 500},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	want := []string{"first prompt", "second prompt", "third prompt"}
	if texts := previewTexts(got[0].Previews); strings.Join(texts, "|") != strings.Join(want, "|") {
		t.Fatalf("previews = %#v, want %#v", texts, want)
	}
	// Numeric message timestamps win; a missing one falls back to the record ts.
	if got[0].Previews[0].At.Format(time.RFC3339) != "2026-06-13T01:01:00Z" {
		t.Fatalf("preview[0] at = %s", got[0].Previews[0].At.Format(time.RFC3339))
	}
	if got[0].Previews[1].At.Format(time.RFC3339) != "2026-06-13T01:02:00Z" {
		t.Fatalf("preview[1] at = %s", got[0].Previews[1].At.Format(time.RFC3339))
	}
	// A user message with neither timestamp is excluded and flagged partial.
	if got[0].Previews[2].At.Format(time.RFC3339) != "2026-06-13T01:03:00Z" {
		t.Fatalf("preview[2] at = %s", got[0].Previews[2].At.Format(time.RFC3339))
	}
	if got[0].Metadata[session.MetadataReportEvidenceStatus] != "" {
		t.Fatalf("unexpected partial evidence: %#v", got[0].Metadata)
	}
}

func TestDiscoverPreviewsWithoutAnyTimestampAreFlagged(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	writePiSession(t, home, "ses-one", repo, []string{
		piHeader("ses-one", repo, "2026-06-13T01:00:00.000Z", ""),
		piUserMessageFull("a1", "", json.RawMessage("null"), "timeless prompt"),
	})

	got, err := New(home).Discover(session.DiscoverOptions{
		Preview: session.PreviewOptions{UserMessagesPerEdge: 1, MaxChars: 500},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if len(got[0].Previews) != 0 {
		t.Fatalf("previews = %#v, want none", got[0].Previews)
	}
	if got[0].Metadata[session.MetadataReportEvidenceStatus] != session.ReportEvidencePartial {
		t.Fatalf("metadata = %#v", got[0].Metadata)
	}
}

func TestDiscoverPreviewsRespectReportWindow(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	writePiSession(t, home, "ses-one", repo, []string{
		piHeader("ses-one", repo, "2026-06-13T01:00:00.000Z", ""),
		piUserMessageNoTimestamp("a1", "2026-06-12T23:59:59.000Z", "outside window"),
		piUserMessage("a2", "2026-06-13T01:05:00.000Z", 1781312460000, "inside window"),
	})

	got, err := New(home).Discover(session.DiscoverOptions{
		Preview: session.PreviewOptions{
			UserMessagesPerEdge: 2,
			MaxChars:            500,
			Since:               time.Date(2026, 6, 13, 0, 0, 0, 0, time.UTC),
			Before:              time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(got[0].Previews) != 1 || got[0].Previews[0].Text != "inside window" {
		t.Fatalf("previews = %#v", got)
	}
}

func TestDiscoverFiltersAndLimitsNewestFiles(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	oldPath := writePiSession(t, home, "ses-old", repo, []string{
		piHeader("ses-old", repo, "2026-06-01T01:00:00.000Z", ""),
	})
	newPath := writePiSession(t, home, "ses-new", repo, []string{
		piHeader("ses-new", repo, "2026-06-02T01:00:00.000Z", ""),
	})
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
	if len(got) != 1 || got[0].ID != "ses-new" {
		t.Fatalf("sessions = %#v", got)
	}
}

func TestDiscoverSkipsFilesWithoutSessionHeader(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	dir := filepath.Join(home, "sessions", "--bogus--")
	writeFile(t, filepath.Join(dir, "not-a-session.jsonl"), piUserMessage("a1", "2026-06-13T01:01:00.000Z", 1781312460000, "orphan message")+"\n")
	writeFile(t, filepath.Join(dir, "broken.jsonl"), "not-json\n")
	writePiSession(t, home, "ses-valid", repo, []string{
		piHeader("ses-valid", repo, "2026-06-13T01:00:00.000Z", ""),
	})

	got, err := New(home).Discover(session.DiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "ses-valid" {
		t.Fatalf("sessions = %#v", got)
	}
}

func TestDiscoverMarksMissingCWD(t *testing.T) {
	home := t.TempDir()
	missing := filepath.Join(home, "missing")
	writePiSession(t, home, "ses-one", missing, []string{
		piHeader("ses-one", missing, "2026-06-13T01:00:00.000Z", ""),
	})

	got, err := New(home).Discover(session.DiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Metadata["cwd_missing"] != "true" {
		t.Fatalf("sessions = %#v", got)
	}
}

func TestDiscoverCacheLifecycleAndDynamicInputs(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "repo-created-after-warm")
	path := writePiSession(t, home, "ses-one", repo, []string{
		piHeader("ses-one", repo, "2026-06-13T01:00:00.000Z", ""),
		piUserMessage("a1", "2026-06-13T01:00:01.000Z", 1781312401000, "before append"),
	})
	cachePath := filepath.Join(t.TempDir(), "cache.json")
	provider := Provider{Home: home, CachePath: cachePath}

	cold, err := provider.Discover(session.DiscoverOptions{})
	if err != nil || len(cold) != 1 || cold[0].Title != "before append" || cold[0].Metadata["cwd_missing"] != "true" {
		t.Fatalf("cold = %#v err=%v", cold, err)
	}
	warm, err := provider.Discover(session.DiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	sessiontest.RequireEqual(t, cold, warm)

	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	refreshed, err := provider.Discover(session.DiscoverOptions{})
	if err != nil || refreshed[0].Metadata["cwd_missing"] != "" {
		t.Fatalf("cwd refresh = %#v err=%v", refreshed, err)
	}

	// Append-only growth invalidates the entry through size and mtime.
	appendFile(t, path, piSessionInfo("a2", "2026-06-13T01:10:00.000Z", "after append"))
	invalidated, err := provider.Discover(session.DiscoverOptions{})
	if err != nil || invalidated[0].Title != "after append" {
		t.Fatalf("invalidated = %#v err=%v", invalidated, err)
	}

	if err := os.WriteFile(cachePath, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	recovered, err := provider.Discover(session.DiscoverOptions{})
	if err != nil || len(recovered) != 1 || recovered[0].Title != "after append" {
		t.Fatalf("corrupt-cache recovery = %#v err=%v", recovered, err)
	}

	oldPath := writePiSession(t, home, "ses-old", repo, []string{
		piHeader("ses-old", repo, "2026-01-01T01:00:00.000Z", ""),
	})
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

func TestDiscoverCacheHitDoesNotCachePreviewsAcrossRuns(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	writePiSession(t, home, "ses-one", repo, []string{
		piHeader("ses-one", repo, "2026-06-13T01:00:00.000Z", ""),
		piUserMessage("a1", "2026-06-13T01:00:01.000Z", 1781312401000, "preview prompt"),
	})
	provider := New(home)

	plain, err := provider.Discover(session.DiscoverOptions{})
	if err != nil || len(plain) != 1 || plain[0].Previews != nil {
		t.Fatalf("plain = %#v err=%v", plain, err)
	}
	withPreviews, err := provider.Discover(session.DiscoverOptions{
		Preview: session.PreviewOptions{UserMessagesPerEdge: 1, MaxChars: 500},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(withPreviews) != 1 || len(withPreviews[0].Previews) != 1 || withPreviews[0].Previews[0].Text != "preview prompt" {
		t.Fatalf("previews after cache hit = %#v", withPreviews)
	}
	plainAgain, err := provider.Discover(session.DiscoverOptions{})
	if err != nil || len(plainAgain) != 1 || plainAgain[0].Previews != nil {
		t.Fatalf("plain after previews = %#v err=%v", plainAgain, err)
	}
}

func TestResumeCommandUsesPiFromSessionCWD(t *testing.T) {
	spec := New("").ResumeCommand(session.Session{ID: "019f4ece-93dd-7e48-b0c4-e2a1c1ada640", CWD: "/repo"})

	if spec.Dir != "/repo" {
		t.Fatalf("Dir = %q", spec.Dir)
	}
	if strings.Join(spec.Args, " ") != "pi --session 019f4ece-93dd-7e48-b0c4-e2a1c1ada640" {
		t.Fatalf("Args = %#v", spec.Args)
	}
}

func TestNewCommandUsesProjectCWD(t *testing.T) {
	spec := New("").NewCommand("/repo")

	if spec.Dir != "/repo" {
		t.Fatalf("Dir = %q", spec.Dir)
	}
	if strings.Join(spec.Args, " ") != "pi" {
		t.Fatalf("Args = %#v", spec.Args)
	}
}

func TestOversizedPiUserEvidenceDetection(t *testing.T) {
	cases := []struct {
		prefix string
		want   bool
	}{
		{`{"type":"message","id":"x","timestamp":"t","message":{"role":"user","content":[{"type":"text","text":"huge"}]}}`, true},
		{`{"type":"message","id":"x","timestamp":"t","message":{"role":"assistant","content":[{"type":"thinking"}]}}`, false},
		{`{"type":"message","id":"x","timestamp":"t","message":{"role":"toolResult","content":[]}}`, false},
		{`{"type":"session_info","id":"x","name":"n"}`, false},
		{`{"type":"unknown_future_record"}`, true},
	}
	for _, tc := range cases {
		if got := oversizedPiCouldContainUserEvidence([]byte(tc.prefix)); got != tc.want {
			t.Errorf("oversizedPiCouldContainUserEvidence(%q) = %v, want %v", tc.prefix, got, tc.want)
		}
	}
}

func writePiSession(t *testing.T, home, id, cwd string, lines []string) string {
	t.Helper()
	dir := filepath.Join(home, "sessions", "--"+strings.Trim(strings.ReplaceAll(id, "_", "-"), "-")+"--")
	path := filepath.Join(dir, "2026-06-13T01-00-00-000Z_"+id+".jsonl")
	writeFile(t, path, strings.Join(lines, "\n")+"\n")
	return path
}

func piHeader(id, cwd, timestamp, parent string) string {
	line := `{"type":"session","version":3,"id":` + jsonString(id) + `,"timestamp":` + jsonString(timestamp) + `,"cwd":` + jsonString(cwd) + `}`
	if parent != "" {
		line = strings.TrimSuffix(line, "}") + `,"parentSession":` + jsonString(parent) + `}`
	}
	return line
}

func piSessionInfo(id, timestamp, name string) string {
	return `{"type":"session_info","id":` + jsonString(id) + `,"parentId":null,"timestamp":` + jsonString(timestamp) + `,"name":` + jsonString(name) + `}`
}

func piUserMessage(id, timestamp string, messageTS int64, text string) string {
	return piUserMessageFull(id, timestamp, jsonInt(messageTS), text)
}

func piUserMessageNoTimestamp(id, timestamp, text string) string {
	return piUserMessageFull(id, timestamp, nil, text)
}

// piUserMessageFull builds a message record; an empty record timestamp becomes
// JSON null and a nil messageTS omits the per-message timestamp, matching the
// shapes Pi leaves behind when exact activity times are unavailable.
func piUserMessageFull(id, timestamp string, messageTS json.RawMessage, text string) string {
	top := "null"
	if timestamp != "" {
		top = jsonString(timestamp)
	}
	msg := `"message":{"role":"user","content":[{"type":"text","text":` + jsonString(text) + `}]`
	if messageTS != nil {
		msg += `,"timestamp":` + string(messageTS)
	}
	return `{"type":"message","id":` + jsonString(id) + `,"parentId":null,"timestamp":` + top + `,` + msg + `}}`
}

func piAssistantMessage(id, timestamp string, messageTS int64, text string) string {
	return `{"type":"message","id":` + jsonString(id) + `,"parentId":null,"timestamp":` + jsonString(timestamp) + `,"message":{"role":"assistant","content":[{"type":"text","text":` + jsonString(text) + `}],"timestamp":` + string(jsonInt(messageTS)) + `}}`
}

func piToolResult(id, timestamp, text string) string {
	return `{"type":"message","id":` + jsonString(id) + `,"parentId":null,"timestamp":` + jsonString(timestamp) + `,"message":{"role":"toolResult","content":[{"type":"text","text":` + jsonString(text) + `}]}}`
}

func previewTexts(previews []session.MessagePreview) []string {
	texts := make([]string, 0, len(previews))
	for _, preview := range previews {
		texts = append(texts, preview.Text)
	}
	return texts
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

func appendFile(t *testing.T, path, content string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(content + "\n"); err != nil {
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

func jsonInt(value int64) json.RawMessage {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}
