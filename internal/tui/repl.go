package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/term"

	"github.com/topcheer/ggcode/internal/a2a"
	"github.com/topcheer/ggcode/internal/acpclient"
	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/agentruntime"
	"github.com/topcheer/ggcode/internal/checkpoint"
	"github.com/topcheer/ggcode/internal/commands"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/cron"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/im"
	"github.com/topcheer/ggcode/internal/knight"
	"github.com/topcheer/ggcode/internal/markdown"
	"github.com/topcheer/ggcode/internal/memory"
	"github.com/topcheer/ggcode/internal/metrics"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/plugin"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/restart"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/session"
	"github.com/topcheer/ggcode/internal/subagent"
	"github.com/topcheer/ggcode/internal/swarm"
	"github.com/topcheer/ggcode/internal/task"
	"github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/tui/cmdpane"
	"github.com/topcheer/ggcode/internal/update"
)

// REPL connects the agent to the TUI model.
type REPL struct {
	model               Model
	agent               *agent.Agent
	program             *tea.Program
	programSend         func(tea.Msg)
	planSwitcher        *replModeSwitcher
	store               session.Store
	resumeID            string
	sessionLock         *session.SessionLock
	core                *agentruntime.InteractiveRuntimeCore
	mcpMgr              *plugin.MCPManager
	commandMgr          *commands.Manager
	skillsChangedHook   func()
	imManager           *im.Manager
	projectMemoryLoader func() (string, []string, error)
	systemPromptBuilder func(task, agentType string) string // builds rich prompt for sub-agents
	webuiAddr           string                              // webui listen address
	webuiToken          string                              // webui auth token, displayed in URL fragment
	knightStartupHint   string                              // one-time hint shown at startup (e.g. lock conflict)
	metricCollector     *metrics.Collector
	metricCancel        context.CancelFunc
}

// NewREPL creates a new REPL with optional permission policy.
func NewREPL(a *agent.Agent, policy permission.PermissionPolicy) *REPL {
	m := NewModel(a, policy)
	r := &REPL{
		model: m,
		agent: a,
	}
	if a != nil {
		a.SetUsageHandler(r.recordSessionUsage)
		collectorCtx, collectorCancel := context.WithCancel(context.Background())
		r.metricCancel = collectorCancel
		r.metricCollector = metrics.NewCollector(collectorCtx, 256, func(ev metrics.MetricEvent) {
			r.recordMetric(ev)
		})
		a.SetMetricHandler(r.metricCollector.Emit)
		r.model.metricCollectorFlush = r.metricCollector.Flush
	}
	return r
}

// SetSessionStore sets the session persistence store.
func (r *REPL) SetSessionStore(s session.Store) {
	r.store = s
}

func (r *REPL) SessionUsageHandler() func(provider.TokenUsage) {
	return r.recordSessionUsage
}

// SetMCPServers passes MCP server info to the TUI model.
func (r *REPL) SetMCPServers(servers []MCPInfo) {
	r.model.SetMCPServers(servers)
}

// SetA2AHandler passes the A2A task handler so the sidebar can show remote tasks.
func (r *REPL) SetA2AHandler(h *a2a.TaskHandler) {
	r.model.SetA2AHandler(h)
}

func (r *REPL) SetMCPManager(mgr *plugin.MCPManager) {
	r.mcpMgr = mgr
	r.model.SetMCPManager(mgr)
}

// SetCore stores the runtime core reference for unified background service management.
func (r *REPL) SetCore(core *agentruntime.InteractiveRuntimeCore) {
	r.core = core
	if r.model.tunnelHost != nil {
		r.model.tunnelHost.Close()
	}
	r.model.tunnelHost = core.Tunnel
}

// SetResumeID sets the session ID to resume.
func (r *REPL) SetResumeID(id string) {
	r.resumeID = id
}

// SetSessionLock passes a pre-acquired session lock to the REPL.
// The REPL will release it on shutdown or restart.
func (r *REPL) SetSessionLock(lock *session.SessionLock) {
	r.sessionLock = lock
}

// SetConfig passes the config to the model for /model and /provider commands.
func (r *REPL) SetConfig(cfg *config.Config) {
	r.model.SetConfig(cfg)
}

// OnConfigProviderChanged is called by the config tool after a provider change.
// It sends a Bubble Tea message to update the TUI state and triggers background tasks.
func (r *REPL) OnConfigProviderChanged() {
	if r.model.config == nil {
		return
	}
	// Probe real context window in background (safe to call from any goroutine)
	r.model.startContextProbe()
	// Trigger Bubble Tea re-render so status bar and terminal title update
	r.sendTUI(providerChangedMsg{})
}

// providerChangedMsg triggers a UI refresh after config tool changes the provider.
type providerChangedMsg struct{}

func (r *REPL) sendTUI(msg tea.Msg) {
	if r.programSend != nil {
		r.programSend(msg)
	} else if r.program != nil {
		r.program.Send(msg)
	}
}

func (r *REPL) SetPluginManager(mgr *plugin.Manager) {
	r.model.SetPluginManager(mgr)
}

