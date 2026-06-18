package skillinstall

import "testing"

func TestParseGitHubSourceSupportsTreeURL(t *testing.T) {
	got, err := ParseGitHubSource("https://github.com/acme/tools/tree/main/skills/report", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Owner != "acme" || got.Repo != "tools" || got.TreePath != "main/skills/report" {
		t.Fatalf("source = %#v", got)
	}
}

func TestParseGitHubSourceSupportsTreeURLWithSlashRef(t *testing.T) {
	got, err := ParseGitHubSource("https://github.com/acme/tools/tree/feature/agent-report/skills/report", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Owner != "acme" || got.Repo != "tools" || got.TreePath != "feature/agent-report/skills/report" {
		t.Fatalf("source = %#v", got)
	}
}

func TestParseGitHubSourceUsesLastSkillPathMarkerForTreeURL(t *testing.T) {
	got, err := ParseGitHubSource("https://github.com/acme/tools/tree/feature/skills/refactor/skills/report", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Owner != "acme" || got.Repo != "tools" || got.TreePath != "feature/skills/refactor/skills/report" {
		t.Fatalf("source = %#v", got)
	}
}

func TestParseGitHubSourceTreatsTrailingSkillMarkerAsSlashRef(t *testing.T) {
	got, err := ParseGitHubSource("https://github.com/acme/tools/tree/feature/skills", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Owner != "acme" || got.Repo != "tools" || got.TreePath != "feature/skills" {
		t.Fatalf("source = %#v", got)
	}
}

func TestParseGitHubSourceAppliesOverrides(t *testing.T) {
	got, err := ParseGitHubSource("acme/tools", "v1", "skills/report")
	if err != nil {
		t.Fatal(err)
	}
	if got.Owner != "acme" || got.Repo != "tools" || got.Ref != "v1" || got.Path != "skills/report" {
		t.Fatalf("source = %#v", got)
	}
}
