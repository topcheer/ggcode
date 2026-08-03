package agent

// Ignored Error Return Detection in Go Code
//
// Problem: AI coding agents frequently produce Go code that calls functions
// returning an error value but completely ignores (discards) the error. This is
// the most fundamental error-handling anti-pattern in Go and is distinct from
// the patterns covered by error_swallow_check.go (which only detects
// if err != nil {} empty handlers and bare returns).
//
// The three patterns this check catches:
//
//  1. Completely ignored call: a function call whose last return type is error,
//     where the call appears as a standalone statement (not assigned to anything).
//     Example: `json.NewEncoder(w).Encode(data)` - the error from Encode is
//     silently dropped. If the writer fails, data is lost.
//
//  2. Explicitly discarded error: `_ = someFunc()` or `_, _ = someFunc()` where
//     the error return is assigned to the blank identifier. While sometimes
//     intentional, LLMs frequently do this as a shortcut when they're unsure
//     how to handle the error, introducing latent bugs.
//
//  3. Partial capture: `result := multiReturnFunc()` where the function returns
//     (T, error) but only one value is captured. In Go this is actually a compile
//     error for multi-return functions, so we focus on patterns 1 and 2.
//
// Pattern 1 is especially dangerous because:
//   - The code compiles and appears correct
//   - Errors from I/O (file, network), parsing (JSON, XML), and state mutations
//     are silently lost
//   - It's the #1 issue flagged by errcheck and staticcheck SA4006
//   - go vet does NOT catch this
//
// Competitor analysis:
//   - Claude Code: no automatic detection (relies on external linters)
//   - Cursor: lint-on-save may catch via errcheck, but not at write time
//   - Cline/OpenHands: reactive only - caught by tests or production incidents
//   - Aider: no automatic detection
//   - Windsurf: no automatic detection
//   - errcheck: catches this but requires installation and a separate lint cycle
//   - staticcheck SA4006: catches assignments that are never used, not bare calls
//
// None provide INLINE detection at write time. This check provides immediate,
// zero-dependency feedback using Go's standard library AST parser.
//
// Approach: AST-based analysis. For standalone call expressions (ExprStmt nodes),
// check if the called function is known to return an error. We maintain a curated
// set of common stdlib functions/methods known to return error. We also flag
// explicit `_ =` discard patterns. Delta-aware: only flags patterns newly
// introduced by this edit.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

