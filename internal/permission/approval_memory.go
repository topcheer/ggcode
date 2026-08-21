package permission

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

// ApprovalMemory provides session-level learned approvals for SupervisedMode.
//
// Competitor analysis:
//   - Claude Code: after the user approves the same tool+path pattern 3 times,
//     it auto-approves subsequent matching calls ("Don't ask again" behavior).
//   - Cursor: remembers per-file approval decisions within a session.
//   - Cline/OpenHands: maintains an auto-approve list that grows as the user
//     approves similar operations.
//
// Gap in ggcode: every tool call requiring "Ask" in supervised mode prompts the
// user, even when the user has already approved the exact same operation
// multiple times. This causes approval fatigue - the most common complaint about
// supervised mode.
//
// Design:
//   - Tracks (toolName, pathSignature) pairs that the user has approved.
//   - pathSignature generalizes paths: same directory + same extension → same key.
//     e.g. "edit_file" on src/foo.go and src/bar.go share the same signature.
//   - After autoApproveThreshold consecutive approvals of the same key, future
//     matching calls are auto-approved.
//   - Any single Deny resets the count for that key to zero.
//   - Only active in SupervisedMode - auto/bypass/autopilot already handle this.
//   - Clears when the user switches permission modes (fresh start per mode).

const (
	// autoApproveThreshold is the number of consecutive user approvals for the
	// same (tool, pathSignature) before auto-approval kicks in.
	autoApproveThreshold = 3

	// maxTrackedKeys bounds memory usage. With path generalization, the number
	// of distinct keys is small, but this prevents unbounded growth.
	maxTrackedKeys = 200
)

// approvalEntry tracks approval history for a single key.
type approvalEntry struct {
	consecutive  int  // consecutive approvals (reset on deny)
	autoApproved bool // true once threshold reached
}

// ApprovalMemory tracks session-level tool approval patterns.
type ApprovalMemory struct {
	mu    sync.RWMutex
	store map[string]*approvalEntry
}

// NewApprovalMemory creates a new approval memory store.
func NewApprovalMemory() *ApprovalMemory {
	return &ApprovalMemory{
		store: make(map[string]*approvalEntry),
	}
}

// pathSignature generalizes a file path into a pattern key for approval
// matching. Two paths in the same directory with the same extension share
// the same signature, reducing repeated prompts for common edit patterns.
//
// Examples:
//
//	src/foo.go → src/*.go
//	internal/bar/baz_test.go → internal/bar/*_test.go
//	/etc/passwd → /etc/*  (sensitive - won't be auto-approved anyway)
func pathSignature(path string) string {
	if path == "" {
		return ""
	}
	dir := filepath.Dir(path)
	ext := filepath.Ext(path)
	if ext == "" {
		// No extension: use the directory only (common for commands).
		return dir
	}
	// Preserve _test suffix for Go test files - they have different risk.
	base := filepath.Base(path)
	if strings.HasSuffix(base, "_test"+ext) {
		return filepath.Join(dir, "*_test"+ext)
	}
	return filepath.Join(dir, "*"+ext)
}

// commandSignature generalizes a command string for approval matching.
// Uses the first two tokens (binary + first argument) as the key to prevent
// over-broad auto-approval (e.g., "git status" must not auto-approve
// "git push --force"). Rejects command chaining entirely.
func commandSignature(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return ""
	}
	// #777: newlines, $( ) and backticks execute/embed additional commands
	// beyond the ;|& chain forms. Without them the first line's tokens became
	// the signature, so an approved "make build" silently auto-approved a
	// two-line payload; `git push <anything>` also fell into the approved
	// "git push origin main" key. Never memorize shell metacharacter commands.
	if strings.ContainsAny(cmd, ";|&\n$`") || strings.Contains(cmd, "$(") {
		return cmd + ":no-auto-approve:chained"
	}
	tokens := strings.Fields(cmd)
	if len(tokens) == 0 {
		return ""
	}
	if len(tokens) >= 2 {
		return tokens[0] + " " + tokens[1]
	}
	return tokens[0]
}

