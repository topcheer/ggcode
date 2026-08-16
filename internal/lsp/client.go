package lsp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
)

type Position struct {
	Line      int
	Character int
}

type Range struct {
	Start Position
	End   Position
}

type Location struct {
	Path  string
	Range Range
}

type Diagnostic struct {
	Severity int
	Message  string
	Range    Range
	Source   string
}

type rawLocation struct {
	URI   string   `json:"uri"`
	Range rawRange `json:"range"`
}

type Symbol struct {
	Name string
	Kind int
	Range
}

type rawRange struct {
	Start struct {
		Line      int `json:"line"`
		Character int `json:"character"`
	} `json:"start"`
	End struct {
		Line      int `json:"line"`
		Character int `json:"character"`
	} `json:"end"`
}

type rawDocumentSymbol struct {
	Name     string              `json:"name"`
	Kind     int                 `json:"kind"`
	Range    rawRange            `json:"range"`
	Children []rawDocumentSymbol `json:"children"`
}

type rawSymbolInformation struct {
	Name     string      `json:"name"`
	Kind     int         `json:"kind"`
	Location rawLocation `json:"location"`
}

type rpcEnvelope struct {
	JSONRPC string          `json:"jsonrpc,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type stdioClient struct {
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	reader   *bufio.Reader
	waitErr  chan error
	resolved ResolvedServer

	nextID  int64
	pending map[string]chan rpcEnvelope
	mu      sync.Mutex
	writeMu sync.Mutex // serializes JSON-RPC header+body writes to stdin
	failed  bool       // set when readLoop exits unexpectedly
	failMu  sync.Mutex

	stderr              lockedBuffer
	notificationHandler func(method string, params json.RawMessage)
}

// lockedBuffer wraps bytes.Buffer with a mutex so it is safe for concurrent
// reads and writes (exec.Cmd writes stderr from a goroutine while
// failPending reads it from another).
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (lb *lockedBuffer) Write(p []byte) (int, error) {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	return lb.buf.Write(p)
}

func (lb *lockedBuffer) String() string {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	return lb.buf.String()
}

// DocumentHighlight represents a single highlight occurrence of a symbol
// within the current document. Kind follows the LSP DocumentHighlightKind
// values: 1=Text, 2=Read, 3=Write.
type DocumentHighlight struct {
	Range Range
	Kind  int
}

// DocumentHighlights returns all occurrences of the symbol at pos within the
// current document, classified by access kind (read/write/text). This is
// faster and more precise than grep for understanding local symbol usage.
func DocumentHighlights(ctx context.Context, workspace, path string, pos Position) ([]DocumentHighlight, error) {
	return withOpenDocument(ctx, workspace, path, func(ctx context.Context, session *sessionClient, docURI string) ([]DocumentHighlight, error) {
		call := func() ([]DocumentHighlight, error) {
			var raw json.RawMessage
			if err := session.client.call(ctx, "textDocument/documentHighlight", map[string]any{
				"textDocument": map[string]any{"uri": docURI},
				"position":     toLSPPosition(pos),
			}, &raw); err != nil {
				return nil, err
			}
			return parseDocumentHighlights(raw), nil
		}
		return retryEmptySliceResult(ctx, session, call)
	})
}

func Hover(ctx context.Context, workspace, path string, pos Position) (string, error) {
	result, err := withOpenDocument(ctx, workspace, path, func(ctx context.Context, session *sessionClient, docURI string) (string, error) {
		call := func() (string, error) {
			var raw json.RawMessage
			if err := session.client.call(ctx, "textDocument/hover", map[string]any{
				"textDocument": map[string]any{"uri": docURI},
				"position":     toLSPPosition(pos),
			}, &raw); err != nil {
				return "", err
			}
			return parseHover(raw), nil
		}
		return retryEmptyStringResult(ctx, session, call)
	})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(result), nil
}

func Definition(ctx context.Context, workspace, path string, pos Position) ([]Location, error) {
	return withOpenDocument(ctx, workspace, path, func(ctx context.Context, session *sessionClient, docURI string) ([]Location, error) {
		call := func() ([]Location, error) {
			var raw json.RawMessage
			if err := session.client.call(ctx, "textDocument/definition", map[string]any{
				"textDocument": map[string]any{"uri": docURI},
				"position":     toLSPPosition(pos),
			}, &raw); err != nil {
				return nil, err
			}
			return parseLocations(raw), nil
		}
		return retryEmptySliceResult(ctx, session, call)
	})
}

func References(ctx context.Context, workspace, path string, pos Position) ([]Location, error) {
	return withOpenDocument(ctx, workspace, path, func(ctx context.Context, session *sessionClient, docURI string) ([]Location, error) {
		call := func() ([]Location, error) {
			var raw json.RawMessage
			if err := session.client.call(ctx, "textDocument/references", map[string]any{
				"textDocument": map[string]any{"uri": docURI},
				"position":     toLSPPosition(pos),
				"context":      map[string]any{"includeDeclaration": true},
			}, &raw); err != nil {
				return nil, err
			}
			return parseLocations(raw), nil
		}
		return retryEmptySliceResult(ctx, session, call)
	})
}

// Implementation finds implementations of the symbol at pos using textDocument/implementation.
func Implementation(ctx context.Context, workspace, path string, pos Position) ([]Location, error) {
	return withOpenDocument(ctx, workspace, path, func(ctx context.Context, session *sessionClient, docURI string) ([]Location, error) {
		call := func() ([]Location, error) {
			var raw json.RawMessage
			if err := session.client.call(ctx, "textDocument/implementation", map[string]any{
				"textDocument": map[string]any{"uri": docURI},
				"position":     toLSPPosition(pos),
			}, &raw); err != nil {
				return nil, err
			}
			return parseLocations(raw), nil
		}
		return retryEmptySliceResult(ctx, session, call)
	})
}

// CallHierarchyItem represents a call hierarchy item returned by the LSP server.
type CallHierarchyItem struct {
	Name   string
	Kind   int
	Detail string
	Path   string
	Range  Range
	// raw fields preserved for follow-up incoming/outgoing calls
	rawURI   string
	rawRange rawRange
}

type rawCallHierarchyItem struct {
	Name           string   `json:"name"`
	Kind           int      `json:"kind"`
	Detail         string   `json:"detail"`
	URI            string   `json:"uri"`
	Range          rawRange `json:"range"`
	SelectionRange rawRange `json:"selectionRange"`
}

// CallHierarchyCall represents a caller or callee in the call hierarchy.
type CallHierarchyCall struct {
	From       CallHierarchyItem
	To         CallHierarchyItem // only populated for outgoing calls
	FromRanges []Range
}

// PrepareCallHierarchy prepares call hierarchy items for the symbol at pos.
func PrepareCallHierarchy(ctx context.Context, workspace, path string, pos Position) ([]CallHierarchyItem, error) {
	return withOpenDocument(ctx, workspace, path, func(ctx context.Context, session *sessionClient, docURI string) ([]CallHierarchyItem, error) {
		call := func() ([]CallHierarchyItem, error) {
			var raw json.RawMessage
			if err := session.client.call(ctx, "textDocument/prepareCallHierarchy", map[string]any{
				"textDocument": map[string]any{"uri": docURI},
				"position":     toLSPPosition(pos),
			}, &raw); err != nil {
				return nil, err
			}
			return parseCallHierarchyItems(raw), nil
		}
		return retryCallHierarchyItems(ctx, session, call)
	})
}

// IncomingCalls returns callers of the given call hierarchy item.
// Uses item.Path to re-acquire the LSP session for the file's language.
func IncomingCalls(ctx context.Context, workspace string, item CallHierarchyItem) ([]CallHierarchyCall, error) {
	return withOpenDocument(ctx, workspace, item.Path, func(ctx context.Context, session *sessionClient, _ string) ([]CallHierarchyCall, error) {
		var raw json.RawMessage
		if err := session.client.call(ctx, "callHierarchy/incomingCalls", map[string]any{
			"item": map[string]any{
				"name":           item.Name,
				"kind":           item.Kind,
				"uri":            item.rawURI,
				"range":          item.rawRange,
				"selectionRange": item.rawRange,
			},
		}, &raw); err != nil {
			return nil, err
		}
		return parseCallHierarchyCalls(raw, true), nil
	})
}

// OutgoingCalls returns callees of the given call hierarchy item.
func OutgoingCalls(ctx context.Context, workspace string, item CallHierarchyItem) ([]CallHierarchyCall, error) {
	return withOpenDocument(ctx, workspace, item.Path, func(ctx context.Context, session *sessionClient, _ string) ([]CallHierarchyCall, error) {
		var raw json.RawMessage
		if err := session.client.call(ctx, "callHierarchy/outgoingCalls", map[string]any{
			"item": map[string]any{
				"name":           item.Name,
				"kind":           item.Kind,
				"uri":            item.rawURI,
				"range":          item.rawRange,
				"selectionRange": item.rawRange,
			},
		}, &raw); err != nil {
			return nil, err
		}
		return parseCallHierarchyCalls(raw, false), nil
	})
}

func DocumentSymbols(ctx context.Context, workspace, path string) ([]Symbol, error) {
	return withOpenDocument(ctx, workspace, path, func(ctx context.Context, session *sessionClient, docURI string) ([]Symbol, error) {
		call := func() ([]Symbol, error) {
			var raw json.RawMessage
			if err := session.client.call(ctx, "textDocument/documentSymbol", map[string]any{
				"textDocument": map[string]any{"uri": docURI},
			}, &raw); err != nil {
				return nil, err
			}
			return parseSymbols(raw), nil
		}
		return retryEmptySliceResult(ctx, session, call)
	})
}

// SiblingDiagnostic holds a diagnostic found in a file other than the edited
// file, along with the source file's path so the caller can report where the
// error originates.
type SiblingDiagnostic struct {
	File       string
	Diagnostic Diagnostic
}

// SiblingDiagnostics returns diagnostics for Go source files in the same
// directory as editedPath (excluding editedPath itself). Language servers like
// gopls push diagnostics for all files in a package via
// textDocument/publishDiagnostics; those diagnostics are cached in the session.
// This function reads from that cache — it does NOT open sibling documents or
// issue additional LSP requests — so it is fast and non-blocking.
//
// This catches cross-file compilation errors that the per-file Diagnostics
// call misses. For example, renaming a function in file A breaks callers in
// sibling file B; gopls pushes diagnostics for both files, but only file A is
// queried by the standard post-edit diagnostics check.
//
// Only files with severity 1 (Error) or 2 (Warning) are returned.
func SiblingDiagnostics(ctx context.Context, workspace, editedPath string) ([]SiblingDiagnostic, error) {
	// Only check sibling diagnostics for Go files (gopls pushes package-wide).
	if filepath.Ext(editedPath) != ".go" {
		return nil, nil
	}

	resolved, ok := ResolveServerForWorkspace(workspace)
	if !ok {
		return nil, nil
	}

	dir := filepath.Dir(editedPath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil
	}

	// Acquire the existing session to read cached diagnostics.
	session, err := globalSessions.acquire(ctx, workspace, resolved)
	if err != nil {
		return nil, nil // session not available — silently skip
	}

	editedAbs, _ := filepath.Abs(editedPath)
	var result []SiblingDiagnostic
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		// Only check Go files, excluding the edited file and test files
		// that pair with the edited file (their diagnostics overlap).
		if filepath.Ext(name) != ".go" {
			continue
		}
		siblingPath := filepath.Join(dir, name)
		siblingAbs, _ := filepath.Abs(siblingPath)
		if siblingAbs == editedAbs {
			continue
		}
		uri := fileURI(siblingPath)
		diags, seen := session.cachedDiagnostics(uri)
		if !seen || len(diags) == 0 {
			continue
		}
		for _, d := range diags {
			if d.Severity <= 2 { // Error or Warning only
				result = append(result, SiblingDiagnostic{
					File:       siblingPath,
					Diagnostic: d,
				})
			}
		}
	}
	return result, nil
}

// CrossPackageDiagnostics checks cached LSP diagnostics for Go files OUTSIDE the
// edited file's directory that may be affected by the edit. Language servers like
// gopls push diagnostics for all files in the workspace via
// textDocument/publishDiagnostics; those diagnostics are cached in the session.
//
// Unlike SiblingDiagnostics (which checks files in the same directory/package),
// this function catches cross-package breakage: when editing a file in package A
// breaks callers in package B that import A. For example, changing a function
// signature in internal/agent/agent.go will cause compilation errors in
// internal/tui/ files that call that function.
//
// To minimize false positives (pre-existing errors in unrelated files), this
// function only reports errors in files whose import block references the edited
// file's package. The package import path is derived from the go.mod module path
// plus the edited file's directory relative to the module root.
//
// This function reads from the cached diagnostics — it does NOT open documents or
// issue additional LSP requests — so it is fast and non-blocking. However, it
// does read candidate files to check their imports; this is capped to a small
// number of files (maxCrossPackageFiles) to stay fast.
func CrossPackageDiagnostics(ctx context.Context, workspace, editedPath string) ([]SiblingDiagnostic, error) {
	// Only check for Go files.
	if filepath.Ext(editedPath) != ".go" {
		return nil, nil
	}

	resolved, ok := ResolveServerForWorkspace(workspace)
	if !ok {
		return nil, nil
	}

	// Determine the Go package import path for the edited file.
	pkgPath, ok := goPackageImportPath(workspace, editedPath)
	if !ok || pkgPath == "" {
		return nil, nil
	}

	// Acquire the existing session to read cached diagnostics.
	session, err := globalSessions.acquire(ctx, workspace, resolved)
	if err != nil {
		return nil, nil // session not available — silently skip
	}

	// Collect all URIs with cached error/warning diagnostics.
	editedDir := filepath.Dir(editedPath)
	editedAbs, _ := filepath.Abs(editedPath)

	var candidates []crossPackageCandidate

	session.mu.Lock()
	for uri, state := range session.diagnostics {
		if !state.seen || len(state.diagnostics) == 0 {
			continue
		}
		// Convert URI to filesystem path.
		fp := uriToPath(uri)
		if fp == "" {
			continue
		}
		fpAbs, _ := filepath.Abs(fp)
		// Skip the edited file itself and files in the same directory
		// (those are handled by SiblingDiagnostics).
		if fpAbs == editedAbs {
			continue
		}
		if filepath.Dir(fp) == editedDir {
			continue
		}
		// Only consider Go source files.
		if filepath.Ext(fp) != ".go" {
			continue
		}
		// Collect error/warning diagnostics.
		var diags []Diagnostic
		for _, d := range state.diagnostics {
			if d.Severity <= 2 {
				diags = append(diags, d)
			}
		}
		if len(diags) > 0 {
			candidates = append(candidates, crossPackageCandidate{path: fp, diag: diags})
		}
	}
	session.mu.Unlock()

	if len(candidates) == 0 {
		return nil, nil
	}

	// Filter candidates: only include files that import the edited file's package.
	// This reduces false positives from pre-existing errors in unrelated files.
	// Sort candidates by number of errors (most errors first) so we check the
	// most likely affected files first within our cap.
	sortCandidatesByErrorCount(candidates)

	checked := 0
	var result []SiblingDiagnostic
	for _, c := range candidates {
		if checked >= maxCrossPackageFiles {
			break
		}
		checked++

		// Quick check: does this file import the edited file's package?
		if !fileImportsPackage(c.path, pkgPath) {
			continue
		}

		for _, d := range c.diag {
			result = append(result, SiblingDiagnostic{
				File:       c.path,
				Diagnostic: d,
			})
		}
	}

	return result, nil
}

// maxCrossPackageFiles limits how many candidate files we read to check imports,
// keeping the post-edit diagnostics fast even in large workspaces.
const maxCrossPackageFiles = 20

// goPackageImportPath derives the Go import path for a source file from the
// workspace's go.mod module path and the file's directory relative to the module root.
// Returns the import path and true on success, or empty string and false if the
// module path cannot be determined.
func goPackageImportPath(workspace, filePath string) (string, bool) {
	// Walk up from the file's directory to find go.mod.
	dir := filepath.Dir(filePath)
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return "", false
	}

	var modDir, modulePath string
	searchDir := absDir
	for i := 0; i < 30; i++ { // cap at 30 levels to prevent infinite loops
		modFile := filepath.Join(searchDir, "go.mod")
		if data, err := os.ReadFile(modFile); err == nil {
			modDir = searchDir
			modulePath = parseGoModModule(string(data))
			break
		}
		parent := filepath.Dir(searchDir)
		if parent == searchDir {
			break
		}
		searchDir = parent
	}

	if modulePath == "" || modDir == "" {
		return "", false
	}

	// Compute the relative path from the module root to the file's directory.
	relDir, err := filepath.Rel(modDir, absDir)
	if err != nil || relDir == "." {
		return modulePath, true
	}

	// Join module path with the relative directory, using forward slashes.
	return modulePath + "/" + filepath.ToSlash(relDir), true
}

// parseGoModModule extracts the module path from a go.mod file content.
func parseGoModModule(content string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module "))
		}
	}
	return ""
}

// fileImportsPackage checks whether a Go source file imports the given package path.
// This is a fast heuristic: it searches the file content for the import path string
// within an import block. It does not fully parse the Go AST.
func fileImportsPackage(filePath, pkgPath string) bool {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return false
	}
	content := string(data)

	// Fast path: if the package path doesn't appear anywhere in the file, skip.
	if !strings.Contains(content, pkgPath) {
		return false
	}

	// More precise check: look for the path in quoted strings within import context.
	// Check both single and block import forms.
	quoted := `"` + pkgPath + `"`
	if strings.Contains(content, quoted) {
		return true
	}

	return false
}

// crossPackageCandidate holds a file path and its cached error/warning diagnostics
// for cross-package impact analysis.
type crossPackageCandidate struct {
	path string
	diag []Diagnostic
}

// sortCandidatesByErrorCount sorts candidates in descending order by number of errors.
func sortCandidatesByErrorCount(candidates []crossPackageCandidate) {
	for i := 1; i < len(candidates); i++ {
		for j := i; j > 0 && len(candidates[j].diag) > len(candidates[j-1].diag); j-- {
			candidates[j], candidates[j-1] = candidates[j-1], candidates[j]
		}
	}
}

func Diagnostics(ctx context.Context, workspace, path string) ([]Diagnostic, error) {
	return withOpenDocument(ctx, workspace, path, func(ctx context.Context, session *sessionClient, docURI string) ([]Diagnostic, error) {
		if session.supportsPullDiagnostics() {
			var raw json.RawMessage
			if err := session.client.call(ctx, "textDocument/diagnostic", map[string]any{
				"textDocument": map[string]any{"uri": docURI},
			}, &raw); err == nil {
				session.setPullDiagnosticsSupport(true)
				if parsed := parseDocumentDiagnostics(raw); len(parsed) > 0 {
					session.setPublishedDiagnostics(docURI, parsed)
					return parsed, nil
				}
			} else if isUnsupportedDiagnosticMethodError(err) {
				session.setPullDiagnosticsSupport(false)
			} else {
				return nil, err
			}
		}
		deadline := time.Now().Add(publishedDiagnosticsWait)
		for time.Now().Before(deadline) {
			if published, seen := session.publishedDiagnostics(docURI); seen {
				return published, nil
			}
			time.Sleep(40 * time.Millisecond)
		}
		return nil, nil
	})
}

func retryEmptyStringResult(ctx context.Context, session *sessionClient, call func() (string, error)) (string, error) {
	result, err := call()
	if err != nil || strings.TrimSpace(result) != "" || !session.shouldRetryEmptyResults() {
		return result, err
	}
	for i := 0; i < csharpWarmupRetryAttempts; i++ {
		if err := sleepWithContext(ctx, csharpWarmupRetryDelay); err != nil {
			return result, nil
		}
		next, err := call()
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(next) != "" {
			return next, nil
		}
	}
	return result, nil
}

func retryEmptySliceResult[T any](ctx context.Context, session *sessionClient, call func() ([]T, error)) ([]T, error) {
	result, err := call()
	if err != nil || len(result) > 0 || !session.shouldRetryEmptyResults() {
		return result, err
	}
	for i := 0; i < csharpWarmupRetryAttempts; i++ {
		if err := sleepWithContext(ctx, csharpWarmupRetryDelay); err != nil {
			return result, nil
		}
		next, err := call()
		if err != nil {
			return nil, err
		}
		if len(next) > 0 {
			return next, nil
		}
	}
	return result, nil
}

func retryCallHierarchyItems(ctx context.Context, session *sessionClient, call func() ([]CallHierarchyItem, error)) ([]CallHierarchyItem, error) {
	result, err := call()
	if err != nil || len(result) > 0 || !session.shouldRetryEmptyResults() {
		return result, err
	}
	for i := 0; i < csharpWarmupRetryAttempts; i++ {
		if err := sleepWithContext(ctx, csharpWarmupRetryDelay); err != nil {
			return result, nil
		}
		next, err := call()
		if err != nil {
			return nil, err
		}
		if len(next) > 0 {
			return next, nil
		}
	}
	return result, nil
}

func parseCallHierarchyItems(raw json.RawMessage) []CallHierarchyItem {
	var items []rawCallHierarchyItem
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil
	}
	out := make([]CallHierarchyItem, 0, len(items))
	for _, item := range items {
		path := uriToPath(item.URI)
		out = append(out, CallHierarchyItem{
			Name:   item.Name,
			Kind:   item.Kind,
			Detail: item.Detail,
			Path:   path,
			Range: Range{
				Start: Position{Line: item.Range.Start.Line + 1, Character: item.Range.Start.Character + 1},
				End:   Position{Line: item.Range.End.Line + 1, Character: item.Range.End.Character + 1},
			},
			rawURI:   item.URI,
			rawRange: item.Range,
		})
	}
	return out
}

func parseCallHierarchyCalls(raw json.RawMessage, incoming bool) []CallHierarchyCall {
	var rawCalls []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rawCalls); err != nil {
		return nil
	}
	var out []CallHierarchyCall
	for _, rc := range rawCalls {
		itemKey := "from"
		if !incoming {
			itemKey = "to"
		}
		itemRaw, ok := rc[itemKey]
		if !ok {
			continue
		}
		var rawItem rawCallHierarchyItem
		if err := json.Unmarshal(itemRaw, &rawItem); err != nil {
			continue
		}
		item := CallHierarchyItem{
			Name:   rawItem.Name,
			Kind:   rawItem.Kind,
			Detail: rawItem.Detail,
			Path:   uriToPath(rawItem.URI),
			Range: Range{
				Start: Position{Line: rawItem.Range.Start.Line + 1, Character: rawItem.Range.Start.Character + 1},
				End:   Position{Line: rawItem.Range.End.Line + 1, Character: rawItem.Range.End.Character + 1},
			},
			rawURI:   rawItem.URI,
			rawRange: rawItem.Range,
		}
		call := CallHierarchyCall{FromRanges: parseFromRanges(rc["fromRanges"])}
		if incoming {
			call.From = item
		} else {
			call.To = item
		}
		out = append(out, call)
	}
	return out
}

func parseFromRanges(raw json.RawMessage) []Range {
	if raw == nil {
		return nil
	}
	var ranges []rawRange
	if err := json.Unmarshal(raw, &ranges); err != nil {
		return nil
	}
	out := make([]Range, 0, len(ranges))
	for _, r := range ranges {
		out = append(out, Range{
			Start: Position{Line: r.Start.Line + 1, Character: r.Start.Character + 1},
			End:   Position{Line: r.End.Line + 1, Character: r.End.Character + 1},
		})
	}
	return out
}

func sleepWithContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func isUnsupportedDiagnosticMethodError(err error) bool {
	text := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(text, "unsupportedoperationexception") ||
		strings.Contains(text, "method not found") ||
		strings.Contains(text, "unsupported method") ||
		strings.Contains(text, "textdocument/diagnostic failed")
}

func startClient(ctx context.Context, workspace string, resolved ResolvedServer) (*stdioClient, error) {
	cmd := exec.Command(resolved.Binary, resolved.Args...)
	cmd.Dir = workspace
	cmd.Env = serverLaunchEnv(resolved.Binary)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	client := &stdioClient{
		cmd:      cmd,
		stdin:    stdin,
		reader:   bufio.NewReader(stdout),
		waitErr:  make(chan error, 1),
		resolved: resolved,
		pending:  make(map[string]chan rpcEnvelope),
	}
	cmd.Stderr = &client.stderr
	if err := cmd.Start(); err != nil {
		debug.Log("lsp", "startClient: failed to start %s: %v", resolved.Binary, err)
		return nil, err
	}
	debug.Log("lsp", "startClient: started %s workspace=%s pid=%d", resolved.Binary, workspace, cmd.Process.Pid)
	safego.Go("lsp.waitProcess", func() { client.waitErr <- cmd.Wait() })
	safego.Go("lsp.readLoop", func() { client.readLoop() })
	if err := client.call(ctx, "initialize", map[string]any{
		"processId": os.Getpid(),
		"rootPath":  workspace,
		"rootUri":   fileURI(workspace),
		"workspaceFolders": []map[string]string{{
			"uri":  fileURI(workspace),
			"name": filepath.Base(workspace),
		}},
		"capabilities": map[string]any{
			"workspace": map[string]any{
				"configuration":    true,
				"workspaceFolders": true,
			},
			"textDocument": map[string]any{
				"hover":          map[string]any{},
				"definition":     map[string]any{},
				"references":     map[string]any{},
				"implementation": map[string]any{},
				"callHierarchy":  map[string]any{},
				"codeAction": map[string]any{
					"dynamicRegistration": false,
					"dataSupport":         true,
					"resolveSupport": map[string]any{
						"properties": []string{"edit", "command"},
					},
					"codeActionLiteralSupport": map[string]any{
						"codeActionKind": map[string]any{
							"valueSet": []string{
								"",
								"quickfix",
								"refactor",
								"refactor.extract",
								"refactor.inline",
								"refactor.rewrite",
								"source",
								"source.organizeImports",
							},
						},
					},
				},
				"documentSymbol": map[string]any{
					"hierarchicalDocumentSymbolSupport": true,
				},
				"publishDiagnostics": map[string]any{
					"relatedInformation": true,
				},
			},
		},
	}, nil); err != nil {
		client.close()
		return nil, err
	}
	if err := client.notify(ctx, "initialized", map[string]any{}); err != nil {
		client.close()
		return nil, err
	}
	if err := client.notify(ctx, "workspace/didChangeConfiguration", map[string]any{
		"settings": clientWorkspaceSettings(resolved, workspace),
	}); err != nil {
		client.close()
		return nil, err
	}
	if strings.TrimSpace(workspace) != "" {
		if err := client.notify(ctx, "workspace/didChangeWorkspaceFolders", map[string]any{
			"event": map[string]any{
				"added": []map[string]string{{
					"uri":  fileURI(workspace),
					"name": filepath.Base(workspace),
				}},
				"removed": []map[string]string{},
			},
		}); err != nil {
			client.close()
			return nil, err
		}
	}
	return client, nil
}

func serverLaunchEnv(binary string) []string {
	env := os.Environ()
	if binaryBaseName(binary) != "csharp-ls" {
		return env
	}
	if hasEnvKey("DOTNET_ROOT") {
		return env
	}
	root := detectDotnetRoot()
	if strings.TrimSpace(root) == "" {
		return env
	}
	env = append(env, "DOTNET_ROOT="+root)
	if strings.TrimSpace(os.Getenv("DOTNET_ROOT_ARM64")) == "" {
		env = append(env, "DOTNET_ROOT_ARM64="+root)
	}
	return env
}

func hasEnvKey(key string) bool {
	value, ok := os.LookupEnv(key)
	return ok && strings.TrimSpace(value) != ""
}

func detectDotnetRoot() string {
	if root := strings.TrimSpace(os.Getenv("DOTNET_ROOT")); root != "" {
		return root
	}
	path, err := exec.LookPath("dotnet")
	if err != nil {
		return ""
	}
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err == nil && strings.TrimSpace(resolvedPath) != "" {
		path = resolvedPath
	}
	dir := filepath.Dir(path)
	if filepath.Base(dir) == "libexec" {
		return dir
	}
	if filepath.Base(dir) == "bin" {
		candidate := filepath.Join(filepath.Dir(dir), "libexec")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	}
	return dir
}

func (c *stdioClient) close() {
	if c == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = c.call(ctx, "shutdown", map[string]any{}, nil)
	_ = c.notify(ctx, "exit", map[string]any{})
	_ = c.stdin.Close()
	select {
	case <-c.waitErr:
	case <-time.After(500 * time.Millisecond):
		if c.cmd != nil && c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
		}
	}
}

func (c *stdioClient) readLoop() {
	for {
		msg, err := readRPCMessage(c.reader)
		if err != nil {
			// Mark client as failed so callers know the session is dead.
			c.failMu.Lock()
			c.failed = true
			c.failMu.Unlock()
			debug.Log("lsp", "readLoop: exited with error: %v (server=%s)", err, c.resolved.Binary)
			c.failPending(err)
			return
		}
		if hasRPCID(msg.ID) && strings.TrimSpace(msg.Method) != "" {
			c.handleServerRequest(msg)
			continue
		}
		if !hasRPCID(msg.ID) {
			c.mu.Lock()
			handler := c.notificationHandler
			c.mu.Unlock()
			if handler != nil && strings.TrimSpace(msg.Method) != "" {
				handler(msg.Method, msg.Params)
			}
			continue
		}
		idKey := rpcIDKey(msg.ID)
		c.mu.Lock()
		ch := c.pending[idKey]
		delete(c.pending, idKey)
		c.mu.Unlock()
		if ch != nil {
			ch <- msg
		}
	}
}

func (c *stdioClient) handleServerRequest(msg rpcEnvelope) {
	result, err := serverRequestResult(msg.Method, msg.Params, c.cmd.Dir, c.resolved)
	if err != nil {
		_ = c.write(rpcEnvelope{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Error:   &rpcError{Code: -32603, Message: err.Error()},
		})
		return
	}
	_ = c.write(rpcEnvelope{
		JSONRPC: "2.0",
		ID:      msg.ID,
		Result:  result,
	})
}

func serverRequestResult(method string, params json.RawMessage, workspace string, resolved ResolvedServer) (json.RawMessage, error) {
	switch strings.TrimSpace(method) {
	case "client/registerCapability", "client/unregisterCapability", "window/workDoneProgress/create", "window/showMessageRequest":
		return json.RawMessage("null"), nil
	case "workspace/configuration":
		var payload struct {
			Items []struct {
				Section string `json:"section"`
			} `json:"items"`
		}
		if err := json.Unmarshal(params, &payload); err != nil {
			return nil, err
		}
		settings := clientWorkspaceSettings(resolved, workspace)
		results := make([]any, len(payload.Items))
		for i, item := range payload.Items {
			results[i] = workspaceSettingValue(settings, item.Section)
		}
		return json.Marshal(results)
	case "workspace/workspaceFolders":
		if strings.TrimSpace(workspace) == "" {
			return json.RawMessage("[]"), nil
		}
		return json.Marshal([]map[string]string{{
			"uri":  fileURI(workspace),
			"name": filepath.Base(workspace),
		}})
	default:
		return json.RawMessage("null"), nil
	}
}

func clientWorkspaceSettings(resolved ResolvedServer, workspace string) map[string]any {
	settings := map[string]any{}
	if binaryBaseName(resolved.Binary) == "csharp-ls" {
		settings["csharp"] = csharpWorkspaceSettings(resolved, workspace)
	}
	return settings
}

func csharpWorkspaceSettings(resolved ResolvedServer, workspace string) map[string]any {
	settings := map[string]any{
		"logLevel":               "info",
		"applyFormattingOptions": false,
		"useMetadataUris":        false,
		"razorSupport":           false,
		"solution":               nil,
		"solutionPathOverride":   nil,
	}
	if solution := csharpSolutionOverride(resolved, workspace); solution != "" {
		settings["solution"] = solution
		settings["solutionPathOverride"] = solution
	}
	return settings
}

func csharpSolutionOverride(resolved ResolvedServer, workspace string) string {
	for i := 0; i < len(resolved.Args)-1; i++ {
		if resolved.Args[i] != "--solution" {
			continue
		}
		solution := strings.TrimSpace(resolved.Args[i+1])
		if solution == "" {
			return ""
		}
		if filepath.IsAbs(solution) {
			return solution
		}
		if strings.TrimSpace(workspace) == "" {
			return solution
		}
		return filepath.Join(workspace, solution)
	}
	return ""
}

func workspaceSettingValue(settings map[string]any, section string) any {
	section = strings.TrimSpace(section)
	if section == "" {
		return settings
	}
	current := any(settings)
	for _, part := range strings.Split(section, ".") {
		m, ok := current.(map[string]any)
		if !ok {
			return map[string]any{}
		}
		next, ok := m[part]
		if !ok {
			return map[string]any{}
		}
		current = next
	}
	return current
}

func (c *stdioClient) failPending(err error) {
	if c != nil {
		if stderr := strings.TrimSpace(c.stderr.String()); stderr != "" {
			err = fmt.Errorf("%w (stderr: %s)", err, stderr)
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, ch := range c.pending {
		ch <- rpcEnvelope{ID: json.RawMessage(id), Error: &rpcError{Code: -1, Message: err.Error()}}
		delete(c.pending, id)
	}
}

func (c *stdioClient) call(ctx context.Context, method string, params any, out any) error {
	// Fail fast if readLoop has exited — the session is dead.
	c.failMu.Lock()
	failed := c.failed
	c.failMu.Unlock()
	if failed {
		return fmt.Errorf("lsp: session terminated (server crashed or protocol error)")
	}
	id := atomic.AddInt64(&c.nextID, 1)
	idRaw := rpcNumericID(id)
	rawParams, err := json.Marshal(params)
	if err != nil {
		return err
	}
	ch := make(chan rpcEnvelope, 1)
	c.mu.Lock()
	c.pending[rpcIDKey(idRaw)] = ch
	c.mu.Unlock()
	if err := c.write(rpcEnvelope{JSONRPC: "2.0", ID: idRaw, Method: method, Params: rawParams}); err != nil {
		c.mu.Lock()
		delete(c.pending, rpcIDKey(idRaw))
		c.mu.Unlock()
		return err
	}
	select {
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, rpcIDKey(idRaw))
		c.mu.Unlock()
		return ctx.Err()
	case msg := <-ch:
		if msg.Error != nil {
			return fmt.Errorf("%s failed: %s", method, msg.Error.Message)
		}
		if out != nil && len(msg.Result) > 0 {
			if err := json.Unmarshal(msg.Result, out); err != nil {
				return err
			}
		}
		return nil
	}
}

func (c *stdioClient) notify(ctx context.Context, method string, params any) error {
	rawParams, err := json.Marshal(params)
	if err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return c.write(rpcEnvelope{JSONRPC: "2.0", Method: method, Params: rawParams})
	}
}

func (c *stdioClient) write(msg rpcEnvelope) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if _, err := fmt.Fprintf(c.stdin, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
		return err
	}
	_, err = c.stdin.Write(body)
	return err
}

func readRPCMessage(r *bufio.Reader) (rpcEnvelope, error) {
	var msg rpcEnvelope
	contentLength := 0
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return msg, err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		if strings.HasPrefix(strings.ToLower(line), "content-length:") {
			value := strings.TrimSpace(strings.TrimPrefix(strings.ToLower(line), "content-length:"))
			contentLength, err = strconv.Atoi(value)
			if err != nil {
				return msg, err
			}
		}
	}
	if contentLength <= 0 {
		return msg, fmt.Errorf("missing content length")
	}
	body := make([]byte, contentLength)
	if _, err := io.ReadFull(r, body); err != nil {
		return msg, err
	}
	if err := json.Unmarshal(body, &msg); err != nil {
		return msg, err
	}
	return msg, nil
}

// fileURI converts a filesystem path to a file:// URI.
// Windows drive paths ("C:/foo/bar.go") must be rooted with a leading slash so
// the drive letter is not mistaken for the URI host component: the correct
// encoding is file:///C:/foo/bar.go (three slashes, empty host). Without the
// root, url.URL.String() emits file://C:/foo/bar.go, which every consumer then
// parses with "C:" as the host and loses the drive letter entirely.
func fileURI(path string) string {
	slashed := filepath.ToSlash(path)
	if isWindowsDrivePath(slashed) {
		slashed = "/" + slashed
	}
	u := url.URL{Scheme: "file", Path: slashed}
	return u.String()
}

// isWindowsDrivePath reports whether s begins with a drive-letter prefix such
// as "C:" or "d:". It is purely lexical so it works on every GOOS, which lets
// POSIX hosts construct and round-trip Windows paths as plain strings.
func isWindowsDrivePath(s string) bool {
	return len(s) >= 2 && isDriveLetter(s[0]) && s[1] == ':'
}

// isDriveLetter reports whether c is an ASCII drive letter.
func isDriveLetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func hasRPCID(id json.RawMessage) bool {
	return len(bytes.TrimSpace(id)) > 0 && string(bytes.TrimSpace(id)) != "null"
}

func rpcIDKey(id json.RawMessage) string {
	return string(bytes.TrimSpace(id))
}

func rpcNumericID(id int64) json.RawMessage {
	return json.RawMessage(strconv.FormatInt(id, 10))
}

func toLSPPosition(pos Position) map[string]int {
	line := max(0, pos.Line-1)
	character := max(0, pos.Character-1)
	return map[string]int{"line": line, "character": character}
}

func parseHover(raw json.RawMessage) string {
	var response struct {
		Contents any `json:"contents"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return ""
	}
	return stringifyHoverContents(response.Contents)
}