func (r *REPL) SetUpdateService(svc *update.Service) {
	r.model.SetUpdateService(svc)
}

func (r *REPL) SetCommandsManager(mgr *commands.Manager) {
	r.commandMgr = mgr
	r.model.SetCommandsManager(mgr)
}

func (r *REPL) SetSkillsChangedHook(hook func()) {
	r.skillsChangedHook = hook
}

func (r *REPL) SetIMManager(mgr *im.Manager) {
	r.imManager = mgr
	r.model.SetIMManager(mgr)
	if mgr != nil {
		mgr.SetBridge(newTUIIMBridge(func() *tea.Program { return r.program }))
	}
}

func (r *REPL) SetAutoMemory(am *memory.AutoMemory) {
	r.model.SetAutoMemory(am)
}

func (r *REPL) SetKnight(k *knight.Knight) {
	r.model.SetKnight(k)
}

// SetKnightStartupHint sets a one-time hint to show in the chat area at startup.
func (r *REPL) SetKnightStartupHint(hint string) {
	r.knightStartupHint = hint
}

// SetWebUIBridge sets the webui event broadcaster for forwarding agent
// events to webchat subscribers.
func (r *REPL) SetWebUIBridge(b WebUIEventBroadcaster) {
	r.model.webuiBridge = b
}

// InjectWebchatMessage sends a webchat user message into the TUI event loop.
// The message is handled like a normal user input — if the agent is idle,
// it starts a new run; if busy, it queues as a pending interruption.
func (r *REPL) InjectWebchatMessage(text string) {
	if r.program != nil {
		r.program.Send(webchatUserMsg{Text: text})
	}
}

// InjectRestart triggers a clean restart via the Bubble Tea event loop.
// This is the same mechanism used by IM /restart and the TUI /restart slash command.
func (r *REPL) InjectRestart() {
	if r.program != nil {
		r.program.Send(remoteRestartMsg{})
	}
}

func (r *REPL) recordSessionUsage(usage provider.TokenUsage) {
	if r.program != nil {
		r.program.Send(sessionUsageMsg{Usage: usage})
		return
	}
	r.model.recordSessionUsage(usage)
}

// recordMetric persists a metric event to the session JSONL.
// Called by the metrics collector goroutine (async, non-blocking for agent).
func (r *REPL) recordMetric(ev metrics.MetricEvent) {
	if r.program != nil {
		r.program.Send(sessionMetricMsg{Metric: ev})
		return
	}
	r.model.recordSessionMetric(ev)
}

// SetWebUIReadyAddr stores the webui address and auth token to be displayed
// in the TUI after startup. The actual program.Send happens in the startup
// goroutine alongside logoMsg to ensure the TUI is ready.
func (r *REPL) SetWebUIReadyAddr(addr, token string) {
	r.webuiAddr = addr
	r.webuiToken = token
}

// SetSystemPromptRebuilder sets a callback that rebuilds the full system prompt
// when skills or other dynamic parts change.
func (r *REPL) SetSystemPromptRebuilder(fn func() string) {
	r.model.SetSystemPromptRebuilder(fn)
}

func (r *REPL) SetProjectMemoryFiles(files []string) {
	r.model.SetProjectMemoryFiles(files)
}

func (r *REPL) SetProjectMemoryLoader(loader func() (string, []string, error)) {
	r.projectMemoryLoader = loader
	r.model.SetProjectMemoryLoading(loader != nil)
}

func (r *REPL) SetAutoMemoryFiles(files []string) {
	r.model.SetAutoMemoryFiles(files)
}

// SetCheckpointManager wires the checkpoint manager into the agent and REPL.
func (r *REPL) SetCheckpointManager(m *checkpoint.Manager) {
	r.agent.SetCheckpointManager(m)
	r.agent.SetDiffConfirm(func(ctx context.Context, filePath, diffText string) bool {
		return r.requestDiffConfirm(ctx, filePath, diffText)
	})
}

// SetSystemPromptBuilder sets the function used to build rich system prompts for sub-agents.
// Must be called before SetSubAgentManager.
func (r *REPL) SetSystemPromptBuilder(fn func(task, agentType string) string) {
	r.systemPromptBuilder = fn
}

