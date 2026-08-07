package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/fzipp/gocyclo"
	"github.com/mibk/dupl/suffixtree"
	"github.com/mibk/dupl/syntax"
	duplgo "github.com/mibk/dupl/syntax/golang"
	"github.com/uudashr/gocognit"
)

func analyzeSnapshot(root, revision string, cfg config) (snapshot, error) {
	productionFiles, testFiles, err := discoverGoFiles(root, cfg.Scope)
	if err != nil {
		return snapshot{}, err
	}
	production, err := analyzeScope(root, productionFiles, cfg)
	if err != nil {
		return snapshot{}, fmt.Errorf("analyze production source: %w", err)
	}
	tests, err := analyzeScope(root, testFiles, cfg)
	if err != nil {
		return snapshot{}, fmt.Errorf("analyze test source: %w", err)
	}
	return snapshot{Revision: revision, Production: production, Tests: tests}, nil
}

func discoverGoFiles(root string, scope scopeConfig) ([]string, []string, error) {
	excluded := make(map[string]bool, len(scope.ExcludedDirectories))
	for _, name := range scope.ExcludedDirectories {
		excluded[name] = true
	}
	generated, err := regexp.Compile(scope.GeneratedCodeMarker)
	if err != nil {
		return nil, nil, fmt.Errorf("compile generated-code marker: %w", err)
	}

	discovery := sourceDiscovery{root: root, excluded: excluded, generated: generated}
	for _, scopeRoot := range scope.Roots {
		if err := discovery.walkRoot(scopeRoot); err != nil {
			return nil, nil, err
		}
	}
	sort.Strings(discovery.production)
	sort.Strings(discovery.tests)
	return discovery.production, discovery.tests, nil
}

type sourceDiscovery struct {
	root       string
	excluded   map[string]bool
	generated  *regexp.Regexp
	production []string
	tests      []string
}

func (d *sourceDiscovery) walkRoot(scopeRoot string) error {
	start := filepath.Join(d.root, filepath.FromSlash(scopeRoot))
	info, err := os.Stat(start)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stat scope root %s: %w", scopeRoot, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("scope root %s is not a directory", scopeRoot)
	}
	return filepath.WalkDir(start, func(path string, entry fs.DirEntry, walkErr error) error {
		return d.visit(start, path, entry, walkErr)
	})
}

func (d *sourceDiscovery) visit(start, path string, entry fs.DirEntry, walkErr error) error {
	if walkErr != nil {
		return walkErr
	}
	if entry.IsDir() {
		if path != start && d.excluded[entry.Name()] {
			return filepath.SkipDir
		}
		return nil
	}
	if entry.Type()&os.ModeSymlink != 0 && strings.HasSuffix(entry.Name(), ".go") {
		return fmt.Errorf("analyzer cannot read symlinked Go source %s", path)
	}
	if !strings.HasSuffix(entry.Name(), ".go") {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	prefix := data
	if len(prefix) > 2048 {
		prefix = prefix[:2048]
	}
	if d.generated.Match(prefix) {
		return nil
	}
	if strings.HasSuffix(entry.Name(), "_test.go") {
		d.tests = append(d.tests, path)
	} else {
		d.production = append(d.production, path)
	}
	return nil
}

func analyzeScope(root string, paths []string, cfg config) (scopeReport, error) {
	result := scopeReport{
		PackageFiles:  make(map[string]int),
		Calls:         make(map[string]int),
		FunctionIndex: make(map[string]int),
		CloneIndex:    make(map[string]int),
	}
	for _, path := range paths {
		file, functions, calls, err := analyzeSourceFile(root, path)
		if err != nil {
			return scopeReport{}, err
		}
		result.Files = append(result.Files, file)
		result.LineCount += file.Lines
		result.PackageFiles[file.Package]++
		for _, function := range functions {
			result.FunctionIndex[function.Key] = len(result.Functions)
			result.Functions = append(result.Functions, function)
		}
		for name, count := range calls {
			result.Calls[name] += count
		}
	}

	clones, err := detectClones(root, paths, cfg.Metrics["clone_tokens"].ReportThreshold)
	if err != nil {
		return scopeReport{}, err
	}
	result.CloneGroups = clones
	result.FileCount = len(result.Files)
	result.FunctionCount = len(result.Functions)
	result.CloneGroupCount = len(result.CloneGroups)
	for i, group := range result.CloneGroups {
		result.CloneIndex[group.Fingerprint] = i
	}
	result.finalize(cfg)
	return result, nil
}

func analyzeSourceFile(root, path string) (fileMetric, []functionMetric, map[string]int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return fileMetric{}, nil, nil, fmt.Errorf("read %s: %w", path, err)
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, data, parser.ParseComments|parser.AllErrors)
	if err != nil {
		return fileMetric{}, nil, nil, fmt.Errorf("parse %s: %w", relativePath(root, path), err)
	}
	relative := relativePath(root, path)
	packagePath := filepath.ToSlash(filepath.Dir(relative))
	if packagePath == "." {
		packagePath = file.Name.Name
	}
	metric := fileMetric{Path: relative, Package: packagePath, Lines: physicalLines(data)}
	var functions []functionMetric
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Body != nil {
			functions = append(functions, measureFunction(fset, function, metric))
		}
	}
	return metric, functions, collectCalls(file), nil
}

