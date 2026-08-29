package permission

import (
	"encoding/json"
	"path/filepath"
	"sort"
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
	// lastMode is the permission mode the current entries were learned
	// under; EnsureModeScope resets on change (#1281).
	lastMode PermissionMode
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
	// #1281: shell redirections are side-effect channels. `echo x > a` and
	// `echo x > ~/.bashrc` previously shared the `echo x` key, so approving
	// the former three times auto-executed the latter — a persistence-path
	// write with no prompt. Fold every redirection TARGET into the signature
	// so a different destination is a different key (and #777's chaining
	// rejection stays intact for the operators themselves).
	if strings.Contains(cmd, ">") || strings.Contains(cmd, "<") {
		return cmd + ":redirect"
	}
	tokens := strings.Fields(cmd)
	if len(tokens) == 0 {
		return ""
	}
	// #1281: the two-token key was too wide for flag-carrying commands —
	// `git push origin main` approved, then `git push origin master --force`
	// and `git push origin --delete main` matched the same `git push` key.
	// Any argument that changes WHAT the command destroys or where it pushes
	// (--force, --delete, --hard, -f, refspecs on push, ...) must widen the
	// signature to the full command so the variant needs its own approval.
	if commandHasDestructiveFlag(tokens) {
		return strings.Join(tokens, " ")
	}
	if len(tokens) >= 2 {
		return tokens[0] + " " + tokens[1]
	}
	return tokens[0]
}

// commandHasDestructiveFlag reports whether any token is a flag/argument
// class that makes a command materially more destructive than its bare
// verb-argument form (#1281). Not exhaustive — it is a one-way ratchet: when
// in doubt the signature widens (full command), never narrows.
func commandHasDestructiveFlag(tokens []string) bool {
	for _, tok := range tokens[1:] {
		t := strings.ToLower(strings.TrimLeft(tok, "-"))
		switch t {
		case "force", "f", "hard", "delete", "d", "recursive", "r",
			"force-push", "ignore-errors", "no-preserve-root":
			return true
		}
		// refspecs like master:..main or :branch (push --delete shorthand)
		if strings.Contains(tok, ":..") || strings.HasPrefix(tok, ":") {
			return true
		}
	}
	return false
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

	// For multi-file tools, sign EVERY file path (#1281: the first-file-only
	// key let multi_file_write/push_files batches ride one approval - every
	// non-first file bypassed human review entirely).
	if v, ok := m["files"]; ok {
		var files []map[string]json.RawMessage
		if json.Unmarshal(v, &files) == nil && len(files) > 0 {
			sigs := make([]string, 0, len(files))
			for _, f := range files {
				if rawPath, ok := f["path"]; ok {
					var s string
					if json.Unmarshal(rawPath, &s) == nil && s != "" {
						sigs = append(sigs, pathSignature(s))
					}
				}
			}
			if len(sigs) > 0 {
				sort.Strings(sigs)
				return toolName + ":" + strings.Join(sigs, ","), true
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

// EnsureModeScope resets the learned approvals whenever the permission mode
// changes (#1281: the doc comment always claimed "Reset on mode switch" but
// no caller existed - approvals learned in bypass/autopilot survived into
// supervised and vice versa, silently changing what auto-approved).
func (am *ApprovalMemory) EnsureModeScope(mode PermissionMode) {
	if am == nil {
		return
	}
	am.mu.Lock()
	defer am.mu.Unlock()
	if am.lastMode != mode {
		am.lastMode = mode
		am.store = make(map[string]*approvalEntry)
	}
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
