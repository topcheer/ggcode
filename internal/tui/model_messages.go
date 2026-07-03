package tui

import (
	tea "charm.land/bubbletea/v2"
	"time"

	"github.com/topcheer/ggcode/internal/harness"
	"github.com/topcheer/ggcode/internal/knight"
	"github.com/topcheer/ggcode/internal/mcp"
	"github.com/topcheer/ggcode/internal/metrics"
	"github.com/topcheer/ggcode/internal/plugin"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
	"github.com/topcheer/ggcode/internal/swarm"
	"github.com/topcheer/ggcode/internal/update"
)

// streamMsg wraps a string from the agent goroutine.
type streamMsg string

// doneMsg signals generation is complete.
type doneMsg struct{}

// knightTaskEventMsg is sent by the Knight background agent to display
// task start/complete notifications in the chat area as system messages.
type knightTaskEventMsg struct {
	TaskName string
	Report   string // non-empty = completed, empty = started
	Duration time.Duration
}

// errMsg signals an error.
type errMsg struct{ err error }

// compactResultMsg is sent by /compact when summarization completes.
type compactResultMsg struct {
	text string // success message
	err  string // error message (mutually exclusive with text)
}

type sessionResumeLoadedMsg struct {
	requestedID string
	session     *session.Session
	err         error
}

type tunnelPublishCurrentSessionMsg struct {
	reset bool
}

type sessionUsageMsg struct {
	Usage provider.TokenUsage
}

type sessionMetricMsg struct {
	Metric metrics.MetricEvent
}

// initPromptCheckMsg carries the result of the startup GGCODE.md existence check.
type initPromptCheckMsg struct {
	needsInit bool
	target    string // path to GGCODE.md that would be created
}

type autoRunCheckResultMsg struct {
	Text        string
	DisplayText string
	Result      *harness.AutoRunResult
	Err         error
}

type harnessRunResultMsg struct {
	Summary    *harness.RunSummary
	Err        error
	CTA        harness.CTAAction
	CTAMessage string
}

// harnessReviewResultMsg carries the result of a one-key review approve action.
type harnessReviewResultMsg struct {
	Task   *harness.Task
	TaskID string
	Err    error
}

// harnessPromoteResultMsg carries the result of a one-key promote action.
type harnessPromoteResultMsg struct {
	Task   *harness.Task
	TaskID string
	Err    error
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

type subAgentUpdateMsg struct {
	AgentID string // empty for general update
}

type subAgentTunnelStreamTextMsg struct {
	AgentID string
	Text    string
}

type subAgentTunnelReasoningMsg struct {
	AgentID string
	Text    string
}

type subAgentTunnelToolCallMsg struct {
	AgentID     string
	ToolID      string
	ToolName    string
	DisplayName string
	Args        string
	Detail      string
}

type subAgentTunnelToolResultMsg struct {
	AgentID     string
	ToolID      string
	ToolName    string
	DisplayName string
	Detail      string
	Result      string
	IsError     bool
}

type swarmTunnelEventMsg struct {
	Event swarm.Event
}

// subAgentDoneMsg is sent when a sub-agent or swarm teammate completes its task.
// It triggers a system message in the chat and optionally wakes the main agent.
type subAgentDoneMsg struct {
	AgentID   string
	AgentName string
	IsError   bool
	Kind      string // "subagent" or "teammate"
}

type subAgentFollowRefreshMsg struct{}
type followGraceTickMsg struct{}

// systemNotifyMsg displays a provider system notification (e.g. retry status)
// as a system message in the chat area.
type systemNotifyMsg struct {
	Text string
}

type skillsChangedMsg struct{}

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

type agentReasoningMsg struct {
	RunID int
	Text  string
}

type agentReasoningDoneMsg struct{}

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

type knightProjectProposalResultMsg struct {
	Goal     string
	Proposal knight.ProjectImprovementProposal
	Result   knight.TaskResult
	Err      error
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

// gitBranchTickMsg is sent every 2 seconds to refresh the cached git branch
// shown in the sidebar, avoiding disk I/O on every View() render.
type gitBranchTickMsg struct{}

// imPanelRefreshMsg is sent every 2 seconds when an IM panel is open
// and the selected adapter is in a non-terminal state (pairing, connecting, etc.)
// This ensures dynamic content like WhatsApp QR codes appears without manual refresh.
type imPanelRefreshMsg struct{}

// webchatUserMsg is sent by the webui TUIChatBridge to inject a webchat
// message into the TUI event loop. The TUI handles it like a normal
// user input submission.
type webchatUserMsg struct {
	Text string
}

// webuiReadyMsg is sent when the webui HTTP server is ready. The TUI
// displays the URL (with token fragment) as a system message in the chat area.
type webuiReadyMsg struct {
	Addr  string
	Token string
}

// harnessPanelRefreshResultMsg carries the result of an async harness panel
// data load. The handler applies the data to the harness panel state.
type harnessPanelRefreshResultMsg struct {
	Err      string
	Project  *harness.Project
	Cfg      *harness.Config
	Doctor   *harness.DoctorReport
	Monitor  *harness.MonitorReport
	Contexts *harness.ContextReport
	Tasks    []*harness.Task
	Inbox    *harness.OwnerInbox
	Review   []*harness.Task
	Promote  []*harness.Task
	Release  *harness.ReleasePlan
	Rollouts []*harness.ReleaseWavePlan
}

// knightStartupHintMsg is sent once at startup to show a Knight-related hint
// (e.g. another instance holds the lock) in the chat area.
type knightStartupHintMsg struct {
	Hint string
}

type tmuxStartupSetupMsg struct {
	Layout string
}
