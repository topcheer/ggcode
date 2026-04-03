package permission

import "encoding/json"

// Decision represents the outcome of a permission check.
type Decision int

const (
	Allow Decision = iota
	Deny
	Ask
)

func (d Decision) String() string {
	switch d {
	case Allow:
		return "allow"
	case Deny:
		return "deny"
	default:
		return "ask"
	}
}

// PermissionPolicy determines whether a tool call needs user approval.
type PermissionPolicy interface {
	// Check returns the decision for a tool call.
	Check(toolName string, input json.RawMessage) (Decision, error)

	// IsDangerous returns true if the command/operation is inherently dangerous,
	// regardless of the tool-level policy. Used for run_command specifically.
	IsDangerous(command string) bool

	// AllowedPath returns true if the given file path is within the sandbox.
	AllowedPath(path string) bool

	// SetOverride allows runtime modification of per-tool policy (e.g., 'a' key in TUI).
	SetOverride(toolName string, decision Decision)
}
