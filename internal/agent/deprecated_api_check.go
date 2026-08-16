package agent

// Deprecated API Detection in Go Code
//
// Problem: AI coding agents trained on historical data frequently suggest
// deprecated APIs that have been superseded by better alternatives. In Go,
// the standard library has deprecated many packages and functions over the
// years. LLMs continue to recommend them because they appeared in training
// data for years before deprecation.
//
// Key deprecated APIs in Go:
//   - io/ioutil (entire package deprecated in Go 1.16, replaced by io and os)
//   - rand.Seed (deprecated in Go 1.20, automatically seeded now)
//   - os.SEEK_SET/CUR/END (deprecated, use io.SeekStart/Current/End)
//   - strings.Title (deprecated in Go 1.18, use golang.org/x/text/cases)
//   - bytes.Title (deprecated in Go 1.18)
//   - crypto/subtle.ConstantTimeCompare with bytes.Equal (various deprecations)
//   - net.Error.Temporary (deprecated)
//   - flag.BoolVar with pointer (deprecated patterns)
//   - reflect.SliceHeader (deprecated, unsafe)
//   - reflect.StringHeader (deprecated, unsafe)
//   - sort.Slice with interface{} (not deprecated but often misused)
//
// Competitor analysis:
//   - Claude Code: no automatic detection (relies on staticcheck which flags some)
//   - Cursor: gopls may underline deprecated calls with strikethrough
//   - Cline/OpenHands: reactive only -- caught by linters post-hoc
//   - Aider: no automatic detection
//   - GitHub Copilot: gopls integration may show deprecation warnings
//
// staticcheck flags some (SA1019 for deprecated), but:
//   1. Not all agents run staticcheck
//   2. This catches the issue at WRITE TIME, before the build cycle
//   3. Provides actionable migration guidance inline
//
// Approach: AST-based + text scanning. Scans for deprecated package imports
// and function calls, then provides replacement guidance. Delta-aware: only
// flags patterns newly introduced by this edit.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

// deprecatedAPIInstance represents a detected deprecated API usage.
type deprecatedAPIInstance struct {
	posStr      string // human-readable position
	identifier  string // the deprecated identifier (e.g., "ioutil.ReadFile")
	replacement string // recommended modern replacement
	severity    string // "warning" or "info"
}

// deprecatedAPIRule defines a deprecated API pattern and its replacement.
type deprecatedAPIRule struct {
	// match identifies the deprecated API in import or call expression.
	// For import-based: the package path (e.g., "io/ioutil")
	// For selector-based: the package.function pattern (e.g., "rand.Seed")
	kind        string // "import" or "selector"
	pkg         string // package name or path
	name        string // function/type name (empty for whole-package import)
	replacement string // what to use instead
	explanation string // why it was deprecated
}