// errorReturningFuncs maps fully-qualified function/method names that return an
// error as their last return value. This is a curated set of the most commonly
// ignored error-returning functions in the Go standard library.
//
// The key is "pkg.Func" or "pkg.Type.Method" format.
// This is intentionally focused on functions that LLMs frequently call without
// checking the error return.
var errorReturningFuncs = map[string]bool{
	// fmt package - Fprint/Fprintln/Fprintf errors are almost always ignored
	"fmt.Fprint":   true,
	"fmt.Fprintln": true,
	"fmt.Fprintf":  true,

	// encoding/json
	"json.Marshal":           true,
	"json.MarshalIndent":     true,
	"json.Unmarshal":         true,
	"json.NewEncoder.Encode": true,
	"json.NewDecoder.Decode": true,

	// encoding/xml
	"xml.Marshal":           true,
	"xml.Unmarshal":         true,
	"xml.NewEncoder.Encode": true,
	"xml.NewDecoder.Decode": true,

	// io.WriteString and similar
	"io.WriteString": true,

	// os package
	"os.WriteFile": true,
	"os.ReadFile":  true,
	"os.MkdirAll":  true,
	"os.Mkdir":     true,
	"os.Remove":    true,
	"os.RemoveAll": true,
	"os.Rename":    true,
	"os.Chmod":     true,
	"os.Chown":     true,
	"os.Symlink":   true,
	"os.Setenv":    true,

	// os.File methods
	"os.File.Close":       true,
	"os.File.Sync":        true,
	"os.File.WriteString": true,
	"os.File.Write":       true,
	"os.File.Seek":        true,
	"os.File.Truncate":    true,

	// http package
	"http.Get":                   true,
	"http.Post":                  true,
	"http.PostForm":              true,
	"http.Head":                  true,
	"http.NewRequest":            true,
	"http.NewRequestWithContext": true,

	// http.ResponseWriter methods
	"http.ResponseWriter.Write":       true,
	"http.ResponseWriter.WriteHeader": true,

	// http.Response
	"http.Response.Write": true, // uncommon but exists in some patterns

	// bytes.Buffer methods (Write returns error for interface satisfaction)
	"bytes.Buffer.Write":       true,
	"bytes.Buffer.WriteString": true,
	"bytes.Buffer.WriteByte":   true,
	"bytes.Buffer.ReadFrom":    true,
	"bytes.Buffer.WriteRune":   true,

	// strings.Builder (Write methods satisfy io.Writer)
	"strings.Builder.Write":       true,
	"strings.Builder.WriteByte":   true,
	"strings.Builder.WriteRune":   true,
	"strings.Builder.WriteString": true,

	// net package
	"net.Listen":  true,
	"net.Dial":    true,
	"net.DialTCP": true,
	"net.DialUDP": true,

	// net/http Server
	"http.Server.ListenAndServe":     true,
	"http.Server.ListenAndServeTLS":  true,
	"http.Server.Serve":              true,
	"http.Server.Shutdown":           true,
	"http.Server.RegisterOnShutdown": true,

	// bufio
	"bufio.Writer.Flush":       true,
	"bufio.Writer.Write":       true,
	"bufio.Writer.WriteString": true,
	"bufio.Writer.WriteByte":   true,
	"bufio.Writer.WriteRune":   true,
	"bufio.Reader.ReadString":  true,
	"bufio.Reader.ReadBytes":   true,
	"bufio.Scanner.Err":        true,

	// database/sql
	"sql.DB.Ping":        true,
	"sql.DB.PingContext": true,
	"sql.DB.Close":       true,
	"sql.DB.Exec":        true,
	"sql.DB.Query":       true,
	"sql.Rows.Close":     true,
	"sql.Rows.Err":       true,
	"sql.Tx.Commit":      true,
	"sql.Tx.Rollback":    true,

	// strconv
	"strconv.Atoi":       true,
	"strconv.ParseInt":   true,
	"strconv.ParseFloat": true,
	"strconv.ParseBool":  true,
	"strconv.ParseUint":  true,

	// template
	"text/template.Execute":         true,
	"text/template.ExecuteTemplate": true,
	"html/template.Execute":         true,
	"html/template.ExecuteTemplate": true,

	// exec
	"os/exec.Command.Run":   true,
	"os/exec.Command.Start": true,
	"os/exec.Command.Wait":  true,

	// log
	"log.Logger.Output": true,

	// crypto
	"crypto/tls.Listen": true,

	// image
	"image/jpeg.Encode": true,
	"image/png.Encode":  true,
	"image/gif.Encode":  true,
}

// ignoredErrorInstance represents a detected ignored-error pattern.
type ignoredErrorInstance struct {
	pattern string // human-readable pattern description
	line    int    // 1-based line number (best-effort from AST positions)
}

// checkIgnoredErrorReturn detects calls to error-returning functions where the
// error is completely ignored. Returns warning strings. Only flags NEW
// occurrences (delta-aware).
//
// Parameters:
//   - filePath: path of the written file (used for language detection)
//   - oldContent: the file content before the write ("" for new files)
//   - newContent: the file content after the write
func checkIgnoredErrorReturn(filePath, oldContent, newContent string) []string {
	if filepath.Ext(filePath) != ".go" {
		return nil
	}
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	newInstances := findIgnoredErrors(filePath, newContent)
	if len(newInstances) == 0 {
		return nil
	}

	// Delta-aware: subtract pre-existing instances from old content.
	if strings.TrimSpace(oldContent) != "" {
		oldInstances := findIgnoredErrors(filePath, oldContent)
		if len(oldInstances) > 0 {
			oldCount := make(map[string]int)
			for _, inst := range oldInstances {
				oldCount[inst.pattern]++
			}
			seen := make(map[string]int)
			filtered := newInstances[:0]
			for _, inst := range newInstances {
				seen[inst.pattern]++
				if seen[inst.pattern] <= oldCount[inst.pattern] {
					continue
				}
				filtered = append(filtered, inst)
			}
			newInstances = filtered
		}
	}

	if len(newInstances) == 0 {
		return nil
	}

	// Build summary - deduplicate by function name.
	funcCounts := make(map[string]int)
	for _, inst := range newInstances {
		funcCounts[inst.pattern]++
	}

	var parts []string
	for name, count := range funcCounts {
		if count > 1 {
			parts = append(parts, fmt.Sprintf("%s (%d calls)", name, count))
		} else {
			parts = append(parts, name)
		}
	}
	summary := joinStrings(parts, ", ")

	return []string{
		fmt.Sprintf(
			"Ignored error return value(s) from: %s. "+
				"These functions return an error that is not checked. "+
				"If the operation fails (I/O, parsing, network), the error is silently lost "+
				"and the program continues with invalid state. "+
				"Capture and handle the error: `if err := %s; err != nil { ... }`.",
			summary, "funcName(args)"),
	}
}