// SetSubAgentManager wires the sub-agent manager and registers sub-agent tools.
func (r *REPL) SetSubAgentManager(mgr *subagent.Manager, prov provider.Provider, tools *tool.Registry) {
	r.model.SetSubAgentManager(mgr)

	factory := func(prov provider.Provider, t interface{}, systemPrompt string, maxTurns int) subagent.AgentRunner {
		return agent.NewAgent(prov, t.(*tool.Registry), systemPrompt, maxTurns)
	}

	tools.Register(tool.SpawnAgentTool{
		Manager:             mgr,
		Provider:            prov,
		Tools:               tools,
		AgentFactory:        factory,
		WorkingDir:          r.model.agent.WorkingDir(),
		OnUsage:             r.recordSessionUsage,
		SystemPromptBuilder: r.systemPromptBuilder,
	})
	tools.Register(tool.WaitAgentTool{Manager: mgr})
	tools.Register(tool.ListAgentsTool{Manager: mgr})

	// Notify TUI on live updates and completion.
	mgr.SetOnUpdate(func(sa *subagent.SubAgent) {
		r.sendProgramMsgs(subAgentUpdateMsg{AgentID: sa.ID})
	})
	mgr.SetOnComplete(func(sa *subagent.SubAgent) {
		r.sendProgramMsgs(
			subAgentUpdateMsg{AgentID: sa.ID},
			subAgentDoneMsg{
				AgentID:   sa.ID,
				AgentName: sa.Name,
				IsError:   sa.Status == subagent.StatusFailed,
				Kind:      "subagent",
			},
		)
	})
	mgr.SetOnStreamText(func(agentID, text string) {
		r.sendProgramMsgs(subAgentTunnelStreamTextMsg{AgentID: agentID, Text: text})
	})
	mgr.SetOnReasoning(func(agentID, text string) {
		r.sendProgramMsgs(subAgentTunnelReasoningMsg{AgentID: agentID, Text: text})
	})
	mgr.SetOnToolCall(func(agentID, toolID, toolName, displayName, args, detail string) {
		r.sendProgramMsgs(subAgentTunnelToolCallMsg{
			AgentID:     agentID,
			ToolID:      toolID,
			ToolName:    toolName,
			DisplayName: displayName,
			Args:        args,
			Detail:      detail,
		})
	})
	mgr.SetOnToolResult(func(agentID, toolID, toolName, displayName, detail, result string, isError bool) {
		r.sendProgramMsgs(subAgentTunnelToolResultMsg{
			AgentID:     agentID,
			ToolID:      toolID,
			ToolName:    toolName,
			DisplayName: displayName,
			Detail:      detail,
			Result:      result,
			IsError:     isError,
		})
	})

	// Start the background ticker that flushes accumulated stream
	// text/reasoning chunks at ~12.5 Hz instead of per-token (~50-100 Hz
	// per agent). Without this, 2+ concurrent sub-agents flood Bubble Tea's
	// event loop with 200-400 messages/second, causing severe TUI stutter.
	mgr.StartStreamBatcher()
}

// SetTaskManager wires the task manager and registers task tools.
func (r *REPL) SetTaskManager(mgr *task.Manager, tools *tool.Registry) {
	tools.Register(tool.TaskCreateTool{Manager: mgr})
	tools.Register(tool.TaskGetTool{Manager: mgr})
	tools.Register(tool.TaskListTool{Manager: mgr})
	tools.Register(tool.TaskUpdateTool{Manager: mgr})
	tools.Register(tool.TaskStopTool{Manager: mgr})
}

// SetTaskOutputTool registers the task_output tool for reading sub-agent results.
func (r *REPL) SetTaskOutputTool(mgr *subagent.Manager, tools *tool.Registry) {
	tools.Register(tool.TaskOutputTool{Provider: mgr})
}

// SetCronScheduler wires the cron scheduler and registers cron tools.
func (r *REPL) SetCronScheduler(s *cron.Scheduler, tools *tool.Registry) {
	s.SetEnqueue(func(prompt string) {
		if r.program != nil {
			r.program.Send(cronPromptMsg{Prompt: prompt})
		}
	})
	tools.Register(tool.CronCreateTool{Scheduler: s})
	tools.Register(tool.CronDeleteTool{Scheduler: s})
	tools.Register(tool.CronListTool{Scheduler: s})
}

// SetPlanModeTools registers plan mode tools with a mode switcher that
// updates both the Model's mode and the ConfigPolicy. The switcher
// remembers the previous mode so exit_plan_mode can restore it.
func (r *REPL) SetPlanModeTools(tools *tool.Registry) {
	switcher := &replModeSwitcher{model: &r.model}
	r.planSwitcher = switcher
	tools.Register(tool.EnterPlanModeTool{Switcher: switcher})
	tools.Register(tool.ExitPlanModeTool{Switcher: switcher, DefaultMode: permission.SupervisedMode})
}

// SetSendMessageTool registers the send_message tool for agent communication.
func (r *REPL) SetSendMessageTool(mgr *subagent.Manager, tools *tool.Registry) {
	tools.Register(tool.SendMessageTool{Manager: mgr})
}

// SetACPClientManager wires the ACP client manager for clean shutdown.
func (r *REPL) SetACPClientManager(mgr *acpclient.ClientManager) {
	r.model.acpClientMgr = mgr
	if mgr == nil {
		return
	}
	mgr.SetApprovalHandler(func(ctx context.Context, toolName string, input string) permission.Decision {
		if r.program == nil {
			return permission.Deny
		}
		resp := make(chan permission.Decision, 1)
		r.program.Send(ApprovalMsg{
			ToolName: toolName,
			Input:    input,
			Response: resp,
		})
		select {
		case d := <-resp:
			return d
		case <-ctx.Done():
			return permission.Deny
		}
	})
}

