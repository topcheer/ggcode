package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/tool"
)

// Duplicate-suppression ledger for non-idempotent mutating tool calls.
//
// Research basis: "Agent Mesh: Reliability Primitives for Non-Idempotent Agent
// Delegation" (arXiv:2608.26225, Aug 2026) reports a production incident in
// which a verifier agent issued the same mutating tool call 54 times over 11
// minutes — every call returned success, so no error-rate breaker could fire.
// The paper's S2 (tool-invocation) seam introduces a ledger whose
// "suppressed-as-duplicate" verdict is deliberately distinct from refusal:
// the call is NOT re-executed and the prior successful result is replayed.
// Community practice converges on the same design ("Idempotency Keys for
// Retry-Safe Agent Tool Calls", viralruparel.com 2026; inngest.com durable
// execution guide): the agent framework must deduplicate at the tool seam
// because the LLM cannot be trusted to notice it already issued the exact
// same mutating call.
//
// Design (scoped to what a coding agent needs):
//   - Only MUTATING tools participate (read-only tools are exempt entirely).
//     MCP tools (mcp__*) are conservatively treated as mutating — they proxy
//     external side effects (create PR, send message, provision infra).
//   - Fingerprint = SHA-256(tool name + canonical args + workspace epoch).
//     The epoch increments whenever a file-mutating tool succeeds, so the
//     classic verify loop (edit file → re-run same test command) is never
//     suppressed, while a timeout-retry double `git push` / double
//     `gh pr create` (no interleaved edit) is.
//   - Only suppresses when the prior identical call SUCCEEDED within the TTL
//     window; error results are never cached (retrying a failure is normal).
//   - The suppressed result replays the prior output prefixed with an
//     explicit notice — an advisory, not a refusal, so the model loses no
//     information and can override by varying arguments or waiting.
//
// Kill switch: GGCODE_TOOL_DEDUP=0 disables the ledger entirely.

const (
	toolDedupTTL        = 60 * time.Second
	toolDedupMaxEntries = 128
)

// mutatingToolNames: tools whose re-execution can duplicate real side effects.
var mutatingToolNames = map[string]bool{
	"run_command":      true,
	"start_command":    true,
	"write_file":       true,
	"edit_file":        true,
	"multi_file_write": true,
	"notebook_edit":    true,
	"file_ops":         true,
	"git_add":          true,
	"git_commit":       true,
	"git_revert":       true,
	"git_tag":          true,
	"git_stash":        true,
	"git_reset":        true,
	"git_checkout":     true,
	"git_remote":       true,
	"cron_create":      true,
	"cron_delete":      true,
	"cron_update":      true,
	"cron_pause":       true,
	"cron_resume":      true,
	"im":               true,
	"delegate":         true,
	"task_create":      true,
	"task_update":      true,
	"task_stop":        true,
	"browser":          true,
	"open":             true,
}

// fileMutatingTools: mutating tools that change workspace file state; their
// success bumps the workspace epoch (see fingerprint rationale above).
var fileMutatingTools = map[string]bool{
	"write_file":       true,
	"edit_file":        true,
	"multi_file_write": true,
	"notebook_edit":    true,
	"file_ops":         true,
}

type toolDedupEntry struct {
	fingerprint string
	at          time.Time
	result      tool.Result
}

type toolDedupLedger struct {
	mu    sync.Mutex
	ttl   time.Duration
	epoch uint64
	count int
	table map[string]toolDedupEntry
}

func newToolDedupLedger() *toolDedupLedger {
	return &toolDedupLedger{
		ttl:   toolDedupTTL,
		table: make(map[string]toolDedupEntry, 8),
	}
}

func (a *Agent) dedupLedger() *toolDedupLedger {
	a.toolDedupOnce.Do(func() {
		if os.Getenv("GGCODE_TOOL_DEDUP") == "0" {
			a.toolDedup = nil
			return
		}
		a.toolDedup = newToolDedupLedger()
	})
	return a.toolDedup
}

// isMutatingTool reports whether the named tool can duplicate side effects
// when re-executed. MCP tools are treated as mutating by name prefix.
func isMutatingTool(name string) bool {
	if mutatingToolNames[name] {
		return true
	}
	return strings.HasPrefix(name, "mcp__")
}

// suppressDuplicate returns a non-nil result when this exact mutating call
// already succeeded within the TTL window; the prior result is replayed with
// an advisory header instead of re-executing the side effect.
func (l *toolDedupLedger) suppressDuplicate(name, args string) *tool.Result {
	if l == nil || !isMutatingTool(name) {
		return nil
	}
	fp := l.fingerprint(name, args)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneLocked(time.Now())
	e, ok := l.table[fp]
	if !ok {
		return nil
	}
	notice := fmt.Sprintf(
		"[suppressed-as-duplicate] Identical mutating tool call %q already executed %ds ago and returned success; its output is replayed below instead of re-executing the side effect. If repeating is intentional, change the arguments or wait before retrying.\n\n",
		name, int(time.Since(e.at).Seconds()))
	replayed := notice + e.result.Content
	debug.Log("agent", "tool-dedup: suppressed duplicate mutating call %s (%ds old)", name, int(time.Since(e.at).Seconds()))
	return &tool.Result{Content: replayed, IsError: false}
}

// record caches a successful mutating call so an identical later call within
// the TTL can be suppressed. Error results are never recorded — retrying a
// failed mutating call is normal and often necessary.
func (l *toolDedupLedger) record(name, args string, res tool.Result) {
	if l == nil || !isMutatingTool(name) || res.IsError {
		return
	}
	now := time.Now()
	fp := l.fingerprint(name, args)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneLocked(now)
	if l.count >= toolDedupMaxEntries {
		return // bounded memory; oldest entries already pruned by TTL
	}
	l.table[fp] = toolDedupEntry{fingerprint: fp, at: now, result: res}
	l.count++
	if fileMutatingTools[name] {
		l.epoch++ // invalidate command fingerprints after workspace file changes
	}
}

// fingerprint = tool name + args + workspace epoch, hashed. The epoch term
// makes run_command fingerprints stale once files changed (verify loops stay
// live) while catching uninterrupted duplicate retries.
func (l *toolDedupLedger) fingerprint(name, args string) string {
	h := sha256.Sum256([]byte(name + "\x00" + args + "\x00" + fmt.Sprint(l.epoch)))
	return hex.EncodeToString(h[:])
}

// pruneLocked drops expired entries; caller holds mu.
func (l *toolDedupLedger) pruneLocked(now time.Time) {
	for fp, e := range l.table {
		if now.Sub(e.at) > l.ttl {
			delete(l.table, fp)
			l.count--
		}
	}
}
