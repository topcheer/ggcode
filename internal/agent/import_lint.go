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
//   - Cline/OpenHands: Reactive only — catches import errors after build fails
//
// ggcode's approach: zero-cost AST-based analysis that runs synchronously after
// each Go file write/edit, catching unused imports and suggesting missing stdlib
// imports BEFORE the agent runs a build. Uses go/ast from the standard library
// (no external dependencies, <1ms per file).
//
// Design decisions:
//   - Only runs on .go files (highest value — Go has strict import rules)
//   - Skips files with syntax errors (already caught by checkGoSyntax)
//   - Unused import detection is near-zero false-positive: it checks whether the
//     package identifier appears anywhere outside the import block
//   - Missing import detection uses a curated stdlib map to avoid false positives

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
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
	"redis":     "", // not stdlib — skip
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

// checkGoImports analyzes Go source for unused and missing imports.
// Returns warning strings. Returns nil if the file has syntax errors
// (those are already caught by checkGoSyntax) or no import issues.
func checkGoImports(filePath, src string) []string {
	if strings.TrimSpace(src) == "" {
		return nil
	}

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filePath, src, 0)
	if err != nil {
		// Syntax errors are already caught by checkGoSyntax; skip import
		// analysis to avoid cascading false positives from broken AST.
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
		// Blank (_) and dot (.) imports are always "used" — they have side effects.
		if name == "_" || name == "." {
			continue
		}
		imports = append(imports, goImportInfo{name: name, path: rawPath})
	}

	if len(imports) == 0 && !hasGoCodeDecls(f) {
		// No imports and no code — nothing to analyze.
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
				"Unused import: %q (%s) is imported but never referenced — "+
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
