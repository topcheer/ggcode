package agent

// Copylock Detection in Go Code (Check #54)
//
// Problem: AI coding agents frequently produce Go code that passes sync types
// (sync.Mutex, sync.RWMutex, sync.WaitGroup, sync.Cond, sync.Once, sync.Map,
// sync.Pool) by VALUE instead of by pointer. Go's sync primitives contain
// internal state (mutex fields, counters, flags) that MUST NOT be copied.
// Copying a sync type silently breaks locking semantics:
//   - Two separate copies of a Mutex protect different state -- no mutual exclusion.
//   - Copying a WaitGroup loses the counter, causing Add/Done/Wait mismatches.
//   - Copying a sync.Once can cause initialization to run twice.
//
// `go vet -copylocks` catches this post-build, but no AI coding agent detects
// it at WRITE time. This check provides immediate, zero-dependency, zero-LLM-cost
// detection using Go's standard AST parser.
//
// Competitor analysis:
//   - Claude Code: no write-time detection (relies on go vet)
//   - Cursor: no detection (go vet may catch post-save, inconsistent)
//   - Cline/OpenHands: no detection
//   - Aider: no detection
//   - Windsurf: no detection
//   - GitHub Copilot: no detection
//
// Approach: AST-based analysis. For each function, find:
//  1. Function parameters whose type is a sync type (value, not pointer).
//  2. Function return types that are sync types (value, not pointer).
//  3. Receiver types that are sync types (value receiver on a struct embedding sync.Mutex).
//  4. Assignments and function call arguments that copy a sync type.
//
// Only NEW occurrences introduced by this edit are flagged (delta-aware).

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

// maxCopylockWarnings limits warnings per write to avoid noise.
const maxCopylockWarnings = 4

// syncTypes are Go standard library types that contain internal state that
// MUST NOT be copied. Passing them by value silently breaks their semantics.
// These mirror go vet's copylocks analyzer.
var syncTypes = map[string]bool{
	"sync.Mutex":     true,
	"sync.RWMutex":   true,
	"sync.WaitGroup": true,
	"sync.Cond":      true,
	"sync.Once":      true,
	"sync.Map":       true,
	"sync.Pool":      true,
}

// copylockIssue represents a detected copylock violation.
type copylockIssue struct {
	pos     token.Position
	message string
}

// checkCopylock detects sync types passed/returned by value in Go source.
// Returns warnings for value-copy violations.
func checkCopylock(filePath, oldContent, newContent string) []string {
	if filepath.Ext(filePath) != ".go" {
		return nil
	}
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filePath, newContent, 0)
	if err != nil || file == nil {
		return nil
	}

	issues := findCopylockIssues(fset, file)
	if len(issues) == 0 {
		return nil
	}

	// Delta-aware: only flag issues at positions not present in old content.
	oldPositions := collectOldCopylockPositions(filePath, oldContent)

	// First pass: count total new issues (excluding pre-existing ones)
	var totalNewIssues int
	for _, issue := range issues {
		if !oldPositions[issue.pos.Line] {
			totalNewIssues++
		}
	}

	// Second pass: build warnings list with truncation
	var warnings []string
	for _, issue := range issues {
		if oldPositions[issue.pos.Line] {
			continue
		}
		msg := fmt.Sprintf("Copylock: %s at %s. ", issue.message, issue.pos)
		msg += "Copying a sync type by value breaks its internal state (locks become "
		msg += "independent, WaitGroup counters reset, sync.Once may run twice). "
		msg += "Pass/return it by pointer (*sync.Type) instead."
		warnings = append(warnings, msg)
		if len(warnings) >= maxCopylockWarnings {
			warnings = append(warnings, fmt.Sprintf("...and %d more copylock issue(s)", totalNewIssues-len(warnings)))
			break
		}
	}

	return warnings
}

// collectOldCopylockPositions parses old content and returns a set of line
// numbers where copylock issues existed. Used for delta-aware filtering.
func collectOldCopylockPositions(filePath, oldContent string) map[int]bool {
	if strings.TrimSpace(oldContent) == "" {
		return nil
	}
	oldFset := token.NewFileSet()
	oldFile, err := parser.ParseFile(oldFset, filePath, oldContent, 0)
	if err != nil || oldFile == nil {
		return nil
	}
	oldIssues := findCopylockIssues(oldFset, oldFile)
	if len(oldIssues) == 0 {
		return nil
	}
	result := make(map[int]bool, len(oldIssues))
	for _, issue := range oldIssues {
		result[issue.pos.Line] = true
	}
	return result
}

