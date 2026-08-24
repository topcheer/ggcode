package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/mod/modfile"
)

// DepGraphTool implements the dep_graph tool that analyzes Go package-level
// dependency relationships in a project. It builds an import graph from source
// files and supports queries: reverse dependencies, import cycles, fan-in/fan-out
// metrics, and architecture layering analysis.
type DepGraphTool struct{ WorkingDir string }

func (t DepGraphTool) Name() string { return "dep_graph" }

func (t DepGraphTool) Description() string {
	return "Analyze Go package-level dependency relationships in a project. Builds an import graph " +
		"from source files and supports reverse dependency lookup, import cycle detection, and " +
		"fan-in/fan-out metrics. Use before modifying a package to understand blast radius, or to " +
		"identify architecture layering issues. Works on the current Go module."
}

func (t DepGraphTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {
				"type": "string",
				"description": "Directory to analyze (default: current working directory)"
			},
			"action": {
				"type": "string",
				"enum": ["overview", "reverse_deps", "cycles", "hotspots"],
				"description": "overview: full graph summary; reverse_deps: packages that import a target; cycles: import cycle detection; hotspots: most depended-upon packages. Default: overview."
			},
			"target": {
				"type": "string",
				"description": "Target package path (import path suffix) for reverse_deps action. Matches any package whose import path ends with this suffix."
			},
			"max_results": {
				"type": "integer",
				"description": "Maximum number of results to return (default: 30)",
				"default": 30
			},
			"description": {
				"type": "string",
				"description": "REQUIRED. Brief activity label shown in the UI."
			}
		},
		"required": ["description"]
	}`)
}

// pkgNode represents a package in the dependency graph.
type pkgNode struct {
	Path      string   `json:"path"`
	ShortName string   `json:"short_name"`
	Imports   []string `json:"imports"`
	Imported  []string `json:"imported_by"`
	FileCount int      `json:"file_count"`
}

// depGraphResult is the structured output of the tool.
type depGraphResult struct {
	ModulePath  string     `json:"module_path,omitempty"`
	Action      string     `json:"action"`
	Packages    int        `json:"packages"`
	Edges       int        `json:"edges"`
	Nodes       []pkgNode  `json:"nodes,omitempty"`
	Cycles      [][]string `json:"cycles,omitempty"`
	Hotspots    []pkgNode  `json:"hotspots,omitempty"`
	ReverseDeps []string   `json:"reverse_deps,omitempty"`
	Target      string     `json:"target,omitempty"`
}

func (t DepGraphTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Path       string `json:"path"`
		Action     string `json:"action"`
		Target     string `json:"target"`
		MaxResults int    `json:"max_results"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	if args.Action == "" {
		args.Action = "overview"
	}
	if args.MaxResults <= 0 {
		args.MaxResults = 30
	}

	dir := resolveDir(args.Path, t.WorkingDir)
	if dir == "" {
		dir = "."
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		absDir = dir
	}

	// Find go.mod to get module path
	modPath, modRoot := findModulePath(absDir)
	if modPath == "" {
		return Result{IsError: true, Content: "no go.mod found - dep_graph requires a Go module"}, nil
	}

	graph, fileCount, err := buildDepGraph(ctx, modRoot, modPath)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("failed to build dependency graph: %v", err)}, nil
	}

	computeReverseEdges(graph)

	result := depGraphResult{
		ModulePath: modPath,
		Action:     args.Action,
		Packages:   len(graph),
		Edges:      countEdges(graph),
	}

	if err := populateActionResult(&result, args, graph, fileCount); err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}

	return Result{Content: formatDepGraphResult(result)}, nil
}

// computeReverseEdges populates the Imported field for each node by reversing
// the forward import edges, then deduplicates.
func computeReverseEdges(graph map[string]*pkgNode) {
	for pkg, node := range graph {
		for _, imp := range node.Imports {
			if dep, ok := graph[imp]; ok {
				dep.Imported = append(dep.Imported, pkg)
			}
		}
	}
	for _, node := range graph {
		node.Imported = dedupSorted(node.Imported)
	}
}

