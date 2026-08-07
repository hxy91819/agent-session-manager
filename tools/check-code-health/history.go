package main

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

type selectedCommit struct {
	revision string
	parent   string
	files    int
}

func buildHistory(root, revision string, requested int, cfg config) (historyReport, error) {
	resolved, selected, err := selectHistoryCommits(root, revision, requested, cfg.Scope)
	if err != nil {
		return historyReport{}, err
	}
	report := newHistoryReport(resolved, requested, selected, cfg)
	trendValues := make(map[string][]int)
	observationValues := make(map[string][]float64)
	snapshotCache := make(map[string]snapshot)
	var previous *historyCommit
	for _, item := range selected {
		commit, measureErr := measureHistoryCommit(root, item, cfg, snapshotCache)
		if measureErr != nil {
			return historyReport{}, measureErr
		}
		appendTrendValues(trendValues, "production", commit.Production)
		appendTrendValues(trendValues, "test", commit.Tests)
		appendCommitObservations(trendValues, observationValues, commit)
		if previous != nil {
			countGrowth(report.GrowthCounts, "production", previous.Production, commit.Production)
			countGrowth(report.GrowthCounts, "test", previous.Tests, commit.Tests)
		}
		compactScope(&commit.Production)
		compactScope(&commit.Tests)
		report.Commits = append(report.Commits, commit)
		previous = &report.Commits[len(report.Commits)-1]
	}
	for name, values := range trendValues {
		ruleName := trendRuleName(name)
		report.Trend[name] = makeDistribution(values, cfg.Metrics[ruleName], ruleName == "clone_tokens")
	}
	for name, values := range observationValues {
		report.ObservationTrend[name] = makeFloatDistribution(values)
	}
	return report, nil
}

func selectHistoryCommits(root, revision string, requested int, scope scopeConfig) (string, []selectedCommit, error) {
	resolved, err := resolveRevision(root, revision)
	if err != nil {
		return "", nil, fmt.Errorf("history revision: %w", err)
	}
	candidates, err := gitLines(root, "rev-list", "--first-parent", resolved)
	if err != nil {
		return "", nil, err
	}
	selected := make([]selectedCommit, 0, requested)
	for _, commit := range candidates {
		if len(selected) == requested {
			break
		}
		parent, parentErr := resolveRevision(root, commit+"^1")
		if parentErr != nil {
			continue
		}
		changed, diffErr := changedProductionFiles(root, parent, commit, scope)
		if diffErr != nil {
			return "", nil, diffErr
		}
		if changed > 0 {
			selected = append(selected, selectedCommit{revision: commit, parent: parent, files: changed})
		}
	}
	if len(selected) < requested {
		return "", nil, fmt.Errorf("found only %d commits with production Go changes, requested %d", len(selected), requested)
	}
	for left, right := 0, len(selected)-1; left < right; left, right = left+1, right-1 {
		selected[left], selected[right] = selected[right], selected[left]
	}
	return resolved, selected, nil
}

func newHistoryReport(resolved string, requested int, selected []selectedCommit, cfg config) historyReport {
	return historyReport{
		SchemaVersion: cfg.HistorySchemaVersion,
		Series:        cfg.Series,
		Config:        cfg,
		Selection: historySelection{
			Requested: requested, Included: len(selected), Revision: resolved,
			FirstParent: true, ProductionRoots: append([]string(nil), cfg.Scope.Roots...), ProductionGoOnly: true,
		},
		GrowthCounts:     make(map[string]int),
		Trend:            make(map[string]distribution),
		ObservationTrend: make(map[string]floatDistribution),
	}
}

