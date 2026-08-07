package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type contractReport struct {
	SchemaVersion string `json:"schema_version"`
	Base          struct {
		Revision   string `json:"revision"`
		Production struct {
			CloneGroups int `json:"clone_group_count"`
		} `json:"production"`
	} `json:"base"`
	Head struct {
		Revision   string `json:"revision"`
		Production struct {
			Files       int `json:"file_count"`
			Functions   int `json:"function_count"`
			CloneGroups int `json:"clone_group_count"`
		} `json:"production"`
		Tests struct {
			Files     int `json:"file_count"`
			Functions int `json:"function_count"`
		} `json:"tests"`
	} `json:"head"`
	Comparison struct {
		Violations []struct {
			Scope  string `json:"scope"`
			Metric string `json:"metric"`
		} `json:"violations"`
		NewCloneGroups int `json:"new_clone_groups"`
	} `json:"comparison"`
	Verdict struct {
		Status    string `json:"status"`
		WouldFail bool   `json:"would_fail"`
		Enforced  bool   `json:"enforced"`
	} `json:"verdict"`
}

var contractToolPath string

func TestMain(m *testing.M) {
	tempDir, err := os.MkdirTemp("", "asm-code-health-test-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	contractToolPath = filepath.Join(tempDir, "check-code-health")
	cmd := exec.Command("go", "build", "-o", contractToolPath, ".")
	if output, buildErr := cmd.CombinedOutput(); buildErr != nil {
		fmt.Fprintf(os.Stderr, "build check-code-health: %v\n%s", buildErr, output)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

func TestDiffAwareFunctionAndFileContracts(t *testing.T) {
	tests := []struct {
		name       string
		base       string
		head       string
		wantCode   int
		wantMetric string
	}{
		{
			name:       "new function over hard line threshold",
			base:       "package sample\n",
			head:       longFunction("added", 82),
			wantCode:   1,
			wantMetric: "function_lines",
		},
		{
			name:     "existing over-limit function decreases",
			base:     longFunction("existing", 90),
			head:     longFunction("existing", 89),
			wantCode: 0,
		},
		{
			name:       "existing over-limit function increases",
			base:       longFunction("existing", 90),
			head:       longFunction("existing", 91),
			wantCode:   1,
			wantMetric: "function_lines",
		},
		{
			name:     "function below hard threshold may grow",
			base:     longFunction("existing", 10),
			head:     longFunction("existing", 20),
			wantCode: 0,
		},
		{
			name:     "existing oversized file decreases",
			base:     longFile(610),
			head:     longFile(609),
			wantCode: 0,
		},
		{
			name:       "existing oversized file increases",
			base:       longFile(610),
			head:       longFile(611),
			wantCode:   1,
			wantMetric: "file_lines",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newFixtureRepo(t, map[string]string{"internal/sample/sample.go": tt.base})
			base := repo.commit(t, "base")
			repo.write(t, "internal/sample/sample.go", tt.head)

			code, stdout, stderr := runTool(t, repo.dir, "--base", base, "--format", "json", "--enforce")
			if code != tt.wantCode {
				t.Fatalf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s", code, tt.wantCode, stdout, stderr)
			}
			report := decodeContractReport(t, stdout)
			if report.Verdict.WouldFail != (tt.wantCode == 1) {
				t.Fatalf("would_fail = %v, exit code = %d", report.Verdict.WouldFail, tt.wantCode)
			}
			if tt.wantMetric != "" && !hasViolation(report, tt.wantMetric) {
				t.Fatalf("violations = %#v, want metric %q", report.Comparison.Violations, tt.wantMetric)
			}
		})
	}
}

func TestFunctionBodyChangeIsComparedWhenDeclarationLineIsUnchanged(t *testing.T) {
	repo := newFixtureRepo(t, map[string]string{"internal/sample/sample.go": longFunction("changed", 79)})
	base := repo.commit(t, "base")
	repo.write(t, "internal/sample/sample.go", longFunction("changed", 82))

	code, stdout, stderr := runTool(t, repo.dir, "--base", base, "--format", "json", "--enforce")
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !hasViolation(decodeContractReport(t, stdout), "function_lines") {
		t.Fatalf("expected function_lines violation, stdout:\n%s", stdout)
	}
}

func TestCloneFingerprintIgnoresLineMovement(t *testing.T) {
	baseSource := cloneSource(0)
	repo := newFixtureRepo(t, map[string]string{"internal/sample/sample.go": baseSource})
	base := repo.commit(t, "base")
	repo.write(t, "internal/sample/sample.go", cloneSource(15))

	code, stdout, stderr := runTool(t, repo.dir, "--base", base, "--format", "json", "--enforce")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	report := decodeContractReport(t, stdout)
	if report.Base.Production.CloneGroups == 0 || report.Head.Production.CloneGroups == 0 {
		t.Fatalf("fixture did not produce clone groups: base=%d head=%d",
			report.Base.Production.CloneGroups, report.Head.Production.CloneGroups)
	}
	if report.Comparison.NewCloneGroups != 0 {
		t.Fatalf("new clone groups = %d, want 0", report.Comparison.NewCloneGroups)
	}
}

func TestNewCloneGroupIsDetected(t *testing.T) {
	repo := newFixtureRepo(t, map[string]string{"internal/sample/sample.go": "package sample\n"})
	base := repo.commit(t, "base")
	repo.write(t, "internal/sample/sample.go", cloneSource(0))

	code, stdout, stderr := runTool(t, repo.dir, "--base", base, "--format", "json", "--enforce")
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	report := decodeContractReport(t, stdout)
	if report.Comparison.NewCloneGroups == 0 || !hasViolation(report, "clone_tokens") {
		t.Fatalf("new clone contract missing: groups=%d violations=%+v",
			report.Comparison.NewCloneGroups, report.Comparison.Violations)
	}
}

func TestTestFilesAreReportedButNeverEnforced(t *testing.T) {
	repo := newFixtureRepo(t, map[string]string{"internal/sample/sample.go": "package sample\n"})
	base := repo.commit(t, "base")
	repo.write(t, "internal/sample/sample_test.go", longFunction("largeTest", 100))

	code, stdout, stderr := runTool(t, repo.dir, "--base", base, "--format", "json", "--enforce")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	report := decodeContractReport(t, stdout)
	if report.Head.Tests.Files != 1 || report.Head.Tests.Functions != 1 {
		t.Fatalf("test stats = %+v, want one file and function", report.Head.Tests)
	}
	for _, violation := range report.Comparison.Violations {
		if violation.Scope == "test" {
			t.Fatalf("test violation was enforced: %+v", violation)
		}
	}
}

func TestOperationalErrorsReturnExitTwo(t *testing.T) {
	t.Run("malformed source", func(t *testing.T) {
		repo := newFixtureRepo(t, map[string]string{"internal/sample/sample.go": "package sample\nfunc broken( {\n"})
		base := repo.commit(t, "base")
		code, _, stderr := runTool(t, repo.dir, "--base", base, "--format", "json")
		if code != 2 || !strings.Contains(stderr, "parse") {
			t.Fatalf("exit=%d stderr=%q, want parse error and exit 2", code, stderr)
		}
	})

	t.Run("invalid base revision", func(t *testing.T) {
		repo := newFixtureRepo(t, map[string]string{"internal/sample/sample.go": "package sample\n"})
		repo.commit(t, "base")
		code, _, stderr := runTool(t, repo.dir, "--base", "does-not-exist", "--format", "json")
		if code != 2 || !strings.Contains(stderr, "base revision") {
			t.Fatalf("exit=%d stderr=%q, want base revision error and exit 2", code, stderr)
		}
	})

	t.Run("analyzer failure", func(t *testing.T) {
		repo := newFixtureRepo(t, map[string]string{"internal/sample/sample.go": "package sample\n"})
		base := repo.commit(t, "base")
		link := filepath.Join(repo.dir, "internal", "sample", "linked.go")
		if err := os.Symlink("sample.go", link); err != nil {
			t.Fatal(err)
		}
		code, _, stderr := runTool(t, repo.dir, "--base", base, "--format", "json")
		if code != 2 || !strings.Contains(stderr, "analyzer failure") {
			t.Fatalf("exit=%d stderr=%q, want analyzer failure and exit 2", code, stderr)
		}
	})
}

func TestTextAndJSONExposeTheSameVerdict(t *testing.T) {
	repo := newFixtureRepo(t, map[string]string{"internal/sample/sample.go": "package sample\n"})
	base := repo.commit(t, "base")
	repo.write(t, "internal/sample/sample.go", longFunction("added", 82))

	jsonCode, jsonOut, jsonErr := runTool(t, repo.dir, "--base", base, "--format", "json")
	textCode, textOut, textErr := runTool(t, repo.dir, "--base", base, "--format", "text")
	if jsonCode != 0 || textCode != 0 {
		t.Fatalf("report-only exits = json:%d text:%d\njson stderr:%s\ntext stderr:%s", jsonCode, textCode, jsonErr, textErr)
	}
	report := decodeContractReport(t, jsonOut)
	if !report.Verdict.WouldFail || report.Verdict.Enforced {
		t.Fatalf("JSON verdict = %+v, want report-only would-fail", report.Verdict)
	}
	if !strings.Contains(textOut, "verdict: would-fail (report only)") {
		t.Fatalf("text output does not expose matching verdict:\n%s", textOut)
	}
}

func TestExplicitHeadRevisionIgnoresWorkingTree(t *testing.T) {
	repo := newFixtureRepo(t, map[string]string{"internal/sample/sample.go": "package sample\n"})
	base := repo.commit(t, "base")
	repo.write(t, "internal/sample/sample.go", longFunction("added", 82))
	head := repo.commit(t, "head")
	repo.write(t, "internal/sample/sample.go", "not go source")

	code, stdout, stderr := runTool(t, repo.dir, "--base", base, "--head", head, "--format", "json")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	report := decodeContractReport(t, stdout)
	if report.Head.Revision != head || !report.Verdict.WouldFail {
		t.Fatalf("head/verdict = %q/%v, want %q/true", report.Head.Revision, report.Verdict.WouldFail, head)
	}
}

func TestHistoryReplaySelectsProductionCommitsAndIsDeterministic(t *testing.T) {
	repo := newFixtureRepo(t, map[string]string{"internal/sample/sample.go": longFunction("one", 8)})
	repo.commit(t, "initial production")
	repo.write(t, "internal/sample/sample.go", longFunction("one", 9))
	repo.commit(t, "production one")
	repo.write(t, "internal/sample/sample_test.go", longFunction("testOnly", 9))
	repo.commit(t, "test only")
	repo.write(t, "internal/sample/sample.go", longFunction("one", 10))
	repo.commit(t, "production two")
	repo.write(t, "internal/sample/extra.go", "package sample\n\nfunc extra() {}\n")
	repo.commit(t, "production three")

	code, first, stderr := runTool(t, repo.dir, "--history", "3", "--format", "json")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, first, stderr)
	}
	var history struct {
		SchemaVersion string `json:"schema_version"`
		Selection     struct {
			Included int `json:"included"`
		} `json:"selection"`
		Commits []struct {
			Subject    string `json:"subject"`
			Production struct {
				Files int `json:"file_count"`
			} `json:"production"`
			Tests struct {
				Files int `json:"file_count"`
			} `json:"tests"`
		} `json:"commits"`
		Trend            map[string]distribution      `json:"trend"`
		ObservationTrend map[string]floatDistribution `json:"observation_trend"`
	}
	if err := json.Unmarshal([]byte(first), &history); err != nil {
		t.Fatalf("decode history: %v\n%s", err, first)
	}
	if history.SchemaVersion == "" || history.Selection.Included != 3 || len(history.Commits) != 3 {
		t.Fatalf("history selection = schema:%q included:%d commits:%d",
			history.SchemaVersion, history.Selection.Included, len(history.Commits))
	}
	for _, commit := range history.Commits {
		if commit.Subject == "test only" {
			t.Fatal("test-only commit was included in production history")
		}
	}
	if len(history.Trend) == 0 || len(history.ObservationTrend) == 0 || history.Commits[len(history.Commits)-1].Tests.Files != 1 {
		t.Fatalf("missing separate trend/test reporting: trend=%d observations=%d tests=%d",
			len(history.Trend), len(history.ObservationTrend), history.Commits[len(history.Commits)-1].Tests.Files)
	}
	secondCode, second, secondErr := runTool(t, repo.dir, "--history", "3", "--format", "json")
	if secondCode != 0 || first != second {
		t.Fatalf("history is not deterministic: code=%d stderr=%s\nfirst bytes=%d second bytes=%d",
			secondCode, secondErr, len(first), len(second))
	}
}