// countEdges returns total forward import edges in the graph.
func countEdges(graph map[string]*pkgNode) int {
	count := 0
	for _, node := range graph {
		count += len(node.Imports)
	}
	return count
}

// populateActionResult fills the result struct based on the requested action.
func populateActionResult(result *depGraphResult, args struct {
	Path       string `json:"path"`
	Action     string `json:"action"`
	Target     string `json:"target"`
	MaxResults int    `json:"max_results"`
}, graph map[string]*pkgNode, fileCount map[string]int) error {
	switch args.Action {
	case "overview":
		result.Nodes = buildOverview(graph, fileCount, args.MaxResults)
	case "reverse_deps":
		if args.Target == "" {
			return fmt.Errorf("target parameter is required for reverse_deps action")
		}
		result.Target = args.Target
		result.ReverseDeps = findReverseDeps(graph, args.Target, args.MaxResults)
	case "cycles":
		result.Cycles = findCycles(graph, args.MaxResults)
	case "hotspots":
		result.Hotspots = findHotspots(graph, args.MaxResults)
	default:
		return fmt.Errorf("unknown action: %s", args.Action)
	}
	return nil
}

// findModulePath walks up from dir to find go.mod and returns (modulePath, modRoot).
func findModulePath(dir string) (string, string) {
	d := dir
	for {
		modFile := filepath.Join(d, "go.mod")
		if data, err := os.ReadFile(modFile); err == nil {
			parsed, err := modfile.Parse(modFile, data, nil)
			if err == nil && parsed.Module != nil {
				return parsed.Module.Mod.Path, d
			}
		}
		parent := filepath.Dir(d)
		if parent == d {
			break
		}
		d = parent
	}
	return "", ""
}

// buildDepGraph parses all .go files under modRoot, extracts intra-module imports,
// and returns a map of package path -> *pkgNode.
func buildDepGraph(ctx context.Context, modRoot, modulePath string) (map[string]*pkgNode, map[string]int, error) {
	graph := make(map[string]*pkgNode)
	fileCount := make(map[string]int)

	fset := token.NewFileSet()

	var walkCtx context.Context = ctx
	err := filepath.Walk(modRoot, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if shouldSkipDir(info) {
			return filepath.SkipDir
		}
		if !shouldParseFile(info) {
			return nil
		}
		select {
		case <-walkCtx.Done():
			return walkCtx.Err()
		default:
		}
		return processGoFile(fset, path, modRoot, modulePath, graph, fileCount)
	})
	if err != nil {
		return nil, nil, err
	}

	// Dedup and sort imports per package
	for _, node := range graph {
		node.Imports = dedupSorted(node.Imports)
	}

	// Ensure imported packages exist as nodes even if they have no .go files
	// in the module (edge case: packages only used as imports)
	for _, node := range graph {
		for _, imp := range node.Imports {
			if _, ok := graph[imp]; !ok {
				graph[imp] = &pkgNode{
					Path:      imp,
					ShortName: shortPkgName(imp, modulePath),
				}
			}
		}
	}

	return graph, fileCount, nil
}

// shouldSkipDir returns true for directories that should not be traversed.
func shouldSkipDir(info os.FileInfo) bool {
	if !info.IsDir() {
		return false
	}
	name := info.Name()
	return name == "vendor" || name == "testdata" || name == ".git" ||
		(strings.HasPrefix(name, ".") && name != ".")
}

// shouldParseFile returns true for non-test .go files.
func shouldParseFile(info os.FileInfo) bool {
	if info.IsDir() {
		return false
	}
	path := info.Name()
	return strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go")
}