// SetSwarmManager wires the swarm manager and registers swarm tools.
func (r *REPL) SetSwarmManager(mgr *swarm.Manager, tools *tool.Registry) {
	r.model.swarmMgr = mgr

	tools.Register(tool.TeamCreateTool{Manager: mgr})
	tools.Register(tool.TeamDeleteTool{Manager: mgr})
	tools.Register(tool.TeammateSpawnTool{Manager: mgr})
	tools.Register(tool.TeammateListTool{Manager: mgr})
	tools.Register(tool.TeammateShutdownTool{Manager: mgr})
	tools.Register(tool.TeammateResultsTool{Manager: mgr})
	tools.Register(tool.SwarmTaskCreateTool{Manager: mgr})
	tools.Register(tool.SwarmTaskListTool{Manager: mgr})
	tools.Register(tool.SwarmTaskClaimTool{Manager: mgr})
	tools.Register(tool.SwarmTaskCompleteTool{Manager: mgr})

	// Re-register send_message with SwarmMgr so it can route to swarm teammates.
	tools.Unregister("send_message")
	tools.Register(tool.SendMessageTool{Manager: r.model.subAgentMgr, SwarmMgr: mgr})

	// Notify TUI on swarm state changes.
	// teammate_text events are high-frequency (one per streaming token).
	// We throttle them to ~2 Hz per teammate to avoid flooding Bubble Tea's
	// event loop with messages that trigger expensive snapshot operations.
	// Status-change events (tool_call, idle, etc.) are sent immediately.
	swarmTextThrottle := newTextThrottleMap(500 * time.Millisecond)

	mgr.SetOnUpdate(func(ev swarm.Event) {
		if r.program == nil && r.programSend == nil {
			return
		}
		msgs := []tea.Msg{swarmTunnelEventMsg{Event: ev}}
		switch ev.Type {
		case "teammate_text":
			// Throttle: at most one subAgentUpdateMsg per teammate per 500ms.
			if !swarmTextThrottle.Allow(ev.TeammateID) {
				r.sendProgramMsgs(msgs...)
				return
			}
			msgs = append(msgs, subAgentUpdateMsg{AgentID: ev.TeammateID})
		case "teammate_idle":
			if ev.Result != "" {
				msgs = append(msgs,
					subAgentUpdateMsg{AgentID: ev.TeammateID},
					subAgentDoneMsg{
						AgentID:   ev.TeammateID,
						AgentName: ev.TeammateName,
						IsError:   ev.Error != nil,
						Kind:      "teammate",
					},
				)
			}
		case "teammate_spawned", "teammate_working", "teammate_shutdown",
			"teammate_tool_call", "teammate_tool_result", "teammate_error":
			// Status-change events: send immediately so strip updates promptly.
			msgs = append(msgs, subAgentUpdateMsg{AgentID: ev.TeammateID})
		}
		r.sendProgramMsgs(msgs...)
	})

	// Start the background ticker that flushes accumulated teammate
	// text/reasoning at ~12.5 Hz instead of per-token (~50-100 Hz
	// per teammate). Same pattern as sub-agent stream batching.
	mgr.StartStreamBatcher()
}

func (r *REPL) sendProgramMsgs(msgs ...tea.Msg) {
	if len(msgs) == 0 {
		return
	}
	send := r.programSend
	if send == nil {
		if r.program == nil {
			return
		}
		send = r.program.Send
	}
	for _, msg := range msgs {
		send(msg)
	}
}

// replModeSwitcher implements tool.ModeSwitcher by delegating to the TUI Model.
type replModeSwitcher struct {
	model        *Model
	program      *tea.Program
	previousMode permission.PermissionMode
}

func (s *replModeSwitcher) SetMode(mode permission.PermissionMode) {
	// ConfigPolicy.SetMode is thread-safe (has its own mutex)
	if cp, ok := s.model.policy.(*permission.ConfigPolicy); ok {
		cp.SetMode(mode)
	}
	// Update Model.mode via program.Send for thread safety
	if s.program != nil {
		s.program.Send(modeChangeMsg{Mode: mode})
	}
}

// RememberMode saves the current mode as "previous" and returns what was saved.
// This is called by enter_plan_mode to remember the mode before switching.
func (s *replModeSwitcher) RememberMode(currentMode permission.PermissionMode) permission.PermissionMode {
	// Read the actual current mode from ConfigPolicy (thread-safe, always up-to-date)
	// rather than s.model.mode which may be stale (Bubble Tea copies the model).
	actualCurrent := currentMode // fallback to the argument
	if cp, ok := s.model.policy.(*permission.ConfigPolicy); ok {
		actualCurrent = cp.CurrentMode()
	}
	s.previousMode = actualCurrent
	return actualCurrent
}

// RestoreMode returns the remembered mode, or the given fallback.
func (s *replModeSwitcher) RestoreMode(fallback permission.PermissionMode) permission.PermissionMode {
	if s.previousMode != permission.SupervisedMode && s.previousMode != permission.PlanMode {
		return s.previousMode
	}
	return fallback
}

// modeChangeMsg is sent to update the Model's mode from a goroutine.
type modeChangeMsg struct {
	Mode permission.PermissionMode
}