func measureHistoryCommit(root string, item selectedCommit, cfg config, cache map[string]snapshot) (historyCommit, error) {
	base, err := cachedSnapshot(root, item.parent, cfg, cache)
	if err != nil {
		return historyCommit{}, fmt.Errorf("analyze parent %s: %w", item.parent, err)
	}
	head, err := cachedSnapshot(root, item.revision, cfg, cache)
	if err != nil {
		return historyCommit{}, fmt.Errorf("analyze commit %s: %w", item.revision, err)
	}
	comparison, _ := compareSnapshots(base, head, cfg, false)
	subject, err := gitText(root, "show", "-s", "--format=%s", item.revision)
	if err != nil {
		return historyCommit{}, err
	}
	return historyCommit{
		Revision: item.revision, Parent: item.parent, Subject: subject,
		ChangedProductionFiles: item.files,
		AddedFunctions:         comparison.AddedFunctions, AddedHelperCalls: comparison.AddedHelperCalls,
		Production: head.Production, Tests: head.Tests,
	}, nil
}

func cachedSnapshot(root, revision string, cfg config, cache map[string]snapshot) (snapshot, error) {
	if existing, ok := cache[revision]; ok {
		return existing, nil
	}
	measured, err := analyzeGitRevision(root, revision, cfg)
	if err == nil {
		cache[revision] = measured
	}
	return measured, err
}

func changedProductionFiles(root, base, head string, scope scopeConfig) (int, error) {
	paths, err := gitLines(root, "diff", "--name-only", "--diff-filter=ACMR", base, head, "--")
	if err != nil {
		return 0, err
	}
	count := 0
	for _, path := range paths {
		if isProductionGoPath(path, scope) {
			count++
		}
	}
	return count, nil
}

func isProductionGoPath(path string, scope scopeConfig) bool {
	path = filepath.ToSlash(path)
	if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
		return false
	}
	inRoot := false
	for _, root := range scope.Roots {
		root = strings.Trim(filepath.ToSlash(root), "/")
		if path == root || strings.HasPrefix(path, root+"/") {
			inRoot = true
			break
		}
	}
	if !inRoot {
		return false
	}
	for _, excluded := range scope.ExcludedDirectories {
		for _, part := range strings.Split(path, "/") {
			if part == excluded {
				return false
			}
		}
	}
	return true
}

func appendTrendValues(values map[string][]int, prefix string, scope scopeReport) {
	for _, file := range scope.Files {
		values[prefix+"_file_lines"] = append(values[prefix+"_file_lines"], file.Lines)
	}
	for _, function := range scope.Functions {
		values[prefix+"_function_lines"] = append(values[prefix+"_function_lines"], function.Lines)
		values[prefix+"_function_statements"] = append(values[prefix+"_function_statements"], function.Statements)
		values[prefix+"_cognitive_complexity"] = append(values[prefix+"_cognitive_complexity"], function.CognitiveComplexity)
		values[prefix+"_cyclomatic_complexity"] = append(values[prefix+"_cyclomatic_complexity"], function.CyclomaticComplexity)
	}
	for _, clone := range scope.CloneGroups {
		values[prefix+"_clone_tokens"] = append(values[prefix+"_clone_tokens"], clone.Tokens)
	}
	for _, count := range scope.PackageFiles {
		values[prefix+"_package_file_count"] = append(values[prefix+"_package_file_count"], count)
	}
}

func appendCommitObservations(integerValues map[string][]int, floatValues map[string][]float64, commit historyCommit) {
	integerValues["changed_production_files"] = append(integerValues["changed_production_files"], commit.ChangedProductionFiles)
	integerValues["added_functions"] = append(integerValues["added_functions"], commit.AddedFunctions)
	integerValues["added_helper_calls"] = append(integerValues["added_helper_calls"], commit.AddedHelperCalls)
	floatValues["production_functions_per_kloc"] = append(floatValues["production_functions_per_kloc"], commit.Production.Observations.FunctionsPerKLOC)
	floatValues["production_micro_function_ratio"] = append(floatValues["production_micro_function_ratio"], commit.Production.Observations.MicroFunctionRatio)
	floatValues["test_functions_per_kloc"] = append(floatValues["test_functions_per_kloc"], commit.Tests.Observations.FunctionsPerKLOC)
	floatValues["test_micro_function_ratio"] = append(floatValues["test_micro_function_ratio"], commit.Tests.Observations.MicroFunctionRatio)
	if commit.AddedFunctions > 0 {
		ratio := float64(commit.AddedHelperCalls) / float64(commit.AddedFunctions)
		floatValues["added_helper_calls_per_function"] = append(floatValues["added_helper_calls_per_function"], ratio)
	}
}