func measureFunction(fset *token.FileSet, function *ast.FuncDecl, file fileMetric) functionMetric {
	receiver := receiverName(function)
	return functionMetric{
		Key:                  file.Package + "|" + receiver + "|" + function.Name.Name,
		Package:              file.Package,
		Receiver:             receiver,
		Name:                 function.Name.Name,
		Path:                 file.Path,
		Line:                 fset.Position(function.Pos()).Line,
		Lines:                fset.Position(function.End()).Line - fset.Position(function.Pos()).Line + 1,
		Statements:           statementCount(function.Body),
		CognitiveComplexity:  gocognit.Complexity(function),
		CyclomaticComplexity: gocyclo.Complexity(function),
	}
}

func collectCalls(file *ast.File) map[string]int {
	calls := make(map[string]int)
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch called := call.Fun.(type) {
		case *ast.Ident:
			calls[called.Name]++
		case *ast.SelectorExpr:
			calls[called.Sel.Name]++
		}
		return true
	})
	return calls
}

func (r *scopeReport) finalize(cfg config) {
	fileLines := make([]int, 0, len(r.Files))
	for _, file := range r.Files {
		fileLines = append(fileLines, file.Lines)
	}
	functionLines := make([]int, 0, len(r.Functions))
	statements := make([]int, 0, len(r.Functions))
	cognitive := make([]int, 0, len(r.Functions))
	cyclomatic := make([]int, 0, len(r.Functions))
	microCount := 0
	microThreshold := cfg.Metrics["micro_function_statements"].ObservationThreshold
	for _, function := range r.Functions {
		functionLines = append(functionLines, function.Lines)
		statements = append(statements, function.Statements)
		cognitive = append(cognitive, function.CognitiveComplexity)
		cyclomatic = append(cyclomatic, function.CyclomaticComplexity)
		if function.Statements <= microThreshold {
			microCount++
		}
	}
	cloneTokens := make([]int, 0, len(r.CloneGroups))
	for _, group := range r.CloneGroups {
		cloneTokens = append(cloneTokens, group.Tokens)
	}
	r.Distributions = distributions{
		FileLines:            makeDistribution(fileLines, cfg.Metrics["file_lines"], false),
		FunctionLines:        makeDistribution(functionLines, cfg.Metrics["function_lines"], false),
		FunctionStatements:   makeDistribution(statements, cfg.Metrics["function_statements"], false),
		CognitiveComplexity:  makeDistribution(cognitive, cfg.Metrics["cognitive_complexity"], false),
		CyclomaticComplexity: makeDistribution(cyclomatic, cfg.Metrics["cyclomatic_complexity"], false),
		CloneTokens:          makeDistribution(cloneTokens, cfg.Metrics["clone_tokens"], true),
	}
	largestPackage := 0
	for _, count := range r.PackageFiles {
		if count > largestPackage {
			largestPackage = count
		}
	}
	r.Observations.LargestPackageFileCount = largestPackage
	if r.LineCount > 0 {
		r.Observations.FunctionsPerKLOC = float64(r.FunctionCount) * 1000 / float64(r.LineCount)
	}
	if r.FunctionCount > 0 {
		r.Observations.MicroFunctionRatio = float64(microCount) / float64(r.FunctionCount)
	}
}

func makeDistribution(values []int, rule metricRule, inclusive bool) distribution {
	if len(values) == 0 {
		return distribution{}
	}
	ordered := append([]int(nil), values...)
	sort.Ints(ordered)
	result := distribution{
		P50: ordered[percentileIndex(len(ordered), 0.50)],
		P90: ordered[percentileIndex(len(ordered), 0.90)],
		P95: ordered[percentileIndex(len(ordered), 0.95)],
		Max: ordered[len(ordered)-1],
	}
	for _, value := range ordered {
		if exceeds(value, rule.ReportThreshold, inclusive) {
			result.OverReport++
		}
		if exceeds(value, rule.HardThreshold, inclusive) {
			result.OverHard++
		}
	}
	return result
}

func percentileIndex(length int, percentile float64) int {
	index := int(math.Ceil(float64(length)*percentile)) - 1
	if index < 0 {
		return 0
	}
	return index
}

func exceeds(value, threshold int, inclusive bool) bool {
	if threshold == 0 {
		return false
	}
	if inclusive {
		return value >= threshold
	}
	return value > threshold
}