func (r *REPL) SetAskUserTool(tools *tool.Registry) {
	tl, ok := tools.Get("ask_user")
	if !ok {
		return
	}
	askTool, ok := tl.(*tool.AskUserTool)
	if !ok {
		return
	}
	askTool.SetHandler(func(ctx context.Context, req tool.AskUserRequest) (tool.AskUserResponse, error) {
		return r.requestAskUser(ctx, req)
	})
}

// SetCommandPane wires the command pane manager into the run_command tool
// for real-time output mirroring in tmux environments.
func (r *REPL) SetCommandPane(tools *tool.Registry, workingDir string) {
	if os.Getenv("TMUX") == "" {
		return // only active in tmux
	}
	mgr := cmdpane.NewManager(workingDir)
	r.model.cmdPaneMgr = mgr

	tl, ok := tools.Get("run_command")
	if !ok {
		return
	}
	rc, ok := tl.(*tool.RunCommand)
	if !ok {
		return
	}

	writer, err := mgr.Writer()
	if err != nil {
		debug.Logf("cmdpane: failed to get writer: %v", err)
		return
	}

	preExecFn := func(command, description string) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := mgr.EnsurePane(ctx); err != nil {
			debug.Logf("cmdpane: ensure pane: %v", err)
		}
		mgr.WriteHeader(command, description)
	}

	rc.OutputTee = writer
	rc.OnPreExec = preExecFn
	rc.OnPostExec = mgr.WriteFooter

	// Also wire start_command for long-running/streaming commands.
	if tl2, ok := tools.Get("start_command"); ok {
		if sc, ok := tl2.(*tool.StartCommandTool); ok {
			sc.OutputTee = writer
			sc.OnPreExec = preExecFn
			// start_command is async — no OnPostExec here (job completion
			// is checked separately via read_command_output/wait_command).
		}
	}
}

// requestDiffConfirm sends a diff confirmation request to the TUI and waits for response.
// Honors ctx so the agent goroutine doesn't leak if the TUI shuts down or the
// run is cancelled while a confirmation prompt is in flight.
func (r *REPL) requestDiffConfirm(ctx context.Context, filePath, diffText string) bool {
	if r.program == nil {
		// Non-interactive (pipe) mode: auto-approve
		return true
	}
	resp := make(chan bool, 1)
	r.program.Send(DiffConfirmMsg{
		FilePath: filePath,
		DiffText: diffText,
		Response: resp,
	})
	select {
	case ok := <-resp:
		return ok
	case <-ctx.Done():
		return false
	}
}

func (r *REPL) requestAskUser(ctx context.Context, req tool.AskUserRequest) (tool.AskUserResponse, error) {
	if r.program == nil {
		return tool.AskUserResponse{}, fmt.Errorf("interactive questionnaire unavailable")
	}
	resp := make(chan tool.AskUserResponse, 1)
	r.program.Send(AskUserMsg{
		Request:  req,
		Response: resp,
	})
	select {
	case result := <-resp:
		return result, nil
	case <-ctx.Done():
		return tool.AskUserResponse{}, ctx.Err()
	}
}

// Program returns the underlying tea.Program for external callers that need to send messages.
func (r *REPL) Program() *tea.Program {
	return r.program
}

// cronPromptMsg is sent when a cron job fires, injecting a prompt into the conversation.
type cronPromptMsg struct {
	Prompt string
}

