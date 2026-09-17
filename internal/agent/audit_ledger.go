package agent

import (
	"encoding/json"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/audit"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/toolreplay"
)

// Harness integration for the tamper-evident audit ledger
// (internal/audit, the "cryptographic governance audit trail" pattern).
//
// The ledger records every action that flows through the single tool
// choke point (Agent.safeExecute) into an append-only, SHA-256 hash-chained
// JSONL file. Unlike ordinary logs, the chain makes edits, reorders, and
// un-anchored tail deletions detectable by re-running verification —
// the 2025-2026 agent-governance baseline for regulated environments
// (EU AI Act Article 12 logging, OWASP LLM06) and the audit half of the
// harness-engineering "inspectable execution history" story that the tool
// tape (GGCODE_TOOL_TAPE) covers for debugging.
//
// Opt-in via environment variable:
//
//	GGCODE_AUDIT_LEDGER=/path/to/audit.jsonl
//
// Design rules, mirroring the tool tape integration:
//   - Opt-in: unset env var keeps the field non-nil but inert (NewAgent
//     must initialize every pointer field, issue #341 guard).
//   - Never blocks: an append failure is a debug log line, never a tool
//     failure. An audit sink must not be able to take the agent down.
//   - Crash-safe: flush after every entry; the tail of an audit trail is
//     most valuable exactly when the session crashes or hangs.
//   - No raw inputs: only the canonical SHA-256 of the input JSON is
//     recorded (tool arguments can carry secrets; a governance ledger
//     proves what ran without duplicating sensitive payloads).
//   - Cancellations and preflight rejections are audited too — from a
//     governance perspective these are precisely the actions worth a
//     durable record (the tape deliberately skips cancellations; the
//     audit ledger deliberately does not).

// auditLedgerEnv is the environment variable that enables the audit ledger.
const auditLedgerEnv = "GGCODE_AUDIT_LEDGER"

// auditErrMax bounds the error summary written into a ledger entry. Error
// text is untrusted tool output; entries stay small and secret-light.
const auditErrMax = 300

// auditLedgerState holds the session-wide ledger. The state struct itself
// is always non-nil; ledger is nil only when the feature is off.
type auditLedgerState struct {
	mu     sync.Mutex
	ledger *audit.Ledger
	path   string
}

// newAuditLedgerState resolves the audit ledger from the environment.
// Always returns a non-nil state; any failure degrades to "off" with a
// debug log line, so enabling an audit sink can never prevent a session
// from starting.
func newAuditLedgerState() *auditLedgerState {
	path := strings.TrimSpace(os.Getenv(auditLedgerEnv))
	if path == "" {
		return &auditLedgerState{}
	}
	lg, err := audit.Open(path, "")
	if err != nil {
		debug.Log("agent", "[audit-ledger] disabled, open failed: %v", err)
		return &auditLedgerState{}
	}
	debug.Log("agent", "[audit-ledger] enabled, ledger file: %s", path)
	return &auditLedgerState{ledger: lg, path: path}
}

// auditToolResult seals a completed (or cancelled, or rejected) tool action
// into the ledger. status is one of the audit.Status* values; errMsg is an
// already-short summary of why the action did not succeed cleanly.
func (a *Agent) auditToolResult(name string, args json.RawMessage, status, errMsg string, dur time.Duration) {
	st := a.auditLedger
	if st == nil || st.ledger == nil {
		return
	}
	if errMsg != "" && len(errMsg) > auditErrMax {
		errMsg = errMsg[:auditErrMax]
	}
	ev := audit.Event{
		Tool:       name,
		Status:     status,
		InputHash:  toolreplay.HashInput(args),
		DurationMS: dur.Milliseconds(),
		Err:        errMsg,
	}
	if a != nil {
		ev.Session = a.SessionID()
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if _, err := st.ledger.Append(ev); err != nil {
		debug.Log("agent", "[audit-ledger] append failed for %s: %v", name, err)
	}
}

// auditToolExecution derives the audit status from a completed execution:
// a Go-level error or an IsError result both count as StatusError (with a
// bounded summary), everything else is StatusOK.
func (a *Agent) auditToolExecution(name string, args json.RawMessage, result tool.Result, err error, dur time.Duration) {
	switch {
	case err != nil:
		a.auditToolResult(name, args, audit.StatusError, err.Error(), dur)
	case result.IsError:
		a.auditToolResult(name, args, audit.StatusError, result.Content, dur)
	default:
		a.auditToolResult(name, args, audit.StatusOK, "", dur)
	}
}

// auditAnchor writes the head anchor sidecar, best-effort. Callers (session
// end) must treat failure as non-fatal.
func (a *Agent) auditAnchor() {
	st := a.auditLedger
	if st == nil || st.ledger == nil {
		return
	}
	if err := st.ledger.Anchor(); err != nil {
		debug.Log("agent", "[audit-ledger] anchor failed: %v", err)
	}
}