func longFunction(name string, lines int) string {
	if lines < 3 {
		panic("longFunction requires at least three lines")
	}
	var b strings.Builder
	b.WriteString("package sample\n\nfunc ")
	b.WriteString(name)
	b.WriteString("() {\n")
	for i := 0; i < lines-2; i++ {
		fmt.Fprintf(&b, "\t_ = %d\n", i)
	}
	b.WriteString("}\n")
	return b.String()
}

func longFile(lines int) string {
	var b strings.Builder
	b.WriteString("package sample\n")
	for i := 1; i < lines; i++ {
		b.WriteString("// padding\n")
	}
	return b.String()
}

func cloneSource(blankLines int) string {
	body := func(name string) string {
		var b strings.Builder
		fmt.Fprintf(&b, "func %s(input int) int {\n", name)
		for i := 0; i < 35; i++ {
			fmt.Fprintf(&b, "\tinput += %d\n", i)
		}
		b.WriteString("\treturn input\n}\n")
		return b.String()
	}
	return "package sample\n" + strings.Repeat("\n", blankLines) + body("first") + "\n" + body("second")
}

func hasViolation(report contractReport, metric string) bool {
	for _, violation := range report.Comparison.Violations {
		if violation.Metric == metric {
			return true
		}
	}
	return false
}