// Run starts the REPL event loop.
func (r *REPL) Run() error {
	traceStart := time.Now()
	traceLast := traceStart
	traceMark := func(label string) {
		now := time.Now()
		debug.Log("repl", "startup timing repl.Run %-40s delta=%s total=%s", label, now.Sub(traceLast).Round(time.Millisecond), now.Sub(traceStart).Round(time.Millisecond))
		traceLast = now
	}
	debug.Log("repl", "Run() START resumeID=%q", r.resumeID)
	traceMark("start")
	if r.core != nil {
		defer r.core.Close()
	}
	// Initialize session
	if r.store != nil {
		if r.resumeID != "" {
			// Explicit --resume <id>
			r.loadSession(r.resumeID)
			traceMark("load session")
		} else {
			// Auto-load: try to resume the most recent workspace session.
			if r.tryAutoLoadSession() {
				traceMark("auto-load session")
			} else {
				r.createSession()
				traceMark("create session")
			}
		}
	}
	r.primeInitialWindowSize(term.GetSize)
	traceMark("prime initial window size")

	// TTY hygiene: drain any pending stdin bytes (e.g. terminal probe responses
	// from the previous shell, paste residue) before bubbletea grabs the TTY.
	// Also enable bubbletea v2's internal trace log so we can see readLoop /
	// cancelReader activity in the next debug bundle.
	enableBubbleteaTrace()
	drainStdinResidual()
	traceMark("tty hygiene")

	// Pre-initialize the glamour markdown renderer so the first LLM response
	// doesn't freeze the TUI while glamour initializes its parser/highlighter.
	markdown.Warmup()
	traceMark("markdown warmup")

	r.program = tea.NewProgram(r.model)
	if r.planSwitcher != nil {
		r.planSwitcher.program = r.program
	}
	traceMark("new bubbletea program")
	debug.Log("repl", "program created stdin_is_term=%v stdout_is_term=%v",
		term.IsTerminal(os.Stdin.Fd()), term.IsTerminal(os.Stdout.Fd()))

	// Watchdog that detects if bubbletea's raw mode is silently lost
	// (readLoop dead → terminal echoes typed bytes → looks like a frozen UI).
	// Detection only — we log loudly so the next bug report has a smoking gun.
	watchdogCtx, watchdogCancel := context.WithCancel(context.Background())
	stopWatchdog := startTTYWatchdog(watchdogCtx)
	stopStdoutMonitor := startStdoutHealthMonitor(watchdogCtx, func(msg interface{}) {
		if r.program != nil {
			r.program.Send(msg)
		}
	})
	traceMark("start tty monitors")
	defer func() {
		stopWatchdog()
		stopStdoutMonitor()
		watchdogCancel()
	}()
	if r.mcpMgr != nil {
		r.mcpMgr.SetOnUpdate(func(servers []plugin.MCPServerInfo) {
			if r.program != nil {
				r.program.Send(mcpServersMsg{Servers: servers})
			}
		})
	}
	traceMark("wire mcp callbacks")
	if r.commandMgr != nil {
		stop := make(chan struct{})
		defer close(stop)
		safego.Go("tui.repl.commandReload", func() {
			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					if r.commandMgr.Reload() && r.program != nil {
						if r.skillsChangedHook != nil {
							r.skillsChangedHook()
						}
						r.program.Send(skillsChangedMsg{})
					}
				case <-stop:
					return
				}
			}
		})
	}
	traceMark("start command reload loop")

	// Wire the agent's approval handler into the TUI via channel bridge.
	// Honors ctx — if the TUI exits or the run is cancelled while waiting
	// for the user's decision, the agent goroutine returns Deny instead of
	// blocking forever on <-resp.
	r.agent.SetApprovalHandler(func(ctx context.Context, toolName string, input string) permission.Decision {
		if r.program == nil {
			return permission.Deny
		}
		resp := make(chan permission.Decision, 1)
		r.program.Send(ApprovalMsg{
			ToolName: toolName,
			Input:    input,
			Response: resp,
		})
		select {
		case d := <-resp:
			return d
		case <-ctx.Done():
			return permission.Deny
		}
	})
	traceMark("wire approval handler")

	// Wire checkpoint handler — persist compacted state after summarize.
	// Acquires the model's sessionMutex while reading m.session and calling
	// AppendCheckpoint (which mutates ses.UpdatedAt and rewrites the index)
	// because the TUI thread also mutates the same session under that mutex
	// (see appendUserMessage in submit.go).
	r.agent.SetCheckpointHandler(func(messages []provider.Message, tokenCount int) {
		if r.store == nil {
			return
		}
		mu := r.model.sessionMutex()
		mu.Lock()
		ses := r.model.Session()
		if ses == nil {
			mu.Unlock()
			return
		}
		// Mutate Session object under sessionMutex.
		ses.UpdatedAt = time.Now()
		store := r.store
		mu.Unlock()

		// Persist to disk outside sessionMutex.
		// AppendCheckpointToDisk only does JSONL write + index update
		// (both protected by the store's own mu), no Session mutation.
		if jsonlStore, ok := store.(*session.JSONLStore); ok {
			if err := jsonlStore.AppendCheckpointToDisk(ses, messages, tokenCount); err != nil {
				debug.Log("repl", "checkpoint save failed: %v", err)
			} else {
				debug.Log("repl", "checkpoint saved: %d messages, %d tokens", len(messages), tokenCount)
			}
		} else {
			mu.Lock()
			if err := store.AppendCheckpoint(ses, messages, tokenCount); err != nil {
				debug.Log("repl", "checkpoint save failed: %v", err)
			} else {
				debug.Log("repl", "checkpoint saved: %d messages, %d tokens", len(messages), tokenCount)
			}
			mu.Unlock()
		}
	})
	traceMark("wire checkpoint handler")

	// NewProgram copies the model, so SetProgram on r.model is useless.
	// We can't Send before Run (deadlock). Instead, run in a goroutine and
	// send the reference once the event loop is up.
	debug.Log("repl", "scheduling setProgramMsg")
	// Send the startup logo with vendor/endpoint/model info.
	vendorName := ""
	endpointName := ""
	if r.model.config != nil {
		vendorName = r.model.config.Vendor
		endpointName = r.model.config.Endpoint
	}
	modelName := ""
	if r.model.config != nil {
		modelName = r.model.config.Model
	}
	safego.Go("tui.repl.startupMsg", func() {
		start := time.Now()
		// Wait for Bubble Tea to complete initialization (raw mode, alt screen,
		// mouse mode, renderer start, readLoop start) before sending any messages.
		// Too short and messages arrive before the event loop is ready.
		time.Sleep(100 * time.Millisecond)
		r.program.Send(setProgramMsg{Program: r.program})
		r.program.Send(logoMsg{Vendor: vendorName, Endpoint: endpointName, Model: modelName})
		debug.Log("repl", "startup timing repl.startupMsg sent initial messages duration=%s", time.Since(start).Round(time.Millisecond))
		if r.webuiAddr != "" {
			r.program.Send(webuiReadyMsg{Addr: r.webuiAddr, Token: r.webuiToken})
		}
		if r.knightStartupHint != "" {
			r.program.Send(knightStartupHintMsg{Hint: r.knightStartupHint})
		}
		if r.projectMemoryLoader != nil {
			loader := r.projectMemoryLoader
			safego.Go("tui.repl.projectMemory", func() {
				start := time.Now()
				content, files, err := loader()
				debug.Log("repl", "startup timing repl.projectMemory files=%d bytes=%d err=%v duration=%s", len(files), len(content), err, time.Since(start).Round(time.Millisecond))
				if r.program != nil {
					r.program.Send(projectMemoryLoadedMsg{Content: content, Files: files, Err: err})
				}
			})
		}
		if r.mcpMgr != nil {
			start := time.Now()
			r.core.StartBackgroundServices()
			debug.Log("repl", "startup timing repl.mcp StartBackground duration=%s", time.Since(start).Round(time.Millisecond))
		}
	})
	traceMark("schedule startup messages")

	traceMark("before bubbletea Run")
	finalModel, err := r.program.Run()
	traceMark("after bubbletea Run")
	debug.Log("repl", "program.Run() returned err=%v", err)
	if errors.Is(err, tea.ErrInterrupted) {
		err = nil
	}
	// Drain remaining metrics before session save.
	if r.metricCollector != nil {
		if r.metricCancel != nil {
			r.metricCancel()
		}
		r.metricCollector.Stop()
	}
	if r.imManager != nil {
		r.imManager.UnbindSession()
	}
	if r.model.acpClientMgr != nil {
		r.model.acpClientMgr.CloseAll()
	}
	if r.model.instanceDetect != nil {
		r.model.instanceDetect.Unregister()
	}
	if err == nil && r.store != nil && r.model.session != nil {
		// Save session on clean exit
		_ = agentruntime.SaveAgentSessionSnapshot(r.store, r.model.session, r.agent)
	}

	// Release the session lock so another instance can resume this session.
	if r.sessionLock != nil {
		r.sessionLock.Release()
		r.sessionLock = nil
	}

	if m, ok := finalModel.(Model); ok {
		if m.terminalTitleWriter != nil {
			m.statusActivity = ""
			m.terminalTitleWriter(m.desiredTerminalTitle())
		}
		m.closeTunnelGracefully(2 * time.Second)
		finalModel = m
	}

	if m, ok := finalModel.(Model); ok && m.tmuxExecRequested {
		sid := ""
		if m.session != nil {
			sid = m.session.ID
		}
		debug.Log("tmux", "finalModel: tmuxExecRequested=%v sessionID=%q tmuxSession=%q", m.tmuxExecRequested, sid, m.tmuxExecSession)
		r.model = m
		return r.execTmuxEnter()
	}

	// Check if the final model requested a self-restart.
	// program.Run() returns the final model state, but r.model is a
	// snapshot from before Run() — we must read from finalModel.
	if m, ok := finalModel.(Model); ok && m.restartRequested {
		sid := ""
		if m.session != nil {
			sid = m.session.ID
		}
		debug.Log("restart", "finalModel: restartRequested=%v sessionID=%q updateSvc=%v",
			m.restartRequested, sid, m.updateSvc != nil)
		r.model = m
		return r.execRestart()
	}

	return err
}

