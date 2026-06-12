package agentruntime

import toolpkg "github.com/topcheer/ggcode/internal/tool"

type ApprovalRequest struct {
	ID       string
	ToolName string
	Input    string
}

type AskUserRequest struct {
	ID      string
	Request toolpkg.AskUserRequest
}

type PendingMessage[T any] struct {
	Text   string
	Hidden bool
	Meta   T
}