// deprecatedRules lists common deprecated Go APIs that LLMs frequently suggest.
var deprecatedRules = []deprecatedAPIRule{
	// io/ioutil - entire package deprecated in Go 1.16
	{kind: "import", pkg: "io/ioutil", replacement: "io and os packages",
		explanation: "The entire io/ioutil package was deprecated in Go 1.16. Use io.ReadAll, io.Copy, os.ReadFile, os.WriteFile, os.MkdirTemp, os.CreateTemp instead."},
	// Specific ioutil functions (for richer messaging)
	{kind: "selector", pkg: "ioutil", name: "ReadFile", replacement: "os.ReadFile",
		explanation: "ioutil.ReadFile was deprecated in Go 1.16. Use os.ReadFile instead."},
	{kind: "selector", pkg: "ioutil", name: "WriteFile", replacement: "os.WriteFile",
		explanation: "ioutil.WriteFile was deprecated in Go 1.16. Use os.WriteFile instead."},
	{kind: "selector", pkg: "ioutil", name: "ReadAll", replacement: "io.ReadAll",
		explanation: "ioutil.ReadAll was deprecated in Go 1.16. Use io.ReadAll instead."},
	{kind: "selector", pkg: "ioutil", name: "ReadDir", replacement: "os.ReadDir",
		explanation: "ioutil.ReadDir was deprecated in Go 1.16. Use os.ReadDir instead (returns []os.DirEntry, not []os.FileInfo)."},
	{kind: "selector", pkg: "ioutil", name: "TempFile", replacement: "os.CreateTemp",
		explanation: "ioutil.TempFile was deprecated in Go 1.16. Use os.CreateTemp instead."},
	{kind: "selector", pkg: "ioutil", name: "TempDir", replacement: "os.MkdirTemp",
		explanation: "ioutil.TempDir was deprecated in Go 1.16. Use os.MkdirTemp instead."},
	{kind: "selector", pkg: "ioutil", name: "NopCloser", replacement: "io.NopCloser",
		explanation: "ioutil.NopCloser was deprecated in Go 1.16. Use io.NopCloser instead."},
	{kind: "selector", pkg: "ioutil", name: "Discard", replacement: "io.Discard",
		explanation: "ioutil.Discard was deprecated in Go 1.16. Use io.Discard instead."},

	// math/rand
	{kind: "selector", pkg: "rand", name: "Seed", replacement: "remove the call",
		explanation: "rand.Seed was deprecated in Go 1.20. The global generator is now automatically seeded. Simply remove the rand.Seed() call."},

	// strings.Title - deprecated in Go 1.18
	{kind: "selector", pkg: "strings", name: "Title", replacement: "golang.org/x/text/cases",
		explanation: "strings.Title was deprecated in Go 1.18. It does not handle Unicode word boundaries properly. Use golang.org/x/text/cases.Title instead."},

	// bytes.Title - deprecated in Go 1.18
	{kind: "selector", pkg: "bytes", name: "Title", replacement: "golang.org/x/text/cases",
		explanation: "bytes.Title was deprecated in Go 1.18. It does not handle Unicode word boundaries properly."},

	// os.SEEK_* constants
	{kind: "selector", pkg: "os", name: "SEEK_SET", replacement: "io.SeekStart",
		explanation: "os.SEEK_SET was deprecated. Use io.SeekStart instead."},
	{kind: "selector", pkg: "os", name: "SEEK_CUR", replacement: "io.SeekCurrent",
		explanation: "os.SEEK_CUR was deprecated. Use io.SeekCurrent instead."},
	{kind: "selector", pkg: "os", name: "SEEK_END", replacement: "io.SeekEnd",
		explanation: "os.SEEK_END was deprecated. Use io.SeekEnd instead."},
}

// checkDeprecatedAPI detects usage of deprecated Go standard library APIs.
// Delta-aware: only flags patterns newly introduced by this edit.
func checkDeprecatedAPI(filePath, oldContent, newContent string) string {
	if filepath.Ext(filePath) != ".go" || strings.TrimSpace(newContent) == "" {
		return ""
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filePath, newContent, parser.AllErrors)
	if err != nil {
		return ""
	}

	importAliases := collectImportAliases(file)

	instances := findDeprecatedImports(fset, file)
	instances = append(instances, findDeprecatedSelectors(fset, file, importAliases, instances)...)

	if len(instances) == 0 {
		return ""
	}

	// Delta check: suppress instances that existed in old content.
	newInstances := filterNewInstances(filePath, instances, oldContent)
	if len(newInstances) == 0 {
		return ""
	}

	return formatDeprecatedWarnings(newInstances)
}

// collectImportAliases builds a map of import alias -> import path.
func collectImportAliases(file *ast.File) map[string]string {
	aliases := make(map[string]string)
	for _, imp := range file.Imports {
		impPath := strings.Trim(imp.Path.Value, `"`)
		if imp.Name != nil {
			aliases[imp.Name.Name] = impPath
		} else {
			parts := strings.Split(impPath, "/")
			aliases[parts[len(parts)-1]] = impPath
		}
	}
	return aliases
}

// findDeprecatedImports scans import declarations for deprecated packages.
func findDeprecatedImports(fset *token.FileSet, file *ast.File) []deprecatedAPIInstance {
	var instances []deprecatedAPIInstance
	for _, imp := range file.Imports {
		impPath := strings.Trim(imp.Path.Value, `"`)
		for _, rule := range deprecatedRules {
			if rule.kind == "import" && impPath == rule.pkg {
				pos := fset.Position(imp.Pos())
				instances = append(instances, deprecatedAPIInstance{
					posStr:      fmt.Sprintf("%s:%d", filepath.Base(pos.Filename), pos.Line),
					identifier:  impPath,
					replacement: rule.replacement,
				})
			}
		}
	}
	return instances
}

