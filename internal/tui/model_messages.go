package tui

import (
	tea "charm.land/bubbletea/v2"
	"time"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/knight"
	"github.com/topcheer/ggcode/internal/mcp"
	"github.com/topcheer/ggcode/internal/metrics"
	"github.com/topcheer/ggcode/internal/plugin"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
	"github.com/topcheer/ggcode/internal/swarm"
	"github.com/topcheer/ggcode/internal/update"
)

type verifyProgressMsg struct {
	text string
}

type verifyResultMsg struct {
	result agent.VerifyResult
}

type toolProgressMsg struct {
	toolID   string
	toolName string
	output   string
}

type streamMsg string

type doneMsg struct{}

type knightTaskEventMsg struct {
	TaskName string
	Report   string
	Duration time.Duration
}

type errMsg struct{ err error }

type compactResultMsg struct {
	text string
	err  string
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
	Usage  provider.TokenUsage
	Source string
}

type sessionMetricMsg struct {
	Metric metrics.MetricEvent
}

type initPromptCheckMsg struct {
	needsInit bool
	target    string
}

type projectMemoryLoadedMsg struct {
	Content string
	Files   []string
	Err     error
}

type subAgentUpdateMsg struct {
	AgentID string
}

type subAgentSystemMsg struct {
	AgentID string
	Text    string
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

type subAgentDoneMsg struct {
	AgentID   string
	AgentName string
	IsError   bool
	Kind      string
}

type subAgentFollowRefreshMsg struct{}
type followGraceTickMsg struct{}

type subAgentsCancelDoneMsg struct{}

type systemNotifyMsg struct {
	Text    string
	ItemID  string
	Replace bool
}

type skillsChangedMsg struct{}

type toolStatusMsg ToolStatusMsg

type statusMsg struct {
	Activity  string
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

type agentTurnDoneMsg struct{}

type agentDoneMsg struct {
	RunID int
}

type agentErrMsg struct {
	RunID int
	Err   error
}

type restorePendingImagesMsg struct {
	RunID  int
	Images []imageAttachedMsg
}

type agentToolStatusMsg struct {
	RunID int
	ToolStatusMsg
}

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

type setProgramMsg struct {
	Program *tea.Program
}

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
	deviceUserCode string
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

type gitBranchTickMsg struct{}

type imPanelRefreshMsg struct{}

type webchatUserMsg struct {
	Text string

	Images []provider.ContentBlock
}

type webuiReadyMsg struct {
	Addr  string
	Token string
}

type knightStartupHintMsg struct {
	Hint string
}

type tmuxStartupSetupMsg struct {
	Layout string
}