func decodeContractReport(t *testing.T, data string) contractReport {
	t.Helper()
	var report contractReport
	if err := json.Unmarshal([]byte(data), &report); err != nil {
		t.Fatalf("decode JSON: %v\noutput:\n%s", err, data)
	}
	if report.SchemaVersion == "" || report.Base.Revision == "" {
		t.Fatalf("missing schema/base contract: %+v", report)
	}
	return report
}

type fixtureRepo struct {
	dir string
}

func newFixtureRepo(t *testing.T, files map[string]string) fixtureRepo {
	t.Helper()
	repo := fixtureRepo{dir: t.TempDir()}
	repo.git(t, "init", "-q")
	repo.git(t, "config", "user.name", "Code Health Test")
	repo.git(t, "config", "user.email", "code-health@example.invalid")
	for path, content := range files {
		repo.write(t, path, content)
	}
	return repo
}

func (r fixtureRepo) write(t *testing.T, path, content string) {
	t.Helper()
	fullPath := filepath.Join(r.dir, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (r fixtureRepo) commit(t *testing.T, message string) string {
	t.Helper()
	r.git(t, "add", ".")
	r.git(t, "commit", "-q", "-m", message)
	return strings.TrimSpace(r.git(t, "rev-parse", "HEAD"))
}

func (r fixtureRepo) git(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = r.dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return string(output)
}

func runTool(t *testing.T, dir string, args ...string) (int, string, string) {
	t.Helper()
	cmd := exec.Command(contractToolPath, args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return 0, stdout.String(), stderr.String()
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode(), stdout.String(), stderr.String()
	}
	t.Fatalf("run check-code-health: %v", err)
	return 0, "", ""
}