// MakeKey creates the tracking key from a tool name and its input arguments.
// Returns ("", false) if no meaningful signature can be extracted.
func MakeKey(toolName string, input json.RawMessage) (string, bool) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(input, &m); err != nil {
		return toolName, true // fallback: just use tool name
	}

	// For file tools, use path signature.
	for _, key := range []string{"file_path", "path", "directory", "notebook_path"} {
		if v, ok := m[key]; ok {
			var s string
			if json.Unmarshal(v, &s) == nil && s != "" {
				return toolName + ":" + pathSignature(s), true
			}
		}
	}

	// For multi-file tools, use the first file's directory.
	if v, ok := m["files"]; ok {
		var files []map[string]json.RawMessage
		if json.Unmarshal(v, &files) == nil && len(files) > 0 {
			if rawPath, ok := files[0]["path"]; ok {
				var s string
				if json.Unmarshal(rawPath, &s) == nil && s != "" {
					return toolName + ":" + pathSignature(s), true
				}
			}
		}
	}

	// For command tools, use the binary name.
	for _, key := range []string{"command", "input"} {
		if v, ok := m[key]; ok {
			var s string
			if json.Unmarshal(v, &s) == nil && s != "" {
				return toolName + ":" + commandSignature(s), true
			}
		}
	}

	return toolName, true
}

// ShouldAutoApprove returns true if the (tool, input) pattern has been
// approved enough times to qualify for automatic approval.
func (am *ApprovalMemory) ShouldAutoApprove(toolName string, input json.RawMessage) bool {
	if am == nil {
		return false
	}
	key, _ := MakeKey(toolName, input)
	am.mu.RLock()
	defer am.mu.RUnlock()
	entry, ok := am.store[key]
	if !ok {
		return false
	}
	return entry.autoApproved
}

// RecordApproval records that the user approved a tool call. After
// autoApproveThreshold consecutive approvals, future calls with the same
// key are auto-approved.
func (am *ApprovalMemory) RecordApproval(toolName string, input json.RawMessage) {
	if am == nil {
		return
	}
	key, _ := MakeKey(toolName, input)
	am.mu.Lock()
	defer am.mu.Unlock()

	entry, ok := am.store[key]
	if !ok {
		// Bound the store size.
		if len(am.store) >= maxTrackedKeys {
			am.evictOldest()
		}
		entry = &approvalEntry{}
		am.store[key] = entry
	}
	entry.consecutive++
	if entry.consecutive >= autoApproveThreshold && !entry.autoApproved {
		entry.autoApproved = true
		debug.Log("approval-memory", "auto-approve activated for %s (after %d approvals)", key, entry.consecutive)
	}
}

// RecordDeny records that the user denied a tool call, resetting the
// consecutive approval count for that key.
func (am *ApprovalMemory) RecordDeny(toolName string, input json.RawMessage) {
	if am == nil {
		return
	}
	key, _ := MakeKey(toolName, input)
	am.mu.Lock()
	defer am.mu.Unlock()

	if entry, ok := am.store[key]; ok {
		entry.consecutive = 0
		entry.autoApproved = false
	}
}

// Reset clears all learned approvals (e.g., when switching permission modes).
func (am *ApprovalMemory) Reset() {
	if am == nil {
		return
	}
	am.mu.Lock()
	defer am.mu.Unlock()
	am.store = make(map[string]*approvalEntry)
}

// AutoApprovedKeys returns the set of keys currently auto-approved.
// Used for diagnostics and TUI display.
func (am *ApprovalMemory) AutoApprovedKeys() []string {
	if am == nil {
		return nil
	}
	am.mu.RLock()
	defer am.mu.RUnlock()
	var keys []string
	for k, e := range am.store {
		if e.autoApproved {
			keys = append(keys, k)
		}
	}
	return keys
}

// evictOldest removes the entry with the lowest consecutive count.
// Caller must hold am.mu.
func (am *ApprovalMemory) evictOldest() {
	var minKey string
	var minCount int = -1
	for k, e := range am.store {
		if minCount == -1 || e.consecutive < minCount {
			minKey = k
			minCount = e.consecutive
		}
	}
	if minKey != "" {
		delete(am.store, minKey)
	}
}