func stringifyHoverContents(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case map[string]any:
		if v, ok := typed["value"].(string); ok {
			return v
		}
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			if part := strings.TrimSpace(stringifyHoverContents(item)); part != "" {
				parts = append(parts, part)
			}
		}
		return strings.Join(parts, "\n\n")
	}
	return ""
}

func parseLocations(raw json.RawMessage) []Location {
	var list []rawLocation
	if err := json.Unmarshal(raw, &list); err == nil && len(list) > 0 {
		return toLocations(list)
	}
	var single rawLocation
	if err := json.Unmarshal(raw, &single); err == nil && single.URI != "" {
		return toLocations([]rawLocation{single})
	}
	return nil
}

func toLocations(list []rawLocation) []Location {
	out := make([]Location, 0, len(list))
	for _, item := range list {
		path := uriToPath(item.URI)
		out = append(out, Location{
			Path: path,
			Range: Range{
				Start: Position{Line: item.Range.Start.Line + 1, Character: item.Range.Start.Character + 1},
				End:   Position{Line: item.Range.End.Line + 1, Character: item.Range.End.Character + 1},
			},
		})
	}
	return out
}

func parseSymbols(raw json.RawMessage) []Symbol {
	var probe []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil || len(probe) == 0 {
		return nil
	}
	if _, ok := probe[0]["location"]; ok {
		var infos []rawSymbolInformation
		if err := json.Unmarshal(raw, &infos); err != nil {
			return nil
		}
		out := make([]Symbol, 0, len(infos))
		for _, item := range infos {
			out = append(out, Symbol{
				Name:  item.Name,
				Kind:  item.Kind,
				Range: toRange(item.Location.Range),
			})
		}
		return out
	}

	var documents []rawDocumentSymbol
	if err := json.Unmarshal(raw, &documents); err == nil && len(documents) > 0 {
		out := make([]Symbol, 0, len(documents))
		var walk func(rawDocumentSymbol)
		walk = func(item rawDocumentSymbol) {
			out = append(out, Symbol{Name: item.Name, Kind: item.Kind, Range: toRange(item.Range)})
			for _, child := range item.Children {
				walk(child)
			}
		}
		for _, item := range documents {
			walk(item)
		}
		return out
	}
	return nil
}