// findIgnoredErrors parses Go source and returns all ignored-error-return patterns.
func findIgnoredErrors(filename, src string) []ignoredErrorInstance {
	if strings.TrimSpace(src) == "" {
		return nil
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, src, 0)
	if err != nil || file == nil {
		return nil
	}

	var results []ignoredErrorInstance

	// Walk the AST looking for standalone call expressions (ExprStmt) where
	// the called function is known to return an error.
	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.ExprStmt:
			// Pattern 1: Standalone call statement - `someFunc()` where someFunc
			// returns error. The call result is completely discarded.
			if call, ok := node.X.(*ast.CallExpr); ok {
				results = append(results, checkIgnoredCall(call, fset)...)
			}
		case *ast.AssignStmt:
			// Pattern 2: Explicit discard with blank identifier - `_ = someFunc()`
			// or `_, _ = someFunc()`. Flag if the called function returns error.
			results = append(results, checkBlankAssign(node, fset)...)
		}
		return true
	})

	return results
}

// checkIgnoredCall checks a standalone call expression for ignored error returns.
func checkIgnoredCall(call *ast.CallExpr, fset *token.FileSet) []ignoredErrorInstance {
	name := resolveCallName(call)
	if name == "" || !isErrorReturningName(name) {
		return nil
	}
	pos := fset.Position(call.Pos())
	return []ignoredErrorInstance{{pattern: name, line: pos.Line}}
}

// checkBlankAssign checks an assignment statement where all LHS are blank
// identifiers for calls to error-returning functions on the RHS.
func checkBlankAssign(assign *ast.AssignStmt, fset *token.FileSet) []ignoredErrorInstance {
	allBlank := true
	for _, lhs := range assign.Lhs {
		ident, ok := lhs.(*ast.Ident)
		if !ok || ident.Name != "_" {
			allBlank = false
			break
		}
	}
	if !allBlank || len(assign.Rhs) == 0 {
		return nil
	}

	var results []ignoredErrorInstance
	for _, rhs := range assign.Rhs {
		if call, ok := rhs.(*ast.CallExpr); ok {
			results = append(results, checkIgnoredCall(call, fset)...)
		}
	}
	return results
}

// resolveCallName resolves a call expression to a fully-qualified function name.
// Handles:
//   - pkg.Func()     -> "pkg.Func"
//   - pkg.Type.Method() -> "pkg.Type.Method"  (limited - see resolveMethodChain)
//   - receiver.Method()  -> tries to resolve via type info if possible
//
// For unqualified calls (local functions), we cannot determine the return type
// without full type-checking, so we skip them. For method calls on values
// (not package-qualified), we use a heuristic: if the method name matches a
// known error-returning method, we flag it.
func resolveCallName(call *ast.CallExpr) string {
	switch fun := call.Fun.(type) {
	case *ast.SelectorExpr:
		// Try pkg.Func or pkg.Type.Method patterns.
		name := resolveSelectorChain(fun)
		if name != "" && errorReturningFuncs[name] {
			return name
		}
		// If the full chain didn't match, check if the selector name
		// (method name) matches a known error-returning method.
		methodName := fun.Sel.Name
		if isKnownErrorMethod(methodName) {
			return methodName
		}
	case *ast.Ident:
		// Local function call - can't determine return type without type info.
		return ""
	}
	return ""
}

