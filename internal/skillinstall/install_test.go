package skillinstall

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFetcherDownloadsSkillsFromGitHubArchive(t *testing.T) {
	archive := testZip(t, map[string]string{
		"repo-main/skills/report/SKILL.md":              "---\nname: report\n---\n",
		"repo-main/skills/report/agents/openai.yaml":    "interface: {}\n",
		"repo-main/skills/other/SKILL.md":               "---\nname: other\n---\n",
		"repo-main/not-a-skill/SKILL.md":                "---\nname: ignored\n---\n",
		"repo-main/skills/report/scripts/run.sh":        "#!/bin/sh\n",
		"repo-main/skills/report/references/guide.md":   "guide\n",
		"repo-main/skills/report/assets/example.txt":    "asset\n",
		"repo-main/skills/report/.codex-plugin/skip.md": "kept\n",
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/acme/tools":
			_ = json.NewEncoder(w).Encode(map[string]string{"default_branch": "main"})
		case r.URL.Path == "/acme/tools/zip/main":
			_, _ = w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	skills, err := (Fetcher{
		APIBase:      server.URL,
		CodeloadBase: server.URL,
	}).Fetch(context.Background(), GitHubSource{Owner: "acme", Repo: "tools", Path: "skills/report"})
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 {
		t.Fatalf("len = %d, want 1", len(skills))
	}
	if skills[0].Name != "report" {
		t.Fatalf("name = %q", skills[0].Name)
	}
	if !hasSkillFile(skills[0], "agents/openai.yaml") || !hasSkillFile(skills[0], "scripts/run.sh") {
		t.Fatalf("files = %#v", skills[0].Files)
	}
}

func TestFetcherResolvesTreeURLToSkillsDirectory(t *testing.T) {
	archive := testZip(t, map[string]string{
		"repo-main/skills/report/SKILL.md": "---\nname: report\n---\n",
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/acme/tools/git/matching-refs/heads/main":
			_ = json.NewEncoder(w).Encode([]map[string]string{{"ref": "refs/heads/main"}})
		case "/repos/acme/tools/git/matching-refs/tags/main":
			_ = json.NewEncoder(w).Encode([]map[string]string{})
		case "/acme/tools/zip/main":
			_, _ = w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	source, err := ParseGitHubSource("https://github.com/acme/tools/tree/main/skills", "", "")
	if err != nil {
		t.Fatal(err)
	}
	skills, err := (Fetcher{
		APIBase:      server.URL,
		CodeloadBase: server.URL,
	}).Fetch(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 || skills[0].Name != "report" {
		t.Fatalf("skills = %#v", skills)
	}
}

func TestFetcherResolvesTreeURLToSlashContainingRef(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/acme/tools/git/matching-refs/heads/feature":
			_ = json.NewEncoder(w).Encode([]map[string]string{
				{"ref": "refs/heads/feature"},
				{"ref": "refs/heads/feature/skills"},
			})
		case "/repos/acme/tools/git/matching-refs/tags/feature":
			_ = json.NewEncoder(w).Encode([]map[string]string{})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	source, err := ParseGitHubSource("https://github.com/acme/tools/tree/feature/skills", "", "")
	if err != nil {
		t.Fatal(err)
	}
	got, err := (Fetcher{
		APIBase: server.URL,
	}).resolveTreeSource(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	if got.Ref != "feature/skills" || got.Path != "" {
		t.Fatalf("source = %#v", got)
	}
}

func TestFetcherDownloadsSkillsFromReleaseAsset(t *testing.T) {
	archive := testZip(t, map[string]string{
		"skills/report/SKILL.md":           "---\nname: report\n---\n",
		"skills/report/agents/openai.yaml": "interface: {}\n",
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/acme/tools/releases/latest":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"assets": []map[string]string{{
					"name":                 "asm_skills_v1.0.0.zip",
					"browser_download_url": serverURL(r) + "/download/asm_skills_v1.0.0.zip",
				}},
			})
		case "/download/asm_skills_v1.0.0.zip":
			_, _ = w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	skills, err := (Fetcher{
		APIBase: server.URL,
		Client:  server.Client(),
		Owner:   "acme",
		Repo:    "tools",
	}).FetchRelease(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 || skills[0].Name != "report" {
		t.Fatalf("skills = %#v", skills)
	}
}

func TestInstallWritesSelectedDestinations(t *testing.T) {
	current := t.TempDir()
	home := t.TempDir()
	results, err := Install([]Skill{{
		Name: "report",
		Files: []SkillFile{
			{Path: "SKILL.md", Data: []byte("---\nname: report\n---\n"), Mode: 0o644},
			{Path: "agents/openai.yaml", Data: []byte("interface: {}\n"), Mode: 0o644},
		},
	}}, InstallOptions{
		Scopes:     []string{ScopeCurrent, ScopeUser},
		Targets:    []string{TargetAgents, TargetClaude},
		CurrentDir: current,
		UserHome:   home,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 4 {
		t.Fatalf("results = %#v", results)
	}
	for _, path := range []string{
		filepath.Join(current, ".agents", "skills", "report", "SKILL.md"),
		filepath.Join(current, ".claude", "skills", "report", "SKILL.md"),
		filepath.Join(home, ".agents", "skills", "report", "SKILL.md"),
		filepath.Join(home, ".claude", "skills", "report", "SKILL.md"),
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "name: report") {
			t.Fatalf("%s = %s", path, string(data))
		}
	}
}

func TestInstallRejectsUnsafeSkillName(t *testing.T) {
	current := t.TempDir()
	outside := filepath.Join(current, "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(outside, "keep.txt")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Install([]Skill{{
		Name: "../../outside",
		Files: []SkillFile{
			{Path: "SKILL.md", Data: []byte("---\nname: ../../outside\n---\n"), Mode: 0o644},
		},
	}}, InstallOptions{
		Scopes:     []string{ScopeCurrent},
		Targets:    []string{TargetAgents},
		CurrentDir: current,
	})
	if err == nil {
		t.Fatal("expected unsafe skill name error")
	}
	data, readErr := os.ReadFile(sentinel)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) != "keep" {
		t.Fatalf("sentinel was modified: %q", data)
	}
}

func TestSkillsFromZipAssignsNestedSkillFilesToDeepestMatch(t *testing.T) {
	archive := testZip(t, map[string]string{
		"repo-main/skills/foo/SKILL.md":          "---\nname: foo\n---\n",
		"repo-main/skills/foo/root.txt":          "root\n",
		"repo-main/skills/foo/sub/SKILL.md":      "---\nname: sub\n---\n",
		"repo-main/skills/foo/sub/nested.txt":    "nested\n",
		"repo-main/skills/foo/sub/deep/info.txt": "deep\n",
	})

	skills, err := skillsFromZip(archive, "skills")
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 2 {
		t.Fatalf("skills = %#v", skills)
	}
	byName := map[string]Skill{}
	for _, skill := range skills {
		byName[skill.Name] = skill
	}
	if hasSkillFile(byName["foo"], "sub/nested.txt") {
		t.Fatalf("nested skill file assigned to parent: %#v", byName["foo"].Files)
	}
	if !hasSkillFile(byName["sub"], "nested.txt") || !hasSkillFile(byName["sub"], "deep/info.txt") {
		t.Fatalf("nested files missing from sub skill: %#v", byName["sub"].Files)
	}
}

func serverURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func testZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, data := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(data)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func hasSkillFile(skill Skill, path string) bool {
	for _, file := range skill.Files {
		if file.Path == path {
			return true
		}
	}
	return false
}
