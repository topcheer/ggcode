package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/audit"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/tool"
)

// Trace guard: self-protection for the audit ledger.
//
// Background (arXiv:2609.30266, "LLM Agents Can Easily Tamper With Their Own
// Traces", 2026-09): every tested local coding-agent harness except one
// allowed the agent to delete its own execution traces (audit logs, session
// transcripts) through its ordinary tools, without any guardrail firing -
// and trace deletion is also what a model does naturally when it tries to
// improve its own rewards. The paper's advice is to keep trace logging
// outside the agent's control.
//
// ggcode's tamper-EVIDENT ledger (internal/audit) detects edits, reorders
// and un-anchored tail truncation - but only if the file still exists. A
// plain `rm` of the ledger leaves nothing to verify, which is exactly the
// failure the paper demonstrates. This guard closes the direct path: while
// a ledger is enabled, the single tool choke point (Agent.safeExecute)
// rejects any mutating tool call whose arguments reference the ledger file
// or its .head anchor, and seals the rejected attempt into the ledger
// itself as audit.StatusInvalid - so the tamper attempt becomes part of the
// tamper-evident record it was trying to destroy.
//
// Scope and honest limits (tamper-evident baseline, not tamper-PROOF):
//   - Structured coverage: the raw arguments of every tool call are scanned
//     for the protected paths, so file_ops, write_file, edit_file,
//     run_command (`rm ...`), and any future tool are covered uniformly -
//     the check sits above individual tools, in the harness.
//   - Path forms: exact path, ~/.tilde form, Clean-ed, and suffix matches
//     (relative invocations from a matching working directory) are caught.
//   - NOT caught: indirect shell evasion (obfuscated paths, variables,
//     `find -delete` on the parent directory, etc.). Those remain covered
//     only by the ledger's tamper-evidence plus external anchoring
//     (SIEM/WORM), which is the operator's responsibility - same boundary
//     the ledger package documents. The guard's job is to make the direct,
//     ordinary-tools path fail loudly and land in the ledger.
//   - Session transcripts live in the TUI-owned session store and are
//     event-sourced append-only JSONL; they are out of scope here.

// traceGuardDenyMsg is returned to the model. It names the boundary so the
// model can explain the refusal to the user instead of retrying variants.
const traceGuardDenyMsg = "blocked by trace-integrity guard: this action would modify or delete the agent's own audit ledger (%s), which records this session's tool actions for governance/compliance. The ledger, its .head anchor, and the audit record of this blocked attempt cannot be altered through tools. If the file must be removed for legitimate operational reasons, do it outside the agent session."

// A mutating tool call whose arguments reference a protected path is rejected
// before execution; the attempt itself is sealed into the ledger with
// audit.StatusInvalid by the caller.

// protectedPaths returns the filesystem paths the guard defends while the
// ledger is enabled: the ledger JSONL and its .head anchor sidecar.
func protectedPaths(ledgerPath string) []string {
	return []string{ledgerPath, ledgerPath + ".head"}
}

// traceGuardCheck reports whether a tool call with the given raw JSON args
// targets a protected trace path, and if so returns the denial message.
// Inert (always false) when no ledger is active.
func (a *Agent) traceGuardCheck(name string, args json.RawMessage) (string, bool) {
	st := a.auditLedger
	if st == nil || st.ledger == nil || st.path == "" {
		return "", false
	}
	argStr := string(args)
	if argStr == "" {
		return "", false
	}
	for _, p := range protectedPaths(st.path) {
		if argsReferencePath(argStr, p) {
			debug.Log("agent", "[trace-guard] blocked %s: arguments reference protected ledger path %s", name, p)
			return traceGuardDenyMsg, true
		}
	}
	return "", false
}

// argsReferencePath reports whether the raw argument string references the
// given absolute path. It matches:
//   - the path verbatim (handles JSON escaping of '~' and '/' cheaply: none
//     needed, both are JSON-safe)
//   - the ~-contracted form (args written as ~/.ggcode/audit.jsonl)
//   - any suffix form "<dir>/audit.jsonl" or "<dir>/audit.jsonl.head"
//     (relative invocations, or basename-only arguments from a matching cwd)
//
// Suffix matching is anchored on a path separator so an unrelated file such
// as "myaudit.jsonl" never trips the guard.
func argsReferencePath(args, path string) bool {
	if strings.Contains(args, path) {
		return true
	}
	home, err := os.UserHomeDir()
	if err == nil && strings.HasPrefix(path, home+string(filepath.Separator)) {
		if strings.Contains(args, "~"+path[len(home):]) {
			return true
		}
	}
	base := filepath.Base(path)
	sep := string(filepath.Separator)
	if strings.Contains(args, sep+base) {
		return true
	}
	return false
}

// enforceTraceGuard runs the guard at the safeExecute choke point. A denial
// returns an error tool.Result and - critically - seals the attempt into
// the ledger it was trying to attack, so the tamper-evident chain documents
// the attempt even though the action never executed.
func (a *Agent) enforceTraceGuard(name string, args json.RawMessage, dur time.Duration) (tool.Result, bool) {
	reason, denied := a.traceGuardCheck(name, args)
	if !denied {
		return tool.Result{}, false
	}
	a.auditToolResult(name, args, audit.StatusInvalid, "trace-guard: "+reason, dur)
	return tool.Result{Content: reason, IsError: true}, true
}
