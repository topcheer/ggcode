package tui

import (
	"regexp"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/harness"
	"github.com/topcheer/ggcode/internal/knight"
	"github.com/topcheer/ggcode/internal/mcp"
	"github.com/topcheer/ggcode/internal/plugin"
	"github.com/topcheer/ggcode/internal/update"
)

// streamMsg wraps a string from the agent goroutine.
type streamMsg string

// doneMsg signals generation is complete.
type doneMsg struct{}

// errMsg signals an error.
type errMsg struct{ err error }

type startupReadyMsg struct{}

type harnessRunResultMsg struct {
	Summary *harness.RunSummary
	Err     error
}

type harnessRunProgressMsg struct {
	TaskID    string
	Activity  string
	Detail    string
	LogPath   string
	LogChunk  string
	LogOffset int64
}

type harnessPanelAutoRefreshMsg struct{}

type harnessContextSuggestionsMsg struct {
	Contexts []harness.ContextConfig
	Err      error
}

type harnessInitResultMsg struct {
	Result *harness.InitResult
	Err    error
}

type projectMemoryLoadedMsg struct {
	Content string
	Files   []string
	Err     error
}

type subAgentUpdateMsg struct{}

type skillsChangedMsg struct{}

var ansiChunkPattern = regexp.MustCompile(`\[[0-9;?<>=]*[A-Za-z~]|\[<\d+(?:;\d+){0,2}[A-Za-zmM]`)

// toolStatusMsg wraps a tool status update.
type toolStatusMsg ToolStatusMsg

// statusMsg updates the status bar display.
type statusMsg struct {
	Activity  string // current activity description
	ToolName  string
	ToolArg   string
	ToolCount int
}

type agentStreamMsg struct {
	RunID int
	Text  string
}

type agentDoneMsg struct {
	RunID int
}

type agentErrMsg struct {
	RunID int
	Err   error
}

type agentToolStatusMsg struct {
	RunID int
	ToolStatusMsg
}

// agentToolBatchMsg delivers a batch of tool status + status bar updates
// in a single message, reducing event-loop saturation from burst tool events.
type agentToolBatchMsg struct {
	RunID      int
	StatusMsgs []agentStatusMsg
	ToolMsgs   []agentToolStatusMsg
}

type agentStatusMsg struct {
	RunID int
	statusMsg
}

type agentRoundProgressMsg struct {
	RunID int
	Text  string
}

type agentRoundSummaryMsg struct {
	RunID         int
	Text          string
	ToolCalls     int
	ToolSuccesses int
	ToolFailures  int
}

type agentAskUserMsg struct {
	RunID int
	Text  string
}

type agentInterruptMsg struct {
	RunID int
	Text  string
}

type knightTaskResultMsg struct {
	Goal   string
	Result knight.TaskResult
	Err    error
}

// setProgramMsg is sent via program.Send so the model copy inside Bubble Tea's
// event loop gets the real *tea.Program reference (NewProgram copies the model).
type setProgramMsg struct {
	Program *tea.Program
}

// inputDrainEndMsg signals the end of the startup input drain window.
type inputDrainEndMsg struct{}

type mcpServersMsg struct {
	Servers []plugin.MCPServerInfo
}

type deviceCodeInfo struct {
	serverName string
	userCode   string
	verifyURL  string
}

type mcpOAuthStartMsg struct {
	serverName     string
	authorizeURL   string
	handler        *mcp.OAuthHandler
	openErr        error
	err            error
	deviceUserCode string // set when using device flow
}

type mcpOAuthResultMsg struct {
	serverName string
	err        error
}

type updateCheckResultMsg struct {
	Result update.CheckResult
	Err    error
}

type updatePrepareResultMsg struct {
	Prepared update.PreparedUpdate
	Err      error
}

type updateCheckTickMsg struct{}
