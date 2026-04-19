package permission

import (
	"strings"
)

// PermissionMode controls how the agent handles tool permissions.
type PermissionMode int

const (
	// SupervisedMode respects explicit per-tool rules and asks for anything unspecified.
	SupervisedMode PermissionMode = iota
	// PlanMode allows a strict read-only subset and denies writes/commands automatically.
	PlanMode
	// AutoMode allows safe operations and denies dangerous ones automatically.
	AutoMode
	// BypassMode allows almost everything automatically and only asks on critical cases.
	BypassMode
	// AutopilotMode uses bypass permissions and keeps going when the model asks the user to decide.
	AutopilotMode
)

func (m PermissionMode) String() string {
	switch m {
	case SupervisedMode:
		return "supervised"
	case PlanMode:
		return "plan"
	case AutoMode:
		return "auto"
	case BypassMode:
		return "bypass"
	case AutopilotMode:
		return "autopilot"
	default:
		return "supervised"
	}
}

// ParsePermissionMode parses a string to PermissionMode (case-insensitive).
func ParsePermissionMode(s string) PermissionMode {
	switch strings.ToLower(s) {
	case "plan":
		return PlanMode
	case "auto":
		return AutoMode
	case "bypass":
		return BypassMode
	case "autopilot":
		return AutopilotMode
	default:
		return SupervisedMode
	}
}

// Next returns the next mode in the cycle: supervised → plan → auto → bypass → autopilot → supervised.
func (m PermissionMode) Next() PermissionMode {
	switch m {
	case SupervisedMode:
		return PlanMode
	case PlanMode:
		return AutoMode
	case AutoMode:
		return BypassMode
	case BypassMode:
		return AutopilotMode
	default:
		return SupervisedMode
	}
}

// IsReadOnlyTool returns true if the tool is safe for Plan mode (read-only).
func IsReadOnlyTool(name string) bool {
	switch name {
	case "read_file", "list_directory", "search_files", "grep":
		return true
	}
	return false
}