// findDeprecatedSelectors scans selector expressions for deprecated function/type calls.
// alreadyFound is used to suppress duplicates from import-level detection.
func findDeprecatedSelectors(fset *token.FileSet, file *ast.File, importAliases map[string]string, alreadyFound []deprecatedAPIInstance) []deprecatedAPIInstance {
	// Build a quick-lookup set of identifiers already flagged at import level.
	flaggedSet := make(map[string]bool, len(alreadyFound))
	for _, inst := range alreadyFound {
		flaggedSet[inst.identifier] = true
		if strings.Contains(inst.identifier, "io/ioutil") {
			flaggedSet["io/ioutil"] = true
		}
	}

	var instances []deprecatedAPIInstance
	ast.Inspect(file, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		ident, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}

		impPath := importAliases[ident.Name]
		// #527 Bug B: resolve the qualifier through the import table so
		// aliased imports (import mrand "math/rand" + mrand.Seed(...)) map
		// back to the canonical package name before rule matching. The old
		// bare-name comparison (ident.Name != rule.pkg) let every function-
		// granularity selector rule be bypassed by an alias.
		canonicalPkg := ident.Name
		if impPath != "" {
			if segs := strings.Split(impPath, "/"); len(segs) > 0 {
				canonicalPkg = segs[len(segs)-1]
			}
		}
		fullName := canonicalPkg + "." + sel.Sel.Name

		for _, rule := range deprecatedRules {
			if rule.kind != "selector" || canonicalPkg != rule.pkg || sel.Sel.Name != rule.name {
				continue
			}
			// Skip if already flagged by import-level check.
			if flaggedSet[fullName] {
				continue
			}
			if impPath == "io/ioutil" && flaggedSet["io/ioutil"] {
				continue
			}
			pos := fset.Position(sel.Pos())
			instances = append(instances, deprecatedAPIInstance{
				posStr:      fmt.Sprintf("%s:%d", filepath.Base(pos.Filename), pos.Line),
				identifier:  fullName,
				replacement: rule.replacement,
			})
		}
		return true
	})
	return instances
}

// filterNewInstances returns only instances not present in old content
// (delta-aware, #527 Bug A: per-identifier multiset delta). Occurrences are
// counted via the AST on BOTH sides, so a textual mention in the old content
// — a TODO comment like "// TODO: remove strings.Title usage" or a string
// literal — no longer suppresses a newly introduced real call, while N
// pre-existing real calls suppress exactly N new ones. The old
// position-blind strings.Contains delta permanently swallowed identifiers
// that were merely mentioned anywhere in the old file.
func filterNewInstances(filePath string, instances []deprecatedAPIInstance, oldContent string) []deprecatedAPIInstance {
	remaining := countDeprecatedInstances(filePath, oldContent)
	var result []deprecatedAPIInstance
	for _, inst := range instances {
		if remaining[inst.identifier] > 0 {
			remaining[inst.identifier]--
			continue
		}
		result = append(result, inst)
	}
	return result
}

// countDeprecatedInstances parses content (best-effort) and returns the
// multiset of deprecated identifiers it actually USES: identifier →
// occurrence count. Comment mentions and string literals never parse as
// calls, so they count as zero (#527 Bug A). Unparseable content (empty or
// mid-edit broken old state) also yields zero — the delta only suppresses
// verified old usages.
func countDeprecatedInstances(filePath, content string) map[string]int {
	counts := make(map[string]int)
	if strings.TrimSpace(content) == "" {
		return counts
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filePath, content, parser.AllErrors)
	if err != nil {
		return counts
	}
	aliases := collectImportAliases(file)
	instances := findDeprecatedImports(fset, file)
	instances = append(instances, findDeprecatedSelectors(fset, file, aliases, instances)...)
	for _, inst := range instances {
		counts[inst.identifier]++
	}
	return counts
}

// formatDeprecatedWarnings builds the warning string for detected instances.
func formatDeprecatedWarnings(instances []deprecatedAPIInstance) string {
	var b strings.Builder
	b.WriteString("[Deprecated API detection] The following use deprecated Go standard library APIs:\n")
	for _, inst := range instances {
		reason := findDeprecatedExplanation(inst.identifier)
		b.WriteString(fmt.Sprintf("  - %s: '%s' is deprecated. %s Use '%s' instead.\n",
			inst.posStr, inst.identifier, reason, inst.replacement))
	}
	return b.String()
}

// findDeprecatedExplanation looks up the explanation for a given identifier.
func findDeprecatedExplanation(identifier string) string {
	for _, rule := range deprecatedRules {
		if rule.kind == "import" && identifier == rule.pkg {
			return rule.explanation
		}
		if rule.kind == "selector" && identifier == rule.pkg+"."+rule.name {
			return rule.explanation
		}
	}
	return "This API is deprecated and may be removed in future Go versions."
}
