package main

import "sort"

type metricValue struct {
	name string
	base int
	head int
	rule metricRule
}

func compareSnapshots(base, head snapshot, cfg config, enforced bool) (comparison, verdict) {
	result := comparison{}
	compareScope("production", base.Production, head.Production, cfg, true, &result)
	compareScope("test", base.Tests, head.Tests, cfg, false, &result)
	sortFindings(result.Violations)
	sortFindings(result.Observations)
	wouldFail := len(result.Violations) > 0
	status := "pass"
	if wouldFail {
		status = "would-fail"
		if enforced {
			status = "fail"
		}
	}
	return result, verdict{Status: status, WouldFail: wouldFail, Enforced: enforced}
}

func compareScope(scope string, base, head scopeReport, cfg config, enforce bool, result *comparison) {
	compareFiles(scope, base, head, cfg, enforce, result)
	compareFunctions(scope, base, head, cfg, enforce, result)
	compareClones(scope, base, head, cfg, enforce, result)
}

func compareFiles(scope string, base, head scopeReport, cfg config, enforce bool, result *comparison) {
	baseFiles := make(map[string]fileMetric, len(base.Files))
	for _, file := range base.Files {
		baseFiles[file.Path] = file
	}
	for _, file := range head.Files {
		rule := cfg.Metrics["file_lines"]
		previous, exists := baseFiles[file.Path]
		if file.Lines > rule.ReportThreshold {
			result.Observations = append(result.Observations, finding{
				Scope: scope, Metric: "file_lines", Symbol: file.Path, Path: file.Path,
				Base: previous.Lines, Head: file.Lines, Threshold: rule.ReportThreshold,
				Reason: "head exceeds report threshold",
			})
		}
		if !enforce {
			continue
		}
		reason := regressionReason(previous.Lines, file.Lines, rule.HardThreshold, exists)
		if reason != "" {
			result.Violations = append(result.Violations, finding{
				Scope: scope, Metric: "file_lines", Symbol: file.Path, Path: file.Path,
				Base: previous.Lines, Head: file.Lines, Threshold: rule.HardThreshold, Reason: reason,
			})
		}
	}
}

func compareFunctions(scope string, base, head scopeReport, cfg config, enforce bool, result *comparison) {
	for _, function := range head.Functions {
		baseIndex, exists := base.FunctionIndex[function.Key]
		var previous functionMetric
		if exists {
			previous = base.Functions[baseIndex]
		} else if enforce {
			result.AddedFunctions++
			result.AddedHelperCalls += head.Calls[function.Name]
		}
		for _, metric := range functionMetricValues(previous, function, cfg) {
			compareFunctionMetric(scope, function, metric, exists, enforce, result)
		}
	}
}

func functionMetricValues(base, head functionMetric, cfg config) []metricValue {
	return []metricValue{
		{name: "function_lines", base: base.Lines, head: head.Lines, rule: cfg.Metrics["function_lines"]},
		{name: "function_statements", base: base.Statements, head: head.Statements, rule: cfg.Metrics["function_statements"]},
		{name: "cognitive_complexity", base: base.CognitiveComplexity, head: head.CognitiveComplexity, rule: cfg.Metrics["cognitive_complexity"]},
		{name: "cyclomatic_complexity", base: base.CyclomaticComplexity, head: head.CyclomaticComplexity, rule: cfg.Metrics["cyclomatic_complexity"]},
	}
}

func compareFunctionMetric(scope string, function functionMetric, metric metricValue, exists, enforce bool, result *comparison) {
	if metric.head > metric.rule.ReportThreshold {
		result.Observations = append(result.Observations, finding{
			Scope: scope, Metric: metric.name, Symbol: function.Key, Path: function.Path,
			Base: metric.base, Head: metric.head, Threshold: metric.rule.ReportThreshold,
			Reason: "head exceeds report threshold",
		})
	}
	if !enforce {
		return
	}
	reason := regressionReason(metric.base, metric.head, metric.rule.HardThreshold, exists)
	if reason != "" {
		result.Violations = append(result.Violations, finding{
			Scope: scope, Metric: metric.name, Symbol: function.Key, Path: function.Path,
			Base: metric.base, Head: metric.head, Threshold: metric.rule.HardThreshold, Reason: reason,
		})
	}
}

func compareClones(scope string, base, head scopeReport, cfg config, enforce bool, result *comparison) {
	cloneRule := cfg.Metrics["clone_tokens"]
	for _, group := range head.CloneGroups {
		_, exists := base.CloneIndex[group.Fingerprint]
		if exists {
			continue
		}
		result.NewCloneGroups++
		path := ""
		if len(group.Fragments) > 0 {
			path = group.Fragments[0].Path
		}
		result.Observations = append(result.Observations, finding{
			Scope: scope, Metric: "clone_tokens", Symbol: group.Fingerprint, Path: path,
			Head: group.Tokens, Threshold: cloneRule.ReportThreshold, Reason: "new clone group",
		})
		if enforce && group.Tokens >= cloneRule.HardThreshold {
			result.Violations = append(result.Violations, finding{
				Scope: scope, Metric: "clone_tokens", Symbol: group.Fingerprint, Path: path,
				Head: group.Tokens, Threshold: cloneRule.HardThreshold, Reason: "new clone group reaches hard threshold",
			})
		}
	}
}

func regressionReason(base, head, hardThreshold int, exists bool) string {
	if !exists {
		if head > hardThreshold {
			return "new code exceeds hard threshold"
		}
		return ""
	}
	if base <= hardThreshold && head > hardThreshold {
		return "existing code crossed hard threshold"
	}
	if base > hardThreshold && head > base {
		return "existing over-limit code increased"
	}
	return ""
}

func sortFindings(findings []finding) {
	sort.Slice(findings, func(i, j int) bool {
		left, right := findings[i], findings[j]
		if left.Scope != right.Scope {
			return left.Scope < right.Scope
		}
		if left.Metric != right.Metric {
			return left.Metric < right.Metric
		}
		if left.Path != right.Path {
			return left.Path < right.Path
		}
		return left.Symbol < right.Symbol
	})
}
