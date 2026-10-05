package permission

import "encoding/json"

// Decision represents the outcome of a permission check.
type Decision int

const (
	Allow Decision = iota
	Deny
	Ask
	// DenyTimeout (#3370): the approval gate expired, the run context was
	// cancelled, or the client disconnected BEFORE any user decision.
	// Execution outcome equals Deny, but it is NOT a user rejection -
	// audit, approval memory, and the ask throttle must not attribute it
	// to the user.
	DenyTimeout
	// DenyDisplaced (#3370): a newer approval request displaced this one
	// (single-slot UI), so the user never saw a prompt for it.
	DenyDisplaced
)

func (d Decision) String() string {
	switch d {
	case Allow:
		return "allow"
	case Deny:
		return "deny"
	case DenyTimeout:
		return "deny_timeout"
	case DenyDisplaced:
		return "deny_displaced"
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