// processGoFile parses a single .go file and records its package imports in the graph.
func processGoFile(fset *token.FileSet, path, modRoot, modulePath string, graph map[string]*pkgNode, fileCount map[string]int) error {
	f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
	if err != nil {
		return nil // skip unparseable files
	}

	relDir, err := filepath.Rel(modRoot, filepath.Dir(path))
	if err != nil {
		return nil
	}
	var pkgPath string
	if relDir == "." {
		pkgPath = modulePath
	} else {
		pkgPath = modulePath + "/" + filepath.ToSlash(relDir)
	}

	node, ok := graph[pkgPath]
	if !ok {
		node = &pkgNode{
			Path:      pkgPath,
			ShortName: shortPkgName(pkgPath, modulePath),
		}
		graph[pkgPath] = node
	}
	node.FileCount++
	fileCount[pkgPath]++

	for _, imp := range f.Imports {
		impPath := strings.Trim(imp.Path.Value, `"`)
		// #1002: require a "/" (or exact) module boundary — a bare prefix
		// match pulls in prefix-colliding external packages (module
		// k8s.io/api must not claim k8s.io/apimachinery imports).
		if impPath != modulePath && !strings.HasPrefix(impPath, modulePath+"/") {
			continue
		}
		if impPath == pkgPath {
			continue
		}
		node.Imports = append(node.Imports, impPath)
	}
	return nil
}

// shortPkgName returns the last component of a package path, relative to module.
func shortPkgName(pkgPath, modulePath string) string {
	if pkgPath == modulePath {
		return filepath.Base(modulePath)
	}
	if strings.HasPrefix(pkgPath, modulePath+"/") {
		rel := strings.TrimPrefix(pkgPath, modulePath+"/")
		return rel
	}
	return pkgPath
}

// dedupSorted returns a sorted, deduplicated slice.
func dedupSorted(s []string) []string {
	if len(s) <= 1 {
		return s
	}
	sort.Strings(s)
	out := make([]string, 0, len(s))
	out = append(out, s[0])
	for i := 1; i < len(s); i++ {
		if s[i] != s[i-1] {
			out = append(out, s[i])
		}
	}
	return out
}

// buildOverview returns a sorted list of all packages with their metrics.
func buildOverview(graph map[string]*pkgNode, fileCount map[string]int, maxResults int) []pkgNode {
	nodes := make([]pkgNode, 0, len(graph))
	for _, n := range graph {
		nodes = append(nodes, *n)
	}
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].Path < nodes[j].Path
	})
	if len(nodes) > maxResults {
		nodes = nodes[:maxResults]
	}
	return nodes
}

// findReverseDeps finds all packages that import the target (directly or transitively).
func findReverseDeps(graph map[string]*pkgNode, target string, maxResults int) []string {
	// Find matching package(s)
	var targets []string
	for pkgPath := range graph {
		if pkgPath == target || strings.HasSuffix(pkgPath, "/"+target) || filepath.Base(pkgPath) == target {
			targets = append(targets, pkgPath)
		}
	}
	if len(targets) == 0 {
		return nil
	}

	// BFS to find all reverse dependencies (transitive)
	visited := make(map[string]bool)
	queue := make([]string, 0, len(targets))
	for _, t := range targets {
		queue = append(queue, t)
		visited[t] = true
	}
	var result []string
	for len(queue) > 0 {
		pkg := queue[0]
		queue = queue[1:]
		node, ok := graph[pkg]
		if !ok {
			continue
		}
		for _, importer := range node.Imported {
			if !visited[importer] {
				visited[importer] = true
				result = append(result, importer)
				queue = append(queue, importer)
			}
		}
		if len(result) >= maxResults {
			break
		}
	}
	sort.Strings(result)
	return result
}