// resolveSelectorChain resolves a SelectorExpr into a dotted name.
// Supports patterns like:
//   - pkg.Func           -> "pkg.Func"
//   - pkg.Type.Method    -> "pkg.Type.Method" (when X is itself a SelectorExpr)
//   - obj.Method         -> "Method" (bare receiver, returns method name only)
func resolveSelectorChain(sel *ast.SelectorExpr) string {
	prefix := ""
	switch x := sel.X.(type) {
	case *ast.Ident:
		prefix = x.Name
	case *ast.SelectorExpr:
		// Nested selector: e.g., json.NewEncoder(w).Encode
		// The X is json.NewEncoder(w) which is a CallExpr, not a SelectorExpr directly.
		// But for pkg.Type.Method, X would be pkg.Type (a SelectorExpr).
		innerPrefix := resolveSelectorChain(x)
		if innerPrefix != "" {
			prefix = innerPrefix
		}
	case *ast.CallExpr:
		// Method call on a result: e.g., json.NewEncoder(w).Encode()
		// Try to resolve what the call returns.
		if innerSel, ok := x.Fun.(*ast.SelectorExpr); ok {
			innerName := resolveSelectorChain(innerSel)
			if innerName != "" {
				// For constructor patterns like json.NewEncoder, the result
				// type is *json.Encoder, so the method chain becomes
				// json.NewEncoder.Encode - but we want json.Encoder.Encode.
				// Use a mapping for common constructors.
				if constructorType, ok := constructorReturns[innerName]; ok {
					return constructorType + "." + sel.Sel.Name
				}
				// Fall back to bare method name.
				return sel.Sel.Name
			}
		}
		return sel.Sel.Name
	default:
		return ""
	}

	if prefix == "" {
		return sel.Sel.Name
	}
	return prefix + "." + sel.Sel.Name
}

// constructorReturns maps known constructor functions to the type they return.
// This helps resolve method chains like json.NewEncoder(w).Encode -> json.Encoder.Encode.
var constructorReturns = map[string]string{
	"json.NewEncoder":  "json.Encoder",
	"json.NewDecoder":  "json.Decoder",
	"xml.NewEncoder":   "xml.Encoder",
	"xml.NewDecoder":   "xml.Decoder",
	"bufio.NewWriter":  "bufio.Writer",
	"bufio.NewReader":  "bufio.Reader",
	"bufio.NewScanner": "bufio.Scanner",
}

// isKnownErrorMethod returns true if a method name is commonly associated with
// error returns in the Go standard library. Used as a fallback heuristic when
// we cannot resolve the full receiver type.
func isKnownErrorMethod(methodName string) bool {
	knownErrorMethods := map[string]bool{
		"Write":             true,
		"WriteString":       true,
		"WriteByte":         true,
		"WriteRune":         true,
		"Close":             true,
		"Encode":            true,
		"Decode":            true,
		"Flush":             true,
		"Sync":              true,
		"Marshal":           true,
		"Unmarshal":         true,
		"Seek":              true,
		"Truncate":          true,
		"ReadFrom":          true,
		"ReadString":        true,
		"ReadBytes":         true,
		"Execute":           true,
		"ExecuteTemplate":   true,
		"Run":               true,
		"Start":             true,
		"Wait":              true,
		"Shutdown":          true,
		"ListenAndServe":    true,
		"ListenAndServeTLS": true,
		"Serve":             true,
		"Commit":            true,
		"Rollback":          true,
		"Ping":              true,
		"Exec":              true,
		"Query":             true,
		"Output":            true,
	}
	return knownErrorMethods[methodName]
}

// isErrorReturningName returns true if the resolved call name is known to
// return an error. Checks both the fully-qualified function map and the
// method-name heuristic set.
func isErrorReturningName(name string) bool {
	if errorReturningFuncs[name] {
		return true
	}
	// For bare method names (heuristic fallback from resolveCallName).
	return isKnownErrorMethod(name)
}
