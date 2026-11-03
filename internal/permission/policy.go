package permission

import (
	"context"
	"encoding/json"
	"errors"
)

type Decision int

func DecisionFromContext(err error) Decision {
	if errors.Is(err, context.DeadlineExceeded) {
		return Timeout
	}
	return Cancelled
}

func (d Decision) IsNonDecision() bool {
	return d == Timeout || d == Cancelled
}

const (
	Allow Decision = iota
	Deny
	Ask

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

type PermissionPolicy interface {
	Check(toolName string, input json.RawMessage) (Decision, error)

	Mode() PermissionMode

	IsDangerous(command string) bool

	BlocksAutoApprove(toolName string, input json.RawMessage) bool

	AllowedPath(path string) bool

	AllowedPathForTool(toolName, path string) bool

	SetOverride(toolName string, decision Decision)

	AllowCommandPattern(pattern string)
}
