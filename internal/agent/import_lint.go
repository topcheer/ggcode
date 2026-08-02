package agent

// Proactive import analysis for Go files at write time.
//
// Research basis: AI coding agents (Claude Code, Cursor, Cline, Aider) frequently
// introduce import-related build failures:
//   - Removing all uses of a package but leaving the import declaration →
//     "imported and not used"
//   - Adding code that references a package without importing it → "undefined: pkg.X"
//
// These are the #1 and #2 most common build-failure categories after syntax errors.
// Each one wastes a full build-test-fix iteration cycle.
//
// Competitive landscape:
//   - Cursor: Auto-imports on completion + "Add all missing imports" command
//   - Aider: Runs goimports automatically after edits
//   - Claude Code: Relies on LSP diagnostics (requires running language server)
//   - Cline/OpenHands: Reactive only - catches import errors after build fails
//
// ggcode's approach: zero-cost AST-based analysis that runs synchronously after
// each Go file write/edit, catching unused imports and suggesting missing stdlib
// imports BEFORE the agent runs a build. Uses go/ast from the standard library
// (no external dependencies, <1ms per file).
//
// Design decisions:
//   - Only runs on .go files (highest value - Go has strict import rules)
//   - Skips files with syntax errors (already caught by checkGoSyntax)
//   - Unused import detection is near-zero false-positive: it checks whether the
//     package identifier appears anywhere outside the import block
//   - Missing import detection uses a curated stdlib map to avoid false positives

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// maxImportWarnings caps the number of import warnings per write.
const maxImportWarnings = 4

// goImportInfo describes a single import declaration.
type goImportInfo struct {
	name string // effective name: alias if present, else last path component
	path string // full import path (e.g. "encoding/json")
}

// commonGoStdlib maps short package identifiers to their full import paths.
// Used for missing-import suggestions. Only includes packages whose short name
// is unlikely to be a user-defined variable or type (to minimize false positives).
var commonGoStdlib = map[string]string{
	"fmt":       "fmt",
	"os":        "os",
	"strings":   "strings",
	"strconv":   "strconv",
	"time":      "time",
	"errors":    "errors",
	"context":   "context",
	"sync":      "sync",
	"io":        "io",
	"bytes":     "bytes",
	"bufio":     "bufio",
	"regexp":    "regexp",
	"sort":      "sort",
	"math":      "math",
	"path":      "path",
	"filepath":  "path/filepath",
	"reflect":   "reflect",
	"unicode":   "unicode",
	"hash":      "hash",
	"image":     "image",
	"crypto":    "crypto",
	"json":      "encoding/json",
	"xml":       "encoding/xml",
	"base64":    "encoding/base64",
	"hex":       "encoding/hex",
	"binary":    "encoding/binary",
	"http":      "net/http",
	"url":       "net/url",
	"smtp":      "net/smtp",
	"mail":      "net/mail",
	"flag":      "flag",
	"log":       "log",
	"logf":      "log",
	"slog":      "log/slog",
	"syscall":   "syscall",
	"signal":    "os/signal",
	"exec":      "os/exec",
	"user":      "os/user",
	"env":       "os",
	"text":      "text/template",
	"template":  "html/template",
	"html":      "html",
	"tar":       "archive/tar",
	"zip":       "archive/zip",
	"sql":       "database/sql",
	"driver":    "database/sql/driver",
	"redis":     "", // not stdlib - skip
	"testing":   "testing",
	"tabwriter": "text/tabwriter",
	"scanner":   "text/scanner",
	"rand":      "math/rand",
	"big":       "math/big",
	"cmplx":     "math/cmplx",
	"bits":      "math/bits",
	"sha256":    "crypto/sha256",
	"sha1":      "crypto/sha1",
	"md5":       "crypto/md5",
	"hmac":      "crypto/hmac",
	"aes":       "crypto/aes",
	"cipher":    "crypto/cipher",
	"des":       "crypto/des",
	"tls":       "crypto/tls",
	"x509":      "crypto/x509",
	"ssh":       "golang.org/x/crypto/ssh", // popular non-stdlib
	"pbkdf2":    "golang.org/x/crypto/pbkdf2",
	"bcrypt":    "golang.org/x/crypto/bcrypt",
	"yaml":      "gopkg.in/yaml.v3",
	"uuid":      "github.com/google/uuid",
}

// goModCacheTTL limits how often we re-read go.mod for the same directory.
// go.mod changes are infrequent during a session; caching avoids redundant I/O
// on every file write.
const goModCacheTTL = 30 * time.Second

type goModCacheEntry struct {
	importMap map[string]string
	modTime   time.Time
	loadedAt  time.Time
}

var (
	goModCacheMu    sync.RWMutex
	goModCacheStore = make(map[string]goModCacheEntry)
)

// goModRequirePattern matches lines like:
//
//	github.com/google/uuid v1.6.0
//	golang.org/x/crypto v0.20.0 // indirect
//
// It captures the module path (everything before the version).
var goModRequirePattern = regexp.MustCompile(`^\s*([a-zA-Z0-9._\-/]+)\s+v[0-9]`)

