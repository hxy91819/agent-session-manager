package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"sort"
	"strings"
)

const (
	exitOK          = 0
	exitPolicy      = 1
	exitOperational = 2
)

type options struct {
	baseRevision string
	headRevision string
	historyCount int
	revision     string
	format       string
	enforce      bool
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	cfg, err := loadConfig()
	if err != nil {
		writeError(stderr, "config: %v", err)
		return exitOperational
	}
	opts, err := parseOptions(args, stderr)
	if err != nil {
		writeError(stderr, "%v", err)
		return exitOperational
	}
	root, err := repositoryRoot()
	if err != nil {
		writeError(stderr, "%v", err)
		return exitOperational
	}

	if opts.historyCount > 0 {
		history, historyErr := buildHistory(root, opts.revision, opts.historyCount, cfg)
		if historyErr != nil {
			writeError(stderr, "history analysis: %v", historyErr)
			return exitOperational
		}
		if outputErr := writeHistory(stdout, history, opts.format); outputErr != nil {
			writeError(stderr, "output: %v", outputErr)
			return exitOperational
		}
		return exitOK
	}

	base, err := analyzeGitRevision(root, opts.baseRevision, cfg)
	if err != nil {
		writeError(stderr, "base revision: %v", err)
		return exitOperational
	}
	var head snapshot
	if opts.headRevision != "" {
		head, err = analyzeGitRevision(root, opts.headRevision, cfg)
		if err != nil {
			writeError(stderr, "head revision: %v", err)
			return exitOperational
		}
	} else {
		head, err = analyzeSnapshot(root, "WORKTREE", cfg)
		if err != nil {
			writeError(stderr, "analyzer failure: %v", err)
			return exitOperational
		}
	}
	comparison, verdict := compareSnapshots(base, head, cfg, opts.enforce)
	result := report{
		SchemaVersion: cfg.SchemaVersion,
		Series:        cfg.Series,
		Config:        cfg,
		Base:          base,
		Head:          head,
		Comparison:    comparison,
		Verdict:       verdict,
	}
	if err := writeReport(stdout, result, opts.format); err != nil {
		writeError(stderr, "output: %v", err)
		return exitOperational
	}
	if opts.enforce && verdict.WouldFail {
		return exitPolicy
	}
	return exitOK
}

func writeError(output io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(output, "check-code-health: "+format+"\n", args...)
}