func (r *REPL) primeInitialWindowSize(getSize func(fd uintptr) (int, int, error)) {
	width, height, err := getSize(os.Stdout.Fd())
	if err != nil || width <= 0 || height <= 0 {
		return
	}
	r.model.handleResize(width, height)
}

// createSession creates a fresh session and wires it into the model.
// tryAutoLoadSession attempts to load the most recent workspace session.
// Returns true if a session was loaded, false if it should create a new one.
// If the latest session is locked by another process, shows the session picker.
func (r *REPL) tryAutoLoadSession() bool {
	if r.store == nil {
		return false
	}
	// If root.go already acquired a lock (from the picker path), skip.
	if r.sessionLock != nil && r.sessionLock.Acquired() {
		return false
	}
	workspace := r.model.agent.WorkingDir()
	if workspace == "" {
		return false
	}

	latest, err := r.store.LatestForWorkspace(workspace)
	if err != nil {
		debug.Log("repl", "tryAutoLoadSession: LatestForWorkspace error: %v", err)
		return false
	}
	if latest == nil {
		debug.Log("repl", "tryAutoLoadSession: no sessions for workspace %q", workspace)
		return false
	}

	// Try to acquire a lock on the session.
	storeDir, err := session.DefaultDir()
	if err != nil {
		debug.Log("repl", "tryAutoLoadSession: DefaultDir error: %v", err)
		return false
	}
	lock, err := session.TryAcquireSessionLock(storeDir, latest.ID)
	if err != nil {
		debug.Log("repl", "tryAutoLoadSession: lock error: %v", err)
		return false
	}
	if lock != nil && lock.Acquired() {
		// We got the lock — auto-resume this session.
		r.sessionLock = lock
		r.loadSession(latest.ID)
		debug.Log("repl", "tryAutoLoadSession: auto-loaded session %s", latest.ID)
		return true
	}

	// Session is locked by another instance — create new session.
	// (The picker flow is handled in root.go before the TUI starts.)
	debug.Log("repl", "tryAutoLoadSession: session %s is locked (PID %d), creating new session",
		latest.ID, lock.HolderPID())
	return false
}