// loadGoModImports reads go.mod from the working directory (or nearest parent)
// and builds a map of package short-name to import path for third-party modules.
// This enables missing-import detection beyond the curated stdlib map.
//
// For example, if go.mod contains `require github.com/charmbracelet/lipgloss v2.x`,
// this returns {"lipgloss": "github.com/charmbracelet/lipgloss/v2"} so that
// code referencing lipgloss.New() without the import is detected.
//
// The result is cached for goModCacheTTL to avoid repeated file I/O.
func loadGoModImports(workingDir string) map[string]string {
	if workingDir == "" {
		return nil
	}

	goModCacheMu.RLock()
	if entry, ok := goModCacheStore[workingDir]; ok && time.Since(entry.loadedAt) < goModCacheTTL {
		goModCacheMu.RUnlock()
		return entry.importMap
	}
	goModCacheMu.RUnlock()

	goModPath := findGoMod(workingDir)
	if goModPath == "" {
		goModCacheMu.Lock()
		goModCacheStore[workingDir] = goModCacheEntry{importMap: nil, loadedAt: time.Now()}
		goModCacheMu.Unlock()
		return nil
	}

	info, err := os.Stat(goModPath)
	if err != nil {
		return nil
	}

	goModCacheMu.RLock()
	if entry, ok := goModCacheStore[workingDir]; ok && !info.ModTime().After(entry.modTime) {
		goModCacheMu.RUnlock()
		return entry.importMap
	}
	goModCacheMu.RUnlock()

	data, err := os.ReadFile(goModPath)
	if err != nil {
		return nil
	}

	result := parseGoModRequires(string(data), goModPath)

	goModCacheMu.Lock()
	goModCacheStore[workingDir] = goModCacheEntry{
		importMap: result,
		modTime:   info.ModTime(),
		loadedAt:  time.Now(),
	}
	goModCacheMu.Unlock()

	return result
}

// findGoMod walks up from startDir to find the nearest go.mod file.
func findGoMod(startDir string) string {
	dir := startDir
	for i := 0; i < 20; i++ { // limit depth to avoid infinite loops
		p := filepath.Join(dir, "go.mod")
		if _, err := os.Stat(p); err == nil {
			return p
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

// parseGoModRequires extracts require directives from go.mod content and builds
// a package-name → import-path map. For versioned module paths (e.g.
// /v2, /v3), the path is kept as-is since the import path includes the version
// segment but the package name does not.
func parseGoModRequires(content, goModPath string) map[string]string {
	result := make(map[string]string)

	// Determine module path to identify internal vs. external imports.
	modulePath := ""
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			modulePath = strings.TrimSpace(strings.TrimPrefix(line, "module "))
			break
		}
	}

	inRequireBlock := false
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)

		// Detect start of require block or single-line require.
		// Check for "require (" first - "require (" would also match
		// HasPrefix(trimmed, "require ") since the paren is preceded by a space.
		if trimmed == "require (" {
			inRequireBlock = true
			continue
		}
		if strings.HasPrefix(trimmed, "require ") {
			// Single-line require: require path v1.0.0
			parseRequireLine(trimmed[len("require "):], modulePath, result)
			continue
		}
		if trimmed == ")" && inRequireBlock {
			inRequireBlock = false
			continue
		}
		if inRequireBlock {
			parseRequireLine(trimmed, modulePath, result)
		}
	}

	return result
}

// parseRequireLine processes a single require directive line and adds entries
// to the result map if the module path yields a usable package name.
func parseRequireLine(line, modulePath string, result map[string]string) {
	matches := goModRequirePattern.FindStringSubmatch(line)
	if len(matches) < 2 {
		return
	}
	modPath := matches[1]

	// Skip internal modules (within the same module path prefix).
	if modulePath != "" && strings.HasPrefix(modPath, modulePath) {
		return
	}

	// Skip indirect dependencies - they're less likely to be directly imported
	// by the code being edited, and including them would bloat the map with noise.
	if strings.Contains(line, "// indirect") {
		return
	}

	// Derive package name: last non-version segment of the module path.
	segments := strings.Split(modPath, "/")
	last := segments[len(segments)-1]
	if isVersionSegment(last) && len(segments) >= 2 {
		last = segments[len(segments)-2]
	}

	// Skip if we can't derive a meaningful package name.
	if last == "" || isVersionSegment(last) {
		return
	}

	// Don't overwrite stdlib entries - stdlib map takes priority.
	if _, isStdlib := commonGoStdlib[last]; isStdlib {
		return
	}

	// If two modules map to the same package name, keep the first (deterministic).
	// This is a rare edge case and either suggestion would be a valid import.
	if result[last] == "" {
		result[last] = modPath
	}
}

// checkGoImports analyzes Go source for unused and missing imports.
// Returns warning strings. Returns nil if the file has syntax errors
// (those are already caught by checkGoSyntax) or no import issues.
func checkGoImportsAST(filePath string, f *ast.File) []string {
	return checkGoImportsASTWithDir(filePath, f, "")
}

