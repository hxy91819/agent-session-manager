package dsh

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/hxy91819/agent-session-manager/internal/session"
	"github.com/hxy91819/agent-session-manager/internal/sessiontest"
)

func TestMain(m *testing.M) {
	cacheDir, err := os.MkdirTemp("", "asm-dsh-cache-*")
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

func TestDiscoverReadsZstdSession(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	writeZstdLog(t, home, "--data-code-asm--", "session-one", []string{
		header("session-one", repo, 1781312400000),
		`{"type":"user/message","seq":0,"time":1781312401000,"data":{"id":"m1","role":"user","content":[{"type":"text","text":"fix the flaky dsh test"}],"source":{"kind":"user"}}}`,
		`{"type":"session/title","seq":1,"time":1781312402000,"data":{"title":"first generated title","messageSeqs":[0],"source":{"kind":"fallback"}}}`,
		`{"type":"session/title","seq":2,"time":1781312403000,"data":{"title":"renamed terminal title","messageSeqs":[],"source":{"kind":"user"}}}`,
	})

	got, err := New(home).Discover(session.DiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	item := got[0]
	if item.ID != "session-one" || item.Provider != Name || item.CWD != repo {
		t.Fatalf("session = %#v", item)
	}
	if item.Title != "renamed terminal title" {
		t.Fatalf("title = %q, want latest title event", item.Title)
	}
	if item.Metadata["title_source"] != "session_title" || item.Metadata["agent_preset"] != "standard" {
		t.Fatalf("metadata = %#v", item.Metadata)
	}
	if item.CreatedAt.Format(time.RFC3339) != "2026-06-13T01:00:00Z" {
		t.Fatalf("CreatedAt = %s", item.CreatedAt.Format(time.RFC3339))
	}
	if item.UpdatedAt.Format(time.RFC3339) != "2026-06-13T01:00:03Z" {
		t.Fatalf("UpdatedAt = %s, want last event time", item.UpdatedAt.Format(time.RFC3339))
	}
}

func TestDiscoverFallsBackToFirstHumanPrompt(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	writeZstdLog(t, home, "--data-code-asm--", "session-two", []string{
		header("session-two", repo, 1781312400000),
		`{"type":"user/message","seq":0,"time":1781312401000,"data":{"id":"m1","role":"user","content":[{"type":"text","text":"injected skill context"}],"source":{"kind":"plugin","plugin":"skills"}}}`,
		`{"type":"user/message","seq":1,"time":1781312402000,"data":{"id":"m2","role":"user","content":[{"type":"text","text":"real first prompt"},{"type":"image"}],"source":{"kind":"user"}}}`,
	})

	got, err := New(home).Discover(session.DiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Title != "real first prompt" {
		t.Fatalf("sessions = %#v", got)
	}
	if got[0].Metadata["title_source"] != "first_input" {
		t.Fatalf("metadata = %#v", got[0].Metadata)
	}
}

func TestDiscoverReadsPlainTextLogAndParentMetadata(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	writePlainLog(t, home, "--data-code-asm--", "session-child", []string{
		`{"type":"session","version":0,"id":"session-child","createdAt":1781312400000,"cwd":` + jsonString(repo) + `,"parentSession":"session-parent","origin":"subagent","delegationDepth":1}`,
		`{"type":"user/message","seq":0,"time":1781312401000,"data":{"id":"m1","role":"user","content":[{"type":"text","text":"child task title"}],"source":{"kind":"user"}}}`,
	})

	got, err := New(home).Discover(session.DiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Title != "child task title" {
		t.Fatalf("sessions = %#v", got)
	}
	meta := got[0].Metadata
	if meta["origin"] != "subagent" || meta["delegation_depth"] != "1" || meta[session.MetadataParentThreadID] != "session-parent" {
		t.Fatalf("metadata = %#v", meta)
	}
}

func TestDiscoverSkipsUnreadableLogs(t *testing.T) {
	home := t.TempDir()
	// Foreign format version: dsh refuses newer logs on load, and asm keeps
	// them undiscovered instead of misreading a format it does not know.
	writeZstdLog(t, home, "--proj--", "session-foreign", []string{
		`{"type":"session","version":1,"id":"session-foreign","createdAt":1781312400000,"delegationDepth":0}`,
	})
	// First record is not a session header.
	writeZstdLog(t, home, "--proj--", "session-noheader", []string{
		`{"type":"user/message","seq":0,"time":1781312401000,"data":{"source":{"kind":"user"}}}`,
	})
	// Half-written empty artifact.
	dir := filepath.Join(home, "sessions", "--proj--", "session-empty")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, zstdLogName), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// A legacy flat file at the project level must not break discovery.
	if err := os.WriteFile(filepath.Join(home, "sessions", "--proj--", "stray.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := New(home).Discover(session.DiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("sessions = %#v, want none", got)
	}
}

func TestDiscoverToleratesTornTail(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	path := writeZstdLog(t, home, "--proj--", "session-torn", []string{
		header("session-torn", repo, 1781312400000),
		`{"type":"user/message","seq":0,"time":1781312401000,"data":{"id":"m1","role":"user","content":[{"type":"text","text":"survives torn tail"}],"source":{"kind":"user"}}}`,
	})
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte{0x28, 0xb5, 0x2f, 0xfd, 0x01, 0x02}); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	got, err := New(home).Discover(session.DiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Title != "survives torn tail" {
		t.Fatalf("sessions = %#v", got)
	}
}

func TestDiscoverCacheLifecycleAndBounds(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "repo-created-after-warm")
	path := writeZstdLog(t, home, "--proj--", "session-cache", []string{
		header("session-cache", repo, 1781312400000),
		`{"type":"user/message","seq":0,"time":1781312401000,"data":{"id":"m1","role":"user","content":[{"type":"text","text":"before primary change"}],"source":{"kind":"user"}}}`,
	})
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
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	refreshed, err := provider.Discover(session.DiscoverOptions{})
	if err != nil || refreshed[0].Metadata["cwd_missing"] != "" {
		t.Fatalf("cwd refresh = %#v err=%v", refreshed, err)
	}

	// An append keeps identity fresh through path+size+mtime, so a grown log
	// invalidates the cached parse.
	writeZstdLog(t, home, "--proj--", "session-cache", []string{
		header("session-cache", repo, 1781312400000),
		`{"type":"user/message","seq":0,"time":1781312401000,"data":{"id":"m1","role":"user","content":[{"type":"text","text":"before primary change"}],"source":{"kind":"user"}}}`,
		`{"type":"session/title","seq":1,"time":1781312409000,"data":{"title":"after append","messageSeqs":[0],"source":{"kind":"provider"}}}`,
		`{"type":"turn/end","seq":2,"time":1781312410000,"data":{"turn":0,"reason":{"kind":"completed"}}}`,
	})
	invalidated, err := provider.Discover(session.DiscoverOptions{})
	if err != nil || invalidated[0].Title != "after append" || invalidated[0].UpdatedAt.Format(time.RFC3339) != "2026-06-13T01:00:10Z" {
		t.Fatalf("invalidated = %#v err=%v", invalidated, err)
	}

	if err := os.WriteFile(cachePath, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	recovered, err := provider.Discover(session.DiscoverOptions{})
	if err != nil || len(recovered) != 1 || recovered[0].Title != "after append" {
		t.Fatalf("corrupt-cache recovery = %#v err=%v", recovered, err)
	}

	// Old logs fall out of --since windows; the newest-first order keeps
	// --limit effective.
	if err := os.Chtimes(path, time.Now().AddDate(0, 0, -60), time.Now().AddDate(0, 0, -60)); err != nil {
		t.Fatal(err)
	}
	old, err := provider.Discover(session.DiscoverOptions{Since: time.Now().AddDate(0, 0, -30)})
	if err != nil || len(old) != 0 {
		t.Fatalf("since filter = %#v err=%v", old, err)
	}
	all, err := provider.Discover(session.DiscoverOptions{})
	if err != nil || len(all) != 1 {
		t.Fatalf("rescan after window = %#v err=%v", all, err)
	}
}

func TestDiscoverAppliesLimitNewestFirst(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	times := []string{"session-old", "session-new"}
	writeZstdLog(t, home, "--proj--", "session-old", []string{
		header("session-old", repo, 1781312400000),
	})
	writeZstdLog(t, home, "--proj--", "session-new", []string{
		header("session-new", repo, 1781312500000),
	})
	oldPath := filepath.Join(home, "sessions", "--proj--", "session-old", zstdLogName)
	oldTime := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(oldPath, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	got, err := New(home).Discover(session.DiscoverOptions{LimitFiles: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != times[1] {
		t.Fatalf("sessions = %#v, want only %s", got, times[1])
	}
}

func TestDiscoverCollectsReportPreviews(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	writeZstdLog(t, home, "--proj--", "session-evidence", []string{
		header("session-evidence", repo, 1781312400000),
		`{"type":"user/message","seq":0,"time":1781312401000,"data":{"id":"m1","role":"user","content":[{"type":"text","text":"first real prompt"}],"source":{"kind":"user"}}}`,
		`{"type":"assistant/message","seq":1,"time":1781312402000,"data":{"message":{"id":"a1"}}}`,
		`{"type":"user/message","seq":2,"time":1781312500000,"data":{"id":"m2","role":"user","content":[{"type":"text","text":"later real prompt"}],"source":{"kind":"user"}}}`,
		`{"type":"user/message","seq":3,"time":1781312600000,"data":{"id":"m3","role":"user","content":[{"type":"text","text":"plugin notice"}],"source":{"kind":"plugin","plugin":"cron"}}}`,
	})

	got, err := New(home).Discover(session.DiscoverOptions{
		Preview: session.PreviewOptions{UserMessagesPerEdge: 1, MaxChars: 100},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("sessions = %#v", got)
	}
	previews := got[0].Previews
	if len(previews) != 2 || previews[0].Text != "first real prompt" || previews[1].Text != "later real prompt" {
		t.Fatalf("previews = %#v", previews)
	}
}

func TestResumeCommandUsesDocumentedInvocation(t *testing.T) {
	provider := New("")
	spec := provider.ResumeCommand(session.Session{ID: "session-1", CWD: "/repo"})
	want := "dsh --profile tui --resume session-1"
	if strings.Join(spec.Args, " ") != want || spec.Dir != "/repo" {
		t.Fatalf("spec = %#v", spec)
	}
	if newSpec := provider.NewCommand("/repo"); newSpec.UnsupportedReason == "" {
		t.Fatalf("new session spec = %#v, want unsupported", newSpec)
	}
}

func header(id, cwd string, createdAt int64) string {
	return `{"type":"session","version":0,"id":` + jsonString(id) + `,"createdAt":` + strconv.FormatInt(createdAt, 10) + `,"cwd":` + jsonString(cwd) + `,"delegationDepth":0,"agentPreset":"standard"}`
}

func jsonString(value string) string {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(data)
}

func writeZstdLog(t testing.TB, home, project, id string, lines []string) string {
	t.Helper()
	content := strings.Join(lines, "\n") + "\n"
	data := encodeZstdFrames(t, content)
	dir := filepath.Join(home, "sessions", project, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, zstdLogName)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func writePlainLog(t testing.TB, home, project, id string, lines []string) string {
	t.Helper()
	content := strings.Join(lines, "\n") + "\n"
	dir := filepath.Join(home, "sessions", project, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, plainLogName)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// encodeZstdFrames writes content as one frame per line to mirror dsh's
// header-frame-plus-event-frames physical layout (frames decode concatenated).
func encodeZstdFrames(t testing.TB, content string) []byte {
	t.Helper()
	var out bytes.Buffer
	for _, line := range strings.SplitAfter(content, "\n") {
		if line == "" {
			continue
		}
		writer, err := zstd.NewWriter(&out)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte(line)); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return out.Bytes()
}
