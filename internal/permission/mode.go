package permission

import (
	"strings"
)

// PermissionMode controls how the agent handles tool permissions.
type PermissionMode int

const (
	// SupervisedMode asks for user confirmation on all tool calls (default).
	SupervisedMode PermissionMode = iota
	// PlanMode allows read-only tools, denies write/execute tools automatically.
	PlanMode
	// AutoMode allows safe operations, denies dangerous ones automatically (no prompts).
	AutoMode
)

func (m PermissionMode) String() string {
	switch m {
	case SupervisedMode:
		return "supervised"
	case PlanMode:
		return "plan"
	case AutoMode:
		return "auto"
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
	default:
		return SupervisedMode
	}
}

// Next returns the next mode in the cycle: supervised → plan → auto → supervised.
func (m PermissionMode) Next() PermissionMode {
	switch m {
	case SupervisedMode:
		return PlanMode
	case PlanMode:
		return AutoMode
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