// checkGoImportsASTWithDir is like checkGoImportsAST but also uses the working
// directory's go.mod to detect missing third-party imports (e.g. lipgloss.New()
// without importing the lipgloss package). When workingDir is empty or no go.mod
// is found, behavior is identical to checkGoImportsAST.
func checkGoImportsASTWithDir(filePath string, f *ast.File, workingDir string) []string {
	if f == nil {
		return nil
	}

	// Collect imports.
	var imports []goImportInfo
	for _, imp := range f.Imports {
		if imp == nil || imp.Path == nil {
			continue
		}
		rawPath := strings.Trim(imp.Path.Value, `"`)
		name := ""
		if imp.Name != nil {
			name = imp.Name.Name
		} else {
			parts := strings.Split(rawPath, "/")
			name = parts[len(parts)-1]
		}
		// Blank (_) and dot (.) imports are always "used" - they have side effects.
		if name == "_" || name == "." {
			continue
		}
		// Versioned paths like "charm.land/lipgloss/v2" have a last segment "v2"
		// that does NOT match the actual package name (e.g. "lipgloss"). Static
		// analysis can't reliably resolve the real package name without loading
		// the package, so skip unused-import detection for these to avoid
		// false positives.
		if isVersionSegment(name) {
			continue
		}
		imports = append(imports, goImportInfo{name: name, path: rawPath})
	}

	if len(imports) == 0 && !hasGoCodeDecls(f) {
		// No imports and no code - nothing to analyze.
		return nil
	}

	var warnings []string

	// 1. Unused import detection.
	// Collect all identifiers used outside the import block.
	usedIdents := make(map[string]bool)
	for _, decl := range f.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if ok && genDecl.Tok == token.IMPORT {
			continue // skip import declarations
		}
		ast.Inspect(decl, func(n ast.Node) bool {
			if ident, ok := n.(*ast.Ident); ok {
				usedIdents[ident.Name] = true
			}
			return true
		})
	}

	importNameSet := make(map[string]bool)
	for _, imp := range imports {
		importNameSet[imp.name] = true
		if !usedIdents[imp.name] {
			warnings = append(warnings, fmt.Sprintf(
				"Unused import: %q (%s) is imported but never referenced - "+
					"remove it or the build will fail with \"imported and not used\".",
				imp.name, imp.path))
		}
	}

	// 2. Missing import detection.
	// Find package-qualified references (X.Sel where X is a simple Ident)
	// where X matches a known stdlib package that isn't imported.
	seenMissing := make(map[string]bool) // dedup
	for _, decl := range f.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if ok && genDecl.Tok == token.IMPORT {
			continue
		}
		ast.Inspect(decl, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			ident, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			pkgName := ident.Name
			// Skip if already imported or if it's a package-level identifier.
			if importNameSet[pkgName] {
				return true
			}
			// Skip identifiers starting with lowercase that aren't known packages
			// (likely local variables, receivers, struct fields).
			suggestedPath, known := commonGoStdlib[pkgName]
			if !known {
				// Check project's go.mod for third-party module imports.
				if workingDir != "" {
					if modPath := loadGoModImports(workingDir); modPath != nil {
						if p, ok := modPath[pkgName]; ok && p != "" {
							suggestedPath = p
							known = true
						}
					}
				}
			}
			if !known || suggestedPath == "" {
				return true
			}
			if seenMissing[pkgName] {
				return true
			}
			seenMissing[pkgName] = true
			warnings = append(warnings, fmt.Sprintf(
				"Likely missing import: %q.%s references package %q but it is not imported. "+
					"Add `import %s %q` or the build will fail with \"undefined: %s\".",
				pkgName, sel.Sel.Name, suggestedPath, pkgName, suggestedPath, pkgName))
			return true
		})
	}

	if len(warnings) > maxImportWarnings {
		warnings = warnings[:maxImportWarnings]
	}

	return warnings
}

// hasGoCodeDecls returns true if the file has at least one non-import declaration.
func hasGoCodeDecls(f *ast.File) bool {
	for _, decl := range f.Decls {
		if _, ok := decl.(*ast.GenDecl); ok {
			continue // could be import, var, const, type
		}
		if _, ok := decl.(*ast.FuncDecl); ok {
			return true
		}
	}
	return false
}

// checkGoImports is a convenience wrapper that parses src then calls
// checkGoImportsAST. Used by tests and as a standalone entry point.
func checkGoImports(filePath, src string) []string {
	return checkGoImportsWithDir(filePath, src, "")
}

// checkGoImportsWithDir is like checkGoImports but also leverages the project's
// go.mod for third-party import detection. The workingDir should be the agent's
// working directory.
func checkGoImportsWithDir(filePath, src, workingDir string) []string {
	if strings.TrimSpace(src) == "" {
		return nil
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filePath, src, 0)
	if err != nil {
		return nil
	}
	return checkGoImportsASTWithDir(filePath, f, workingDir)
}

// isVersionSegment reports whether s looks like a Go module version path
// segment (e.g. "v2", "v3"). These segments do NOT correspond to the actual
// package name and should be excluded from unused-import detection.
func isVersionSegment(s string) bool {
	if len(s) < 2 || s[0] != 'v' {
		return false
	}
	for _, c := range s[1:] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