func makeFloatDistribution(values []float64) floatDistribution {
	if len(values) == 0 {
		return floatDistribution{}
	}
	ordered := append([]float64(nil), values...)
	sort.Float64s(ordered)
	return floatDistribution{
		P50: ordered[percentileIndex(len(ordered), 0.50)],
		P90: ordered[percentileIndex(len(ordered), 0.90)],
		P95: ordered[percentileIndex(len(ordered), 0.95)],
		Max: ordered[len(ordered)-1],
	}
}

func trendRuleName(name string) string {
	for _, suffix := range []string{
		"function_statements", "cognitive_complexity", "cyclomatic_complexity",
		"function_lines", "file_lines", "clone_tokens",
		"package_file_count", "changed_production_files", "added_helper_calls", "added_functions",
	} {
		if strings.HasSuffix(name, suffix) {
			return suffix
		}
	}
	return ""
}

func countGrowth(counts map[string]int, prefix string, previous, current scopeReport) {
	metrics := map[string][2]float64{
		"file_lines_p95":            {float64(previous.Distributions.FileLines.P95), float64(current.Distributions.FileLines.P95)},
		"file_lines_max":            {float64(previous.Distributions.FileLines.Max), float64(current.Distributions.FileLines.Max)},
		"function_lines_p95":        {float64(previous.Distributions.FunctionLines.P95), float64(current.Distributions.FunctionLines.P95)},
		"function_lines_max":        {float64(previous.Distributions.FunctionLines.Max), float64(current.Distributions.FunctionLines.Max)},
		"function_statements_p95":   {float64(previous.Distributions.FunctionStatements.P95), float64(current.Distributions.FunctionStatements.P95)},
		"function_statements_max":   {float64(previous.Distributions.FunctionStatements.Max), float64(current.Distributions.FunctionStatements.Max)},
		"cognitive_complexity_p95":  {float64(previous.Distributions.CognitiveComplexity.P95), float64(current.Distributions.CognitiveComplexity.P95)},
		"cognitive_complexity_max":  {float64(previous.Distributions.CognitiveComplexity.Max), float64(current.Distributions.CognitiveComplexity.Max)},
		"cyclomatic_complexity_p95": {float64(previous.Distributions.CyclomaticComplexity.P95), float64(current.Distributions.CyclomaticComplexity.P95)},
		"cyclomatic_complexity_max": {float64(previous.Distributions.CyclomaticComplexity.Max), float64(current.Distributions.CyclomaticComplexity.Max)},
		"clone_groups":              {float64(previous.CloneGroupCount), float64(current.CloneGroupCount)},
		"functions_per_kloc":        {previous.Observations.FunctionsPerKLOC, current.Observations.FunctionsPerKLOC},
		"micro_function_ratio":      {previous.Observations.MicroFunctionRatio, current.Observations.MicroFunctionRatio},
		"largest_package_files":     {float64(previous.Observations.LargestPackageFileCount), float64(current.Observations.LargestPackageFileCount)},
	}
	keys := make([]string, 0, len(metrics))
	for name := range metrics {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	for _, name := range keys {
		pair := metrics[name]
		if pair[1] > pair[0] {
			counts[prefix+"_"+name]++
		}
	}
}

func compactScope(scope *scopeReport) {
	scope.Files = nil
	scope.Functions = nil
	scope.CloneGroups = nil
	scope.Calls = nil
	scope.FunctionIndex = nil
	scope.CloneIndex = nil
}