func toRange(r rawRange) Range {
	return Range{
		Start: Position{Line: r.Start.Line + 1, Character: r.Start.Character + 1},
		End:   Position{Line: r.End.Line + 1, Character: r.End.Character + 1},
	}
}

func parseDocumentDiagnostics(raw json.RawMessage) []Diagnostic {
	var response struct {
		Items []struct {
			Severity int    `json:"severity"`
			Message  string `json:"message"`
			Source   string `json:"source"`
			Range    struct {
				Start struct {
					Line      int `json:"line"`
					Character int `json:"character"`
				} `json:"start"`
				End struct {
					Line      int `json:"line"`
					Character int `json:"character"`
				} `json:"end"`
			} `json:"range"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil
	}
	out := make([]Diagnostic, 0, len(response.Items))
	for _, item := range response.Items {
		out = append(out, Diagnostic{
			Severity: item.Severity,
			Message:  item.Message,
			Source:   item.Source,
			Range: Range{
				Start: Position{Line: item.Range.Start.Line + 1, Character: item.Range.Start.Character + 1},
				End:   Position{Line: item.Range.End.Line + 1, Character: item.Range.End.Character + 1},
			},
		})
	}
	return out
}

// uriToPath converts a file:// URI back to a filesystem path.
// Two Windows encodings are handled so the drive letter survives the round
// trip regardless of who produced the URI:
//
//	file:///C:/foo/bar.go  (canonical, three slashes, empty host)
//	file://C:/foo/bar.go   (legacy two-slash form where the drive letter was
//	                       parsed as the host; produced by older fileURI)
//
// Both yield "C:/foo/bar.go". POSIX URIs are returned cleaned, unchanged.
func uriToPath(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "file" {
		return raw
	}
	path := u.Path
	// Legacy two-slash form: reconstruct the drive letter from the host.
	// url.Parse lowercases the host ("C:" → "c:"), so restore upper case.
	if u.Host != "" && isWindowsDrivePath(u.Host) {
		path = "/" + strings.ToUpper(u.Host[:1]) + u.Host[1:] + path
	}
	// Rooted Windows drive path: "/C:/foo" → "C:/foo". Forward slashes are
	// kept (Windows APIs accept them), which also keeps this deterministic
	// on non-Windows GOOS values.
	if len(path) >= 3 && path[0] == '/' && isDriveLetter(path[1]) && path[2] == ':' {
		return strings.ToUpper(path[1:2]) + ":" + path[3:]
	}
	return filepath.Clean(path)
}