// findCycles detects import cycles using DFS-based cycle detection.
func findCycles(graph map[string]*pkgNode, maxResults int) [][]string {
	var cycles [][]string

	// Build adjacency list
	adj := make(map[string][]string)
	for pkg, node := range graph {
		adj[pkg] = node.Imports
	}

	const (
		white = 0 // unvisited
		gray  = 1 // in progress
		black = 2 // done
	)
	color := make(map[string]int)
	var path []string

	var dfs func(pkg string)
	dfs = func(pkg string) {
		if len(cycles) >= maxResults {
			return
		}
		color[pkg] = gray
		path = append(path, pkg)

		for _, dep := range adj[pkg] {
			if color[dep] == gray {
				// Found cycle: extract from path
				cycleStart := -1
				for i, p := range path {
					if p == dep {
						cycleStart = i
						break
					}
				}
				if cycleStart >= 0 {
					cycle := make([]string, len(path)-cycleStart)
					copy(cycle, path[cycleStart:])
					cycles = append(cycles, cycle)
				}
			} else if color[dep] == white {
				dfs(dep)
			}
		}

		path = path[:len(path)-1]
		color[pkg] = black
	}

	// Process packages in sorted order for deterministic results
	pkgs := make([]string, 0, len(graph))
	for pkg := range graph {
		pkgs = append(pkgs, pkg)
	}
	sort.Strings(pkgs)

	for _, pkg := range pkgs {
		if color[pkg] == white {
			dfs(pkg)
		}
		if len(cycles) >= maxResults {
			break
		}
	}

	return cycles
}

// findHotspots returns the most depended-upon packages (highest fan-in).
func findHotspots(graph map[string]*pkgNode, maxResults int) []pkgNode {
	nodes := make([]pkgNode, 0, len(graph))
	for _, n := range graph {
		nodes = append(nodes, *n)
	}
	// Sort by fan-in (imported_by count) descending
	sort.Slice(nodes, func(i, j int) bool {
		if len(nodes[i].Imported) != len(nodes[j].Imported) {
			return len(nodes[i].Imported) > len(nodes[j].Imported)
		}
		return nodes[i].Path < nodes[j].Path
	})
	if len(nodes) > maxResults {
		nodes = nodes[:maxResults]
	}
	return nodes
}

// formatDepGraphResult produces a human-readable summary.
func formatDepGraphResult(r depGraphResult) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Dependency Graph: %d packages, %d edges", r.Packages, r.Edges))
	if r.ModulePath != "" {
		sb.WriteString(fmt.Sprintf(" (module: %s)", r.ModulePath))
	}
	sb.WriteString("\n\n")

	switch r.Action {
	case "overview":
		sb.WriteString("Packages (showing import relationships):\n")
		for _, n := range r.Nodes {
			sb.WriteString(fmt.Sprintf("  %s [%d files, %d deps, %d dependents]\n",
				n.ShortName, n.FileCount, len(n.Imports), len(n.Imported)))
		}
		if r.Packages > len(r.Nodes) {
			sb.WriteString(fmt.Sprintf("  ... (%d more packages)\n", r.Packages-len(r.Nodes)))
		}

	case "reverse_deps":
		if r.Target != "" {
			sb.WriteString(fmt.Sprintf("Packages that depend on '%s':\n", r.Target))
		} else {
			sb.WriteString("Reverse dependencies:\n")
		}
		if len(r.ReverseDeps) == 0 {
			sb.WriteString("  (none found)\n")
		} else {
			for _, dep := range r.ReverseDeps {
				sb.WriteString(fmt.Sprintf("  %s\n", dep))
			}
		}

	case "cycles":
		if len(r.Cycles) == 0 {
			sb.WriteString("No import cycles detected.\n")
		} else {
			sb.WriteString(fmt.Sprintf("Import cycles found (%d):\n", len(r.Cycles)))
			for i, cycle := range r.Cycles {
				sb.WriteString(fmt.Sprintf("  Cycle %d: %s\n", i+1, strings.Join(cycle, " -> ")))
			}
		}

	case "hotspots":
		sb.WriteString("Most depended-upon packages (fan-in ranking):\n")
		for i, n := range r.Hotspots {
			if len(n.Imported) == 0 {
				break
			}
			sb.WriteString(fmt.Sprintf("  %d. %s - %d dependents\n", i+1, n.ShortName, len(n.Imported)))
		}
	}

	return sb.String()
}