// findCopylockIssues walks the AST and finds all copylock violations.
func findCopylockIssues(fset *token.FileSet, file *ast.File) []copylockIssue {
	var issues []copylockIssue

	// First pass: collect named struct types that contain sync fields.
	lockStructs := findLockContainingStructs(file)

	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok {
			issues = append(issues, checkFuncDeclCopylocks(fset, fn, lockStructs)...)
		}
	}

	return issues
}

// findLockContainingStructs scans type declarations for structs that embed or
// contain a sync field by value. Returns a set of type names that are unsafe
// to copy (pass/return/receive by value).
func findLockContainingStructs(file *ast.File) map[string]bool {
	result := make(map[string]bool)

	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok || ts.Name == nil {
				continue
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok || st.Fields == nil {
				continue
			}
			if structContainsSync(st) {
				result[ts.Name.Name] = true
			}
		}
	}

	return result
}

// structContainsSync returns true if the struct has at least one field whose
// type is a bare (non-pointer) sync type.
func structContainsSync(st *ast.StructType) bool {
	for _, field := range st.Fields.List {
		if identifySyncValueType(field.Type) != "" {
			return true
		}
	}
	return false
}

// checkFuncDeclCopylocks inspects a function declaration for copylock violations
// in parameters, return values, and receiver.
func checkFuncDeclCopylocks(fset *token.FileSet, fn *ast.FuncDecl, lockStructs map[string]bool) []copylockIssue {
	if fn == nil {
		return nil
	}
	var issues []copylockIssue

	// Check value receiver (e.g., func (s Server) Lock() - if Server embeds sync.Mutex,
	// a value receiver copies it).
	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		for _, field := range fn.Recv.List {
			if typeName := syncOrLockStruct(field.Type, lockStructs); typeName != "" {
				pos := fset.Position(field.Pos())
				issues = append(issues, copylockIssue{
					pos:     pos,
					message: fmt.Sprintf("value receiver of type %s (contains sync field)", typeName),
				})
			}
		}
	}

	// Check parameters.
	if fn.Type != nil && fn.Type.Params != nil {
		for _, field := range fn.Type.Params.List {
			if typeName := syncOrLockStruct(field.Type, lockStructs); typeName != "" {
				pos := fset.Position(field.Pos())
				names := fieldNames(field)
				issues = append(issues, copylockIssue{
					pos:     pos,
					message: fmt.Sprintf("value parameter %s of type %s", names, typeName),
				})
			}
		}
	}

	// Check return types.
	if fn.Type != nil && fn.Type.Results != nil {
		for _, field := range fn.Type.Results.List {
			if typeName := syncOrLockStruct(field.Type, lockStructs); typeName != "" {
				pos := fset.Position(field.Pos())
				issues = append(issues, copylockIssue{
					pos:     pos,
					message: fmt.Sprintf("value return of type %s", typeName),
				})
			}
		}
	}

	return issues
}

// syncOrLockStruct checks if the type is a direct sync type OR a named struct
// known to contain sync fields. Returns the type name if unsafe to copy, "" otherwise.
func syncOrLockStruct(expr ast.Expr, lockStructs map[string]bool) string {
	if name := identifySyncValueType(expr); name != "" {
		return name
	}
	if ident, ok := expr.(*ast.Ident); ok && lockStructs[ident.Name] {
		return ident.Name
	}
	return ""
}

// identifySyncValueType returns the sync type name if the given expression
// is a bare (non-pointer) reference to a sync type. Returns "" otherwise.
func identifySyncValueType(expr ast.Expr) string {
	// Selector expression: e.g., "sync.Mutex"
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	pkgIdent, ok := sel.X.(*ast.Ident)
	if !ok {
		return ""
	}
	fullName := pkgIdent.Name + "." + sel.Sel.Name
	if syncTypes[fullName] {
		return fullName
	}
	return ""
}

// fieldNames returns a comma-separated string of field names.
func fieldNames(field *ast.Field) string {
	if len(field.Names) == 0 {
		return "_"
	}
	parts := make([]string, len(field.Names))
	for i, name := range field.Names {
		parts[i] = name.Name
	}
	return strings.Join(parts, ", ")
}