func parseOptions(args []string, stderr io.Writer) (options, error) {
	flags := flag.NewFlagSet("check-code-health", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var opts options
	flags.StringVar(&opts.baseRevision, "base", "", "explicit base revision to compare")
	flags.StringVar(&opts.headRevision, "head", "", "head revision; defaults to the current worktree")
	flags.IntVar(&opts.historyCount, "history", 0, "replay this many first-parent commits with production Go changes")
	flags.StringVar(&opts.revision, "revision", "HEAD", "history selection endpoint")
	flags.StringVar(&opts.format, "format", "text", "output format: text or json")
	flags.BoolVar(&opts.enforce, "enforce", false, "return exit 1 when the diff violates hard thresholds")
	if err := flags.Parse(args); err != nil {
		return options{}, err
	}
	if flags.NArg() != 0 {
		return options{}, fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	if opts.format != "text" && opts.format != "json" {
		return options{}, fmt.Errorf("invalid --format %q: use text or json", opts.format)
	}
	if opts.historyCount < 0 {
		return options{}, errors.New("--history must be positive")
	}
	if opts.historyCount > 0 {
		if opts.baseRevision != "" || opts.headRevision != "" || opts.enforce {
			return options{}, errors.New("--history cannot be combined with --base, --head, or --enforce")
		}
		return opts, nil
	}
	if opts.baseRevision == "" {
		return options{}, errors.New("--base is required unless --history is used")
	}
	return opts, nil
}

func loadConfig() (config, error) {
	data, err := configFS.ReadFile("config.json")
	if err != nil {
		return config{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var cfg config
	if err := decoder.Decode(&cfg); err != nil {
		return config{}, err
	}
	if cfg.SchemaVersion == "" || cfg.HistorySchemaVersion == "" || cfg.Series == "" {
		return config{}, errors.New("schema versions and series are required")
	}
	if cfg.Mode != "report" {
		return config{}, fmt.Errorf("unsupported experiment mode %q", cfg.Mode)
	}
	for _, name := range []string{
		"file_lines", "function_lines", "function_statements",
		"cognitive_complexity", "cyclomatic_complexity", "clone_tokens",
	} {
		rule, ok := cfg.Metrics[name]
		if !ok {
			return config{}, fmt.Errorf("missing metric %q", name)
		}
		if rule.Definition == "" || rule.ReportThreshold <= 0 || rule.HardThreshold <= 0 {
			return config{}, fmt.Errorf("metric %q requires definition and positive thresholds", name)
		}
	}
	if cfg.Metrics["micro_function_statements"].ObservationThreshold <= 0 {
		return config{}, errors.New("micro_function_statements requires a positive observation threshold")
	}
	if err := validateAnalyzerVersions(cfg.Analyzers); err != nil {
		return config{}, err
	}
	return cfg, nil
}

func validateAnalyzerVersions(expected map[string]string) error {
	build, ok := debug.ReadBuildInfo()
	if !ok {
		return errors.New("build dependency versions are unavailable")
	}
	actual := make(map[string]string, len(build.Deps))
	for _, dependency := range build.Deps {
		actual[dependency.Path] = dependency.Version
	}
	for _, key := range []string{"cognitive_complexity", "cyclomatic_complexity", "clone_detection"} {
		entry := expected[key]
		separator := strings.LastIndex(entry, "@")
		if separator <= 0 || separator == len(entry)-1 {
			return fmt.Errorf("analyzer %q must use module@version", key)
		}
		module, version := entry[:separator], entry[separator+1:]
		if actual[module] != version {
			return fmt.Errorf("analyzer %q is configured as %s but build uses %s", key, entry, actual[module])
		}
	}
	return nil
}

func writeReport(output io.Writer, result report, format string) error {
	if format == "json" {
		return writeJSON(output, result)
	}
	var text strings.Builder
	_, _ = fmt.Fprintf(&text, "code-health %s (%s)\n", result.SchemaVersion, result.Series)
	_, _ = fmt.Fprintf(&text, "base: %s\nhead: %s\n", result.Base.Revision, result.Head.Revision)
	writeScopeSummary(&text, "production", result.Head.Production)
	writeScopeSummary(&text, "test (report only)", result.Head.Tests)
	_, _ = fmt.Fprintf(&text, "violations: %d; observations: %d; new clone groups: %d\n",
		len(result.Comparison.Violations), len(result.Comparison.Observations), result.Comparison.NewCloneGroups)
	for _, violation := range result.Comparison.Violations {
		_, _ = fmt.Fprintf(&text, "VIOLATION %s %s %s: %d -> %d (%s; threshold %d)\n",
			violation.Scope, violation.Metric, violation.Symbol, violation.Base, violation.Head, violation.Reason, violation.Threshold)
	}
	if result.Verdict.WouldFail && !result.Verdict.Enforced {
		text.WriteString("verdict: would-fail (report only)\n")
	} else {
		_, _ = fmt.Fprintf(&text, "verdict: %s\n", result.Verdict.Status)
	}
	_, err := io.WriteString(output, text.String())
	return err
}

func writeScopeSummary(output *strings.Builder, label string, scope scopeReport) {
	_, _ = fmt.Fprintf(output, "%s: %d files, %d lines, %d functions, %d clone groups\n",
		label, scope.FileCount, scope.LineCount, scope.FunctionCount, scope.CloneGroupCount)
}

func writeHistory(output io.Writer, result historyReport, format string) error {
	if format == "json" {
		return writeJSON(output, result)
	}
	var text strings.Builder
	_, _ = fmt.Fprintf(&text, "code-health history %s (%s)\n", result.SchemaVersion, result.Series)
	_, _ = fmt.Fprintf(&text, "revision: %s; commits: %d; first-parent: true; production Go changes only\n",
		result.Selection.Revision, result.Selection.Included)
	if len(result.Commits) > 0 {
		_, _ = fmt.Fprintf(&text, "range: %s..%s\n", result.Commits[0].Revision, result.Commits[len(result.Commits)-1].Revision)
	}
	keys := make([]string, 0, len(result.Trend))
	for key := range result.Trend {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		trend := result.Trend[key]
		_, _ = fmt.Fprintf(&text, "%s: p50=%d p90=%d p95=%d max=%d\n", key, trend.P50, trend.P90, trend.P95, trend.Max)
	}
	observationKeys := make([]string, 0, len(result.ObservationTrend))
	for key := range result.ObservationTrend {
		observationKeys = append(observationKeys, key)
	}
	sort.Strings(observationKeys)
	for _, key := range observationKeys {
		trend := result.ObservationTrend[key]
		_, _ = fmt.Fprintf(&text, "%s: p50=%.4f p90=%.4f p95=%.4f max=%.4f\n", key, trend.P50, trend.P90, trend.P95, trend.Max)
	}
	_, err := io.WriteString(output, text.String())
	return err
}

func writeJSON(output io.Writer, value any) error {
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