func (r *REPL) createSession() {
	start := time.Now()
	vendor := ""
	endpoint := ""
	model := ""
	if r.model.config != nil {
		vendor = r.model.config.Vendor
		endpoint = r.model.config.Endpoint
		model = r.model.config.Model
	}
	ses := session.NewSession(vendor, endpoint, model)
	debug.Log("repl", "startup timing repl.createSession session.NewSession workspace=%q duration=%s", ses.Workspace, time.Since(start).Round(time.Millisecond))
	saveStart := time.Now()
	if err := r.store.Save(ses); err == nil {
		debug.Log("repl", "startup timing repl.createSession store.Save duration=%s", time.Since(saveStart).Round(time.Millisecond))
		r.model.SetSession(ses, r.store)
		r.model.chatWriteSystem(nextSystemID(), r.model.t("session.new", ses.ID))
		debug.Log("repl", "startup timing repl.createSession total=%s", time.Since(start).Round(time.Millisecond))
	} else {
		debug.Log("repl", "startup timing repl.createSession store.Save err=%v duration=%s", err, time.Since(saveStart).Round(time.Millisecond))
	}
}

// loadSession loads a previous session and restores messages into the agent.
func (r *REPL) loadSession(id string) {
	start := time.Now()
	ses, err := r.store.Load(id)
	debug.Log("repl", "startup timing repl.loadSession store.Load id=%q messages=%d err=%v duration=%s", id, messageCount(ses), err, time.Since(start).Round(time.Millisecond))
	if err != nil {
		r.model.chatWriteSystem(nextSystemID(), r.model.t("session.resume_failed", id, err))
		r.model.chatWriteSystem(nextSystemID(), r.model.t("session.resume_fallback"))
		r.createSession()
		return
	}
	agentruntime.RestoreSessionIntoAgent(r.agent, ses)
	r.model.SetSession(ses, r.store)
	r.model.rebuildConversationFromMessages(ses.Messages)
	r.model.restoreHistoryFromMessages(ses.Messages)
	title := ses.Title
	if title == "" {
		title = r.model.t("session.untitled")
	}
	r.model.chatWriteSystem(nextSystemID(), r.model.t("session.resume", ses.ID, title, len(ses.Messages)))
	debug.Log("repl", "startup timing repl.loadSession total=%s", time.Since(start).Round(time.Millisecond))
}

func messageCount(ses *session.Session) int {
	if ses == nil {
		return 0
	}
	return len(ses.Messages)
}

// execRestart replaces the current process with a fresh ggcode binary.
// Called after program.Run() returns and the terminal has been restored.
// Uses restart.ExecSelf which does syscall.Exec on Unix or exec+exit on Windows.
func (r *REPL) execRestart() error {
	// Release session lock before execve — the new process will re-acquire it.
	if r.sessionLock != nil {
		r.sessionLock.Release()
		r.sessionLock = nil
	}

	binary, err := restart.ResolveBinary()
	if err != nil {
		return fmt.Errorf("restart: resolve binary: %w", err)
	}

	args := r.model.buildRestartArgs()

	sessionID := ""
	if r.model.session != nil {
		sessionID = r.model.session.ID
	}
	debug.Log("restart", "exec binary=%s session=%s args=%v", binary, sessionID, args)

	env := os.Environ()
	if r.model.restartDebug {
		env = append(env, "GGCODE_DEBUG=1")
	}

	return restart.ExecSelf(binary, args, env)
}

func (r *REPL) execTmuxEnter() error {
	binary, err := restart.ResolveBinary()
	if err != nil {
		return fmt.Errorf("tmux enter: resolve binary: %w", err)
	}
	args := r.model.buildRestartArgs()
	sessionName := sanitizeTmuxSessionName(r.model.tmuxExecSession)
	if sessionName == "" {
		sessionName = defaultTmuxSessionName(r.model.tmuxWorkspace())
	}
	wd := r.model.tmuxWorkspace()
	cmdArgs := append([]string{"new-session", "-A", "-s", sessionName, "-c", wd, binary}, args...)
	debug.Log("tmux", "exec tmux session=%q binary=%s args=%v wd=%s", sessionName, binary, args, wd)
	cmd := exec.Command("tmux", cmdArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()
	cmd.Env = removeEnv(cmd.Env, "GGCODE_TMUX_SETUP_LAYOUT")
	if strings.TrimSpace(r.model.tmuxExecSetupLayout) != "" {
		cmd.Env = append(cmd.Env, "GGCODE_TMUX_SETUP_LAYOUT="+r.model.tmuxExecSetupLayout)
	}
	cmd.Dir = wd
	if r.model.restartDebug {
		cmd.Env = append(cmd.Env, "GGCODE_DEBUG=1")
	}
	return cmd.Run()
}
