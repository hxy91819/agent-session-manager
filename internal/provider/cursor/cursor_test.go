package cursor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hxy91819/agent-session-manager/internal/session"
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
