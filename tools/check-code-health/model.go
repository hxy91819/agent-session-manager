package main

import "embed"

//go:embed config.json
var configFS embed.FS

type config struct {
	SchemaVersion        string                `json:"schema_version"`
	HistorySchemaVersion string                `json:"history_schema_version"`
	Series               string                `json:"series"`
	Mode                 string                `json:"mode"`
	Scope                scopeConfig           `json:"scope"`
	Analyzers            map[string]string     `json:"analyzers"`
	Metrics              map[string]metricRule `json:"metrics"`
}

type scopeConfig struct {
	Roots               []string `json:"roots"`
	ExcludedDirectories []string `json:"excluded_directories"`
	GeneratedCodeMarker string   `json:"generated_code_marker"`
}

type metricRule struct {
	Definition           string `json:"definition"`
	ReportThreshold      int    `json:"report_threshold,omitempty"`
	HardThreshold        int    `json:"hard_threshold,omitempty"`
	ObservationThreshold int    `json:"observation_threshold,omitempty"`
}

type report struct {
	SchemaVersion string     `json:"schema_version"`
	Series        string     `json:"series"`
	Config        config     `json:"config"`
	Base          snapshot   `json:"base"`
	Head          snapshot   `json:"head"`
	Comparison    comparison `json:"comparison"`
	Verdict       verdict    `json:"verdict"`
}

type snapshot struct {
	Revision   string      `json:"revision"`
	Production scopeReport `json:"production"`
	Tests      scopeReport `json:"tests"`
}

type scopeReport struct {
	FileCount       int              `json:"file_count"`
	LineCount       int              `json:"line_count"`
	FunctionCount   int              `json:"function_count"`
	CloneGroupCount int              `json:"clone_group_count"`
	Distributions   distributions    `json:"distributions"`
	Observations    observations     `json:"observations"`
	PackageFiles    map[string]int   `json:"package_files"`
	Files           []fileMetric     `json:"files"`
	Functions       []functionMetric `json:"functions"`
	CloneGroups     []cloneGroup     `json:"clone_groups"`
	Calls           map[string]int   `json:"-"`
	FunctionIndex   map[string]int   `json:"-"`
	CloneIndex      map[string]int   `json:"-"`
}

type distributions struct {
	FileLines            distribution `json:"file_lines"`
	FunctionLines        distribution `json:"function_lines"`
	FunctionStatements   distribution `json:"function_statements"`
	CognitiveComplexity  distribution `json:"cognitive_complexity"`
	CyclomaticComplexity distribution `json:"cyclomatic_complexity"`
	CloneTokens          distribution `json:"clone_tokens"`
}

type distribution struct {
	P50        int `json:"p50"`
	P90        int `json:"p90"`
	P95        int `json:"p95"`
	Max        int `json:"max"`
	OverReport int `json:"over_report"`
	OverHard   int `json:"over_hard"`
}

type observations struct {
	FunctionsPerKLOC        float64 `json:"functions_per_kloc"`
	MicroFunctionRatio      float64 `json:"micro_function_ratio"`
	LargestPackageFileCount int     `json:"largest_package_file_count"`
}

type fileMetric struct {
	Path    string `json:"path"`
	Package string `json:"package"`
	Lines   int    `json:"lines"`
}

type functionMetric struct {
	Key                  string `json:"key"`
	Package              string `json:"package"`
	Receiver             string `json:"receiver,omitempty"`
	Name                 string `json:"name"`
	Path                 string `json:"path"`
	Line                 int    `json:"line"`
	Lines                int    `json:"lines"`
	Statements           int    `json:"statements"`
	CognitiveComplexity  int    `json:"cognitive_complexity"`
	CyclomaticComplexity int    `json:"cyclomatic_complexity"`
}

type cloneGroup struct {
	Fingerprint string          `json:"fingerprint"`
	Tokens      int             `json:"tokens"`
	Fragments   []cloneFragment `json:"fragments"`
}

type cloneFragment struct {
	Path      string `json:"path"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
}

type comparison struct {
	Violations       []finding `json:"violations"`
	Observations     []finding `json:"observations"`
	NewCloneGroups   int       `json:"new_clone_groups"`
	AddedFunctions   int       `json:"added_functions"`
	AddedHelperCalls int       `json:"added_helper_calls"`
}

type finding struct {
	Scope     string `json:"scope"`
	Metric    string `json:"metric"`
	Symbol    string `json:"symbol"`
	Path      string `json:"path"`
	Base      int    `json:"base"`
	Head      int    `json:"head"`
	Threshold int    `json:"threshold"`
	Reason    string `json:"reason"`
}

type verdict struct {
	Status    string `json:"status"`
	WouldFail bool   `json:"would_fail"`
	Enforced  bool   `json:"enforced"`
}

type historyReport struct {
	SchemaVersion    string                       `json:"schema_version"`
	Series           string                       `json:"series"`
	Config           config                       `json:"config"`
	Selection        historySelection             `json:"selection"`
	Commits          []historyCommit              `json:"commits"`
	GrowthCounts     map[string]int               `json:"growth_counts"`
	Trend            map[string]distribution      `json:"trend"`
	ObservationTrend map[string]floatDistribution `json:"observation_trend"`
}

type floatDistribution struct {
	P50 float64 `json:"p50"`
	P90 float64 `json:"p90"`
	P95 float64 `json:"p95"`
	Max float64 `json:"max"`
}

type historySelection struct {
	Requested        int      `json:"requested"`
	Included         int      `json:"included"`
	Revision         string   `json:"revision"`
	FirstParent      bool     `json:"first_parent"`
	ProductionRoots  []string `json:"production_roots"`
	ProductionGoOnly bool     `json:"production_go_only"`
}

type historyCommit struct {
	Revision               string      `json:"revision"`
	Parent                 string      `json:"parent"`
	Subject                string      `json:"subject"`
	ChangedProductionFiles int         `json:"changed_production_files"`
	AddedFunctions         int         `json:"added_functions"`
	AddedHelperCalls       int         `json:"added_helper_calls"`
	Production             scopeReport `json:"production"`
	Tests                  scopeReport `json:"tests"`
}
