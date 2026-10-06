package permission

import (
	"context"
	"encoding/json"
	"errors"
)

// Decision represents the outcome of a permission check.
type Decision int

// DecisionFromContext maps a context error observed after ctx.Done() to
// the matching non-decision approval outcome (#3370). A deadline means the
// prompt expired; anything else (user interrupt, run cancelled, bridge
// teardown) is a cancellation. Callers should only invoke this inside a
// ctx.Done() branch.
func DecisionFromContext(err error) Decision {
	if errors.Is(err, context.DeadlineExceeded) {
		return Timeout
	}
	return Cancelled
}

// IsNonDecision reports whether d represents an approval that ended
// without any user decision (timeout or cancellation) - the user must not
// be attributed with these outcomes anywhere downstream (#3370).
func (d Decision) IsNonDecision() bool {
	return d == Timeout || d == Cancelled
}

const (
	Allow Decision = iota
	Deny
	Ask
	// Timeout and Cancelled are approval-OUTCOME values (#3370): they are
	// returned by approval handlers when the user never made a decision
	// (prompt expired / run interrupted / request displaced). policy.Check
	// never returns them. Downstream consumers must treat them as
	// fail-closed (tool does not run) but must NOT attribute them to the
	// user (audit as ask_timeout, no approval-memory sample, no throttle
	// count) - a timeout is not a denial.
	Timeout
	Cancelled
)

func (d Decision) String() string {
	switch d {
	case Allow:
		return "allow"
	case Deny:
		return "deny"
	case Timeout:
		return "timeout"
	case Cancelled:
		return "cancelled"
	default:
		return "ask"
	}
}

// PermissionPolicy determines whether a tool call needs user approval.
type PermissionPolicy interface {
	// Check returns the decision for a tool call.
	Check(toolName string, input json.RawMessage) (Decision, error)

	// Mode returns the current permission mode.
	Mode() PermissionMode

	// IsDangerous returns true if the command/operation is inherently dangerous,
	// regardless of the tool-level policy. Used for run_command specifically.
	IsDangerous(command string) bool

	// BlocksAutoApprove reports whether a tool call must NOT be auto-approved
	// from learned approval memory (#1281): dangerous commands and network
	// exfiltration always need a human, even when the pattern was learned.
	BlocksAutoApprove(toolName string, input json.RawMessage) bool

	// AllowedPath returns true if the given file path is within the sandbox.
	AllowedPath(path string) bool

	// AllowedPathForTool returns true if the given path is within the sandbox
	// for the specific file tool being executed.
	AllowedPathForTool(toolName, path string) bool

	// SetOverride allows runtime modification of per-tool policy (e.g., 'a' key in TUI).
	SetOverride(toolName string, decision Decision)

	// AllowCommandPattern adds a fine-grained command-level allow rule.
	// Only applies to command tools (run_command, start_command, etc.) in supervised mode.
	AllowCommandPattern(pattern string)
}
