package agent

// Duplicate Declaration Detection — Post-edit structural duplicate checker.
//
// Research basis: AI coding agents frequently produce duplicate declarations
// when editing existing files:
//
//  1. Duplicate imports — the agent adds "import \"fmt\"" when it already exists.
//  2. Duplicate functions/methods — the agent re-implements a function that
//     already exists in the file instead of editing the existing one.
//  3. Duplicate type/struct declarations — the agent pastes a struct definition
//     that already exists, creating a redeclaration error.
//  4. Duplicate constants/variables — the agent adds a const/var block that
//     conflicts with an existing declaration.
//
// These are guaranteed compilation errors in most languages. The agent then
// wastes several iterations debugging "redeclared in this block" errors that
// could have been caught immediately at write time.
//
// Competitor analysis:
//   - Claude Code: relies on the build cycle to catch these (wastes iterations)
//   - Cursor: LSP diagnostics may catch them on save, but not inline
//   - Cline/OpenHands: reactive only — caught by build/test cycle
//   - Aider: commit-per-edit makes duplicates visible in diff review
//
// ggcode's approach: detect duplicate declarations at write time by comparing
// declaration counts in old vs. new content. Only NEW duplicates (introduced
// by this edit) are flagged. This is zero-LLM-cost, language-aware, and has
// near-zero false positives because we only flag exact name collisions.
//
// For Go files, we use the AST parser for precise detection. For other
// languages, we use regex-based heuristics for function/type/import patterns.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"strings"
)

// maxDuplicateWarnings caps the number of duplicate-declaration warnings per write.
const maxDuplicateWarnings = 3

// checkDuplicateDeclarations detects declarations that were duplicated by this
// edit — i.e., a function/type/import name that appears 2+ times in the new
// content but appeared 0 or 1 times in the old content.
//
// Returns a non-empty warning string if duplicates were introduced, "" otherwise.
func checkDuplicateDeclarations(filePath, oldContent, newContent string) string {
	if strings.TrimSpace(newContent) == "" {
		return ""
	}

	// Skip test files — Go test files can legitimately have duplicate helper
	// names across different test functions (not package-level duplicates, but
	// our regex heuristics for non-Go languages may false-positive in tests).
	if isTestFile(filePath) {
		return ""
	}

	ext := filepath.Ext(filePath)

	var dups []dupDecl

	switch ext {
	case ".go":
		dups = checkGoDuplicateDecls(filePath, oldContent, newContent)
	case ".py":
		dups = checkPythonDuplicateDecls(oldContent, newContent)
	case ".js", ".jsx", ".ts", ".tsx":
		dups = checkJSDuplicateDecls(oldContent, newContent, ext)
	default:
		return "" // unsupported language
	}

	if len(dups) == 0 {
		return ""
	}

	if len(dups) > maxDuplicateWarnings {
		dups = dups[:maxDuplicateWarnings]
	}

	var b strings.Builder
	for i, d := range dups {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(formatDuplicateWarning(d))
	}
	return b.String()
}

// dupDecl represents a detected duplicate declaration.
type dupDecl struct {
	kind  string // "function", "method", "type", "import", "const", "var"
	name  string // declaration name
	count int    // number of occurrences in new content
}

// formatDuplicateWarning renders a concise warning for a duplicate declaration.
func formatDuplicateWarning(d dupDecl) string {
	return fmt.Sprintf(
		"Duplicate %s %q detected (%d occurrences in file). "+
			"This will cause a compilation/runtime error — "+
			"remove the duplicate or merge the declarations.",
		d.kind, d.name, d.count)
}

// --- Go (AST-based) ---

