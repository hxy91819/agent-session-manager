package kiro

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hxy91819/agent-session-manager/internal/session"
	"github.com/hxy91819/agent-session-manager/internal/sessioncache"
)

func TestMain(m *testing.M) {
	cacheDir, err := os.MkdirTemp("", "asm-kiro-cache-*")
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

func TestDiscoverReadsSessionMetadata(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	metadataPath, _ := writeKiroMetadata(t, home, "ses_one", repo, "Kiro title", "2026-06-13T01:00:00Z", "2026-06-13T01:10:00Z", "parent", "user")

	got, err := New(home).Discover(session.DiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	item := got[0]
	if item.ID != "ses_one" || item.Provider != Name || item.CWD != repo || item.Title != "Kiro title" {
		t.Fatalf("session = %#v", item)
	}
	if item.Path != metadataPath {
		t.Fatalf("Path = %q, want %q", item.Path, metadataPath)
	}
	if item.Metadata["title_source"] != "session" || item.Metadata["session_created_reason"] != "user" {
		t.Fatalf("metadata = %#v", item.Metadata)
	}
	if item.Metadata[session.MetadataParentThreadID] != "parent" || item.Metadata["agent_name"] != "kiro_default" {
		t.Fatalf("parent/agent metadata = %#v", item.Metadata)
	}
	if item.CreatedAt.Format(time.RFC3339) != "2026-06-13T01:00:00Z" {
		t.Fatalf("CreatedAt = %s", item.CreatedAt.Format(time.RFC3339))
	}
	if item.UpdatedAt.Format(time.RFC3339) != "2026-06-13T01:10:00Z" {
		t.Fatalf("UpdatedAt = %s", item.UpdatedAt.Format(time.RFC3339))
	}
}

func TestDiscoverFallsBackToPromptTitleAndReadsPreviews(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	_, transcriptPath := writeKiroMetadata(t, home, "ses_one", repo, "", "2026-06-13T01:00:00Z", "2026-06-13T01:10:00Z", "", "user")
	writeFile(t, transcriptPath, strings.Join([]string{
		kiroPrompt(1781312400, "# AGENTS.md instructions\nignore this injected context"),
		kiroPrompt(1781312460, "first prompt"),
		`{"kind":"AssistantMessage","version":"v1","data":{"content":[]}}`,
		kiroPrompt(1781312520, "second\nprompt"),
		kiroPrompt(1781312580, "third prompt"),
		kiroPrompt(1781312640, "fourth prompt"),
	}, "\n")+"\n")

	got, err := New(home).Discover(session.DiscoverOptions{
		Preview: session.PreviewOptions{UserMessagesPerEdge: 2, MaxChars: 500},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].Title != "first prompt" || got[0].Metadata["title_source"] != "prompt" {
		t.Fatalf("title/source = %q/%q", got[0].Title, got[0].Metadata["title_source"])
	}
	want := []string{"first prompt", "second prompt", "third prompt", "fourth prompt"}
	if texts := previewTexts(got[0].Previews); strings.Join(texts, "|") != strings.Join(want, "|") {
		t.Fatalf("previews = %#v, want %#v", texts, want)
	}
	if got[0].Previews[0].At.Format(time.RFC3339) != "2026-06-13T01:01:00Z" {
		t.Fatalf("preview time = %s", got[0].Previews[0].At.Format(time.RFC3339))
	}
}

func TestDiscoverFiltersAndLimitsNewestMetadataFiles(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	oldPath, _ := writeKiroMetadata(t, home, "ses_old", repo, "old", "2026-06-01T01:00:00Z", "2026-06-01T01:10:00Z", "", "user")
	newPath, _ := writeKiroMetadata(t, home, "ses_new", repo, "new", "2026-06-02T01:00:00Z", "2026-06-02T01:10:00Z", "", "user")
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
	if len(got) != 1 || got[0].ID != "ses_new" {
		t.Fatalf("sessions = %#v", got)
	}
}

func TestDiscoverRefreshesPromptFallbackAfterCacheHit(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	_, transcriptPath := writeKiroMetadata(t, home, "ses_one", repo, "", "2026-06-13T01:00:00Z", "2026-06-13T01:10:00Z", "", "user")
	provider := New(home)

	got, err := provider.Discover(session.DiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Title != "" {
		t.Fatalf("before transcript = %#v", got)
	}
	writeFile(t, transcriptPath, kiroPrompt(1781312400, "title from transcript")+"\n")

	got, err = provider.Discover(session.DiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Title != "title from transcript" {
		t.Fatalf("after transcript = %#v", got)
	}
}

func TestDiscoverMarksMissingCWD(t *testing.T) {
	home := t.TempDir()
	missing := filepath.Join(home, "missing")
	writeKiroMetadata(t, home, "ses_one", missing, "missing", "2026-06-13T01:00:00Z", "2026-06-13T01:10:00Z", "", "user")

	got, err := New(home).Discover(session.DiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Metadata["cwd_missing"] != "true" {
		t.Fatalf("sessions = %#v", got)
	}
}

func TestDiscoverCacheLifecycleAndBoundedScanPreservesHistory(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "repo-created-after-warm")
	writeKiroMetadata(t, home, "current", repo, "before primary change", "2026-06-13T01:00:00Z", "2026-06-13T01:10:00Z", "", "user")
	cachePath := filepath.Join(t.TempDir(), "cache.json")
	provider := Provider{Home: home, CachePath: cachePath}

	cold, err := provider.Discover(session.DiscoverOptions{})
	if err != nil || len(cold) != 1 || cold[0].Title != "before primary change" || cold[0].Metadata["cwd_missing"] != "true" {
		t.Fatalf("cold = %#v err=%v", cold, err)
	}
	warm, err := provider.Discover(session.DiscoverOptions{})
	if err != nil || !reflect.DeepEqual(cold, warm) {
		t.Fatalf("warm = %#v err=%v, want %#v", warm, err, cold)
	}
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	refreshed, err := provider.Discover(session.DiscoverOptions{})
	if err != nil || refreshed[0].Metadata["cwd_missing"] != "" {
		t.Fatalf("cwd refresh = %#v err=%v", refreshed, err)
	}
	writeKiroMetadata(t, home, "current", repo, "after primary file changed and grew", "2026-06-13T01:00:00Z", "2026-06-13T01:20:00Z", "", "user")
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

	oldPath, _ := writeKiroMetadata(t, home, "old", repo, "old title", "2026-01-01T01:00:00Z", "2026-01-01T01:10:00Z", "", "user")
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

func TestResumeCommandUsesKiroCLIFromSessionCWD(t *testing.T) {
	spec := New("").ResumeCommand(session.Session{ID: "ses_one", CWD: "/repo"})

	if spec.Dir != "/repo" {
		t.Fatalf("Dir = %q", spec.Dir)
	}
	if strings.Join(spec.Args, " ") != "kiro-cli chat --resume-id ses_one" {
		t.Fatalf("Args = %#v", spec.Args)
	}
}

func TestNewCommandUsesProjectCWD(t *testing.T) {
	spec := New("").NewCommand("/repo")

	if spec.Dir != "/repo" {
		t.Fatalf("Dir = %q", spec.Dir)
	}
	if strings.Join(spec.Args, " ") != "kiro-cli" {
		t.Fatalf("Args = %#v", spec.Args)
	}
}

func writeKiroMetadata(t *testing.T, home, id, cwd, title, createdAt, updatedAt, parentID, reason string) (string, string) {
	t.Helper()
	sessionsDir := filepath.Join(home, "sessions", "cli")
	metadataPath := filepath.Join(sessionsDir, id+".json")
	transcriptPath := filepath.Join(sessionsDir, id+".jsonl")
	metadata := `{"session_id":` + jsonString(id) +
		`,"cwd":` + jsonString(cwd) +
		`,"created_at":` + jsonString(createdAt) +
		`,"updated_at":` + jsonString(updatedAt) +
		`,"title":` + jsonString(title) +
		`,"session_created_reason":` + jsonString(reason) +
		`,"session_state":{"version":"1","agent_name":"kiro_default"}}`
	if parentID != "" {
		metadata = strings.TrimSuffix(metadata, "}") + `,"parent_session_id":` + jsonString(parentID) + `}`
	}
	writeFile(t, metadataPath, metadata+"\n")
	return metadataPath, transcriptPath
}

func kiroPrompt(timestamp int64, text string) string {
	return `{"kind":"Prompt","version":"v1","data":{"message_id":"msg","content":[{"kind":"text","data":` + jsonString(text) + `}],"meta":{"timestamp":` + jsonInt(timestamp) + `}}}`
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

func jsonString(value string) string {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(data)
}

func jsonInt(value int64) string {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(data)
}