func statementCount(body *ast.BlockStmt) int {
	count := 0
	ast.Inspect(body, func(node ast.Node) bool {
		statement, ok := node.(ast.Stmt)
		if !ok {
			return true
		}
		switch statement.(type) {
		case *ast.BlockStmt, *ast.EmptyStmt:
		default:
			count++
		}
		return true
	})
	return count
}

func receiverName(function *ast.FuncDecl) string {
	if function.Recv == nil || len(function.Recv.List) == 0 {
		return ""
	}
	var output bytes.Buffer
	if err := format.Node(&output, token.NewFileSet(), function.Recv.List[0].Type); err != nil {
		return "<unknown>"
	}
	return output.String()
}

func detectClones(root string, paths []string, threshold int) ([]cloneGroup, error) {
	tree, data, err := buildCloneTree(root, paths)
	if err != nil {
		return nil, err
	}
	grouped, err := collectCloneGroups(root, tree, data, threshold)
	if err != nil {
		return nil, err
	}
	return sortedCloneGroups(grouped), nil
}

func buildCloneTree(root string, paths []string) (*suffixtree.STree, []*syntax.Node, error) {
	tree := suffixtree.New()
	data := make([]*syntax.Node, 0)
	for _, path := range paths {
		node, err := duplgo.Parse(path)
		if err != nil {
			return nil, nil, fmt.Errorf("dupl parse %s: %w", relativePath(root, path), err)
		}
		sequence := syntax.Serialize(node)
		data = append(data, sequence...)
		for _, token := range sequence {
			tree.Update(token)
		}
	}
	tree.Update(&syntax.Node{Type: -1})
	return tree, data, nil
}

func collectCloneGroups(root string, tree *suffixtree.STree, data []*syntax.Node, threshold int) (map[string]*cloneGroup, error) {
	grouped := make(map[string]*cloneGroup)
	seenFragments := make(map[string]map[string]bool)
	for match := range tree.FindDuplOver(threshold) {
		found := syntax.FindSyntaxUnits(data, match, threshold)
		if len(found.Frags) == 0 {
			continue
		}
		fingerprint := normalizedFingerprint(found.Hash)
		group := grouped[fingerprint]
		if group == nil {
			group = &cloneGroup{Fingerprint: fingerprint, Tokens: fragmentTokens(found.Frags[0])}
			grouped[fingerprint] = group
			seenFragments[fingerprint] = make(map[string]bool)
		}
		if err := addCloneFragments(root, group, seenFragments[fingerprint], found.Frags); err != nil {
			return nil, err
		}
	}
	return grouped, nil
}

func addCloneFragments(root string, group *cloneGroup, seen map[string]bool, fragments [][]*syntax.Node) error {
	for _, fragment := range fragments {
		if len(fragment) == 0 {
			continue
		}
		first, last := fragment[0], fragment[len(fragment)-1]
		key := first.Filename + fmt.Sprintf(":%d:%d", first.Pos, last.End)
		if seen[key] {
			continue
		}
		seen[key] = true
		content, err := os.ReadFile(first.Filename)
		if err != nil {
			return fmt.Errorf("dupl read %s: %w", relativePath(root, first.Filename), err)
		}
		group.Fragments = append(group.Fragments, cloneFragment{
			Path: relativePath(root, first.Filename), StartLine: lineAt(content, first.Pos), EndLine: lineAt(content, last.End-1),
		})
	}
	return nil
}

func sortedCloneGroups(grouped map[string]*cloneGroup) []cloneGroup {
	groups := make([]cloneGroup, 0, len(grouped))
	for _, group := range grouped {
		if len(group.Fragments) < 2 {
			continue
		}
		sort.Slice(group.Fragments, func(i, j int) bool {
			if group.Fragments[i].Path == group.Fragments[j].Path {
				return group.Fragments[i].StartLine < group.Fragments[j].StartLine
			}
			return group.Fragments[i].Path < group.Fragments[j].Path
		})
		groups = append(groups, *group)
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].Fingerprint < groups[j].Fingerprint })
	return groups
}

func normalizedFingerprint(duplHash string) string {
	digest := sha256.Sum256([]byte(duplHash))
	return hex.EncodeToString(digest[:])
}

func fragmentTokens(fragment []*syntax.Node) int {
	tokens := 0
	for _, node := range fragment {
		tokens += node.Owns + 1
	}
	return tokens
}

func physicalLines(data []byte) int {
	if len(data) == 0 {
		return 0
	}
	lines := bytes.Count(data, []byte{'\n'})
	if data[len(data)-1] != '\n' {
		lines++
	}
	return lines
}

func lineAt(data []byte, offset int) int {
	if offset < 0 {
		offset = 0
	}
	if offset > len(data) {
		offset = len(data)
	}
	return bytes.Count(data[:offset], []byte{'\n'}) + 1
}

func relativePath(root, path string) string {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(relative)
}