// checkGoDuplicateDecls uses go/parser to detect duplicate package-level
// declarations introduced by this edit. It compares declaration name counts
// between old and new content.
func checkGoDuplicateDecls(filePath, oldContent, newContent string) []dupDecl {
	oldDecls := collectGoDecls(filePath, oldContent)
	newDecls := collectGoDecls(filePath, newContent)

	var dups []dupDecl
	seen := make(map[string]bool)

	for name, newCount := range newDecls {
		if newCount < 2 {
			continue
		}
		oldCount := oldDecls[name]
		// Only flag if the edit introduced or increased the duplication.
		// If oldCount >= newCount, the duplicates already existed (e.g. file
		// had 2 copies and now still has 2).
		if newCount <= oldCount {
			continue
		}
		// Only flag NEW duplicates (count increased from <2 to >=2).
		if oldCount >= 2 {
			continue
		}
		key := name.kind + ":" + name.name
		if seen[key] {
			continue
		}
		seen[key] = true
		dups = append(dups, dupDecl{
			kind:  name.kind,
			name:  name.name,
			count: newCount,
		})
	}

	return dups
}

// goDeclKey identifies a Go declaration by kind + name.
type goDeclKey struct {
	kind string
	name string
}

// collectGoDecls parses Go source and counts package-level declarations.
// Returns a map from declaration key to occurrence count.
func collectGoDecls(filePath, src string) map[goDeclKey]int {
	counts := make(map[goDeclKey]int)
	if strings.TrimSpace(src) == "" {
		return counts
	}

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filePath, src, 0)
	if err != nil {
		// If the file doesn't parse, we can't reliably detect duplicates.
		// Syntax errors are caught by checkWriteIntegrity; skip here.
		return counts
	}

	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Name == nil || d.Name.Name == "" {
				continue
			}
			// For methods (d.Recv != nil), key includes receiver type to
			// distinguish methods on different types with the same name.
			if d.Recv != nil && len(d.Recv.List) > 0 {
				recvType := receiverTypeName(d.Recv.List[0].Type)
				if recvType != "" {
					counts[goDeclKey{"method", recvType + "." + d.Name.Name}]++
					continue
				}
			}
			counts[goDeclKey{"function", d.Name.Name}]++
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					if s.Name != nil && s.Name.Name != "" {
						counts[goDeclKey{"type", s.Name.Name}]++
					}
				case *ast.ValueSpec:
					for _, name := range s.Names {
						if name.Name != "" && name.Name != "_" {
							kind := "var"
							if d.Tok == token.CONST {
								kind = "const"
							}
							counts[goDeclKey{kind, name.Name}]++
						}
					}
				}
			}
		}
	}

	return counts
}

// --- Python (regex-based) ---

// pythonFuncRe matches top-level and class method function definitions.
// Group 1 captures leading indentation so methods can be scoped to their
// enclosing class (#753); group 3 is the function name.
var pythonFuncRe = regexp.MustCompile(`(?m)^(\s*)(async\s+)?def\s+(\w+)\s*\(`)

// pythonClassRe matches class definitions.
var pythonClassRe = regexp.MustCompile(`(?m)^class\s+(\w+)\s*[\(:]`)

// checkPythonDuplicateDecls detects duplicate function/class definitions.
// Methods are scoped to their class (#753): same-name methods in different
// classes are legal Python idiom (__init__, run, process...) and must not
// count as duplicates of each other -- mirroring the Go path's method:Recv
// scoping, which the Python path lacked.
func checkPythonDuplicateDecls(oldContent, newContent string) []dupDecl {
	return compareDeclCounts(
		collectPythonDecls(oldContent),
		collectPythonDecls(newContent),
	)
}

// collectPythonDecls counts Python declarations with class scoping for
// methods (#753). Top-level def -> function:<name>; indented def under the
// most recent class line -> method:<Class>.<name>; class -> class:<name>.
// An indented def before any class line falls back to function:<name>
// (preserving pre-#753 behavior for module-level nested defs).
func collectPythonDecls(src string) map[regexDeclKey]int {
	counts := make(map[regexDeclKey]int)
	if strings.TrimSpace(src) == "" {
		return counts
	}
	currentClass := ""
	for _, raw := range strings.Split(src, "\n") {
		line := strings.TrimRight(raw, "\r")
		if cm := pythonClassRe.FindStringSubmatch(line); cm != nil {
			currentClass = cm[1]
			counts[regexDeclKey{"class", cm[1]}]++
			continue
		}
		fm := pythonFuncRe.FindStringSubmatch(line)
		if fm == nil {
			continue
		}
		name := fm[3]
		if fm[1] != "" && currentClass != "" {
			counts[regexDeclKey{"method", currentClass + "." + name}]++
		} else {
			counts[regexDeclKey{"function", name}]++
		}
	}
	return counts
}

// --- JavaScript/TypeScript (regex-based) ---

// jsFuncRe matches function declarations: "function foo(", "function foo (".
var jsFuncRe = regexp.MustCompile(`(?m)^\s*(?:export\s+)?(?:default\s+)?(?:async\s+)?function\s+(\w+)\s*\(`)

// jsClassRe matches class declarations.
var jsClassRe = regexp.MustCompile(`(?m)^\s*(?:export\s+)?(?:default\s+)?(?:abstract\s+)?class\s+(\w+)\s*[\{<]`)

// jsConstFuncRe matches "const foo = (" arrow function declarations.
var jsConstFuncRe = regexp.MustCompile(`(?m)^\s*(?:export\s+)?const\s+(\w+)\s*=\s*(?:async\s*)?\(?`)

// checkJSDuplicateDecls detects duplicate function/class/const declarations.
func checkJSDuplicateDecls(oldContent, newContent, ext string) []dupDecl {
	patterns := []regexDeclPattern{
		{re: jsFuncRe, kind: "function", group: 1},
		{re: jsClassRe, kind: "class", group: 1},
		{re: jsConstFuncRe, kind: "const", group: 1},
	}
	return checkRegexDuplicates(oldContent, newContent, patterns)
}

// --- Regex-based engine ---

// regexDeclPattern defines a regex pattern for extracting declarations.
type regexDeclPattern struct {
	re    *regexp.Regexp
	kind  string
	group int // capture group index for the name
}

// checkRegexDuplicates is a generic engine that counts declaration names
// from old and new content using regex patterns, then flags NEW duplicates.
func checkRegexDuplicates(oldContent, newContent string, patterns []regexDeclPattern) []dupDecl {
	return compareDeclCounts(
		collectRegexDecls(oldContent, patterns),
		collectRegexDecls(newContent, patterns),
	)
}

// compareDeclCounts flags names whose new count reached 2+ while the old
// count was below 2 (pre-existing duplicates are not re-reported).
func compareDeclCounts(oldCounts, newCounts map[regexDeclKey]int) []dupDecl {
	var dups []dupDecl
	seen := make(map[string]bool)

	for key, newCount := range newCounts {
		if newCount < 2 {
			continue
		}
		oldCount := oldCounts[key]
		if newCount <= oldCount {
			continue
		}
		if oldCount >= 2 {
			continue
		}
		mapKey := key.kind + ":" + key.name
		if seen[mapKey] {
			continue
		}
		seen[mapKey] = true
		dups = append(dups, dupDecl{
			kind:  key.kind,
			name:  key.name,
			count: newCount,
		})
	}

	return dups
}

// regexDeclKey identifies a declaration by kind + name (for regex-based detection).
type regexDeclKey struct {
	kind string
	name string
}

// collectRegexDecls extracts and counts declaration names from source using patterns.
func collectRegexDecls(src string, patterns []regexDeclPattern) map[regexDeclKey]int {
	counts := make(map[regexDeclKey]int)
	if strings.TrimSpace(src) == "" {
		return counts
	}
	for _, p := range patterns {
		matches := p.re.FindAllStringSubmatch(src, -1)
		for _, m := range matches {
			if p.group < len(m) && m[p.group] != "" {
				counts[regexDeclKey{p.kind, m[p.group]}]++
			}
		}
	}
	return counts
}
