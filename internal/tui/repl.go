package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/term"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/checkpoint"
	"github.com/topcheer/ggcode/internal/commands"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/cron"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/im"
	"github.com/topcheer/ggcode/internal/knight"
	"github.com/topcheer/ggcode/internal/memory"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/plugin"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/session"
	"github.com/topcheer/ggcode/internal/subagent"
	"github.com/topcheer/ggcode/internal/swarm"
	"github.com/topcheer/ggcode/internal/task"
	"github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/update"
)

// REPL connects the agent to the TUI model.
type REPL struct {
	model               Model
	agent               *agent.Agent
	program             *tea.Program
	store               session.Store
	resumeID            string
	mcpMgr              *plugin.MCPManager
	commandMgr          *commands.Manager
	imManager           *im.Manager
	projectMemoryLoader func() (string, []string, error)
}

// NewREPL creates a new REPL with optional permission policy.
func NewREPL(a *agent.Agent, policy permission.PermissionPolicy) *REPL {
	m := NewModel(a, policy)
	return &REPL{
		model: m,
		agent: a,
	}
}

// SetSessionStore sets the session persistence store.
func (r *REPL) SetSessionStore(s session.Store) {
	r.store = s
}

// SetMCPServers passes MCP server info to the TUI model.
func (r *REPL) SetMCPServers(servers []MCPInfo) {
	r.model.SetMCPServers(servers)
}

func (r *REPL) SetMCPManager(mgr *plugin.MCPManager) {
	r.mcpMgr = mgr
	r.model.SetMCPManager(mgr)
}

// SetResumeID sets the session ID to resume.
func (r *REPL) SetResumeID(id string) {
	r.resumeID = id
}

// SetConfig passes the config to the model for /model and /provider commands.
func (r *REPL) SetConfig(cfg *config.Config) {
	r.model.SetConfig(cfg)
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

// SetSubAgentManager wires the sub-agent manager and registers sub-agent tools.
func (r *REPL) SetSubAgentManager(mgr *subagent.Manager, prov provider.Provider, tools *tool.Registry) {
	r.model.SetSubAgentManager(mgr)

	factory := func(prov provider.Provider, t interface{}, systemPrompt string, maxTurns int) subagent.AgentRunner {
		return agent.NewAgent(prov, t.(*tool.Registry), systemPrompt, maxTurns)
	}

	tools.Register(tool.SpawnAgentTool{
		Manager:      mgr,
		Provider:     prov,
		Tools:        tools,
		AgentFactory: factory,
	})
	tools.Register(tool.WaitAgentTool{Manager: mgr})
	tools.Register(tool.ListAgentsTool{Manager: mgr})

	// Notify TUI on live updates and completion.
	mgr.SetOnUpdate(func(sa *subagent.SubAgent) {
		if r.program != nil {
			r.program.Send(subAgentUpdateMsg{})
		}
	})
	mgr.SetOnComplete(func(sa *subagent.SubAgent) {
		if r.program != nil {
			r.program.Send(subAgentUpdateMsg{})
		}
	})
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
	tools.Register(tool.EnterPlanModeTool{Switcher: switcher})
	tools.Register(tool.ExitPlanModeTool{Switcher: switcher, DefaultMode: permission.SupervisedMode})
}

// SetConfigTool registers the config tool backed by the current config.
func (r *REPL) SetConfigTool(tools *tool.Registry) {
	access := &replConfigAccess{model: &r.model}
	tools.Register(tool.ConfigTool{Access: access})
}

// SetSendMessageTool registers the send_message tool for agent communication.
func (r *REPL) SetSendMessageTool(mgr *subagent.Manager, tools *tool.Registry) {
	tools.Register(tool.SendMessageTool{Manager: mgr})
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
	tools.Register(tool.SendMessageTool{Manager: r.model.subAgentMgr, SwarmMgr: mgr})

	// Notify TUI on swarm state changes.
	mgr.SetOnUpdate(func(ev swarm.Event) {
		if r.program != nil {
			r.program.Send(subAgentUpdateMsg{}) // reuse existing update message
		}
	})
}

// replModeSwitcher implements tool.ModeSwitcher by delegating to the TUI Model.
type replModeSwitcher struct {
	model        *Model
	previousMode permission.PermissionMode
}

func (s *replModeSwitcher) SetMode(mode permission.PermissionMode) {
	// ConfigPolicy.SetMode is thread-safe (has its own mutex)
	if cp, ok := s.model.policy.(*permission.ConfigPolicy); ok {
		cp.SetMode(mode)
	}
	// Update Model.mode via program.Send for thread safety
	if s.model.program != nil {
		s.model.program.Send(modeChangeMsg{Mode: mode})
	}
}

// RememberMode saves the current mode as "previous" and returns what was saved.
// This is called by enter_plan_mode to remember the mode before switching.
func (s *replModeSwitcher) RememberMode(currentMode permission.PermissionMode) permission.PermissionMode {
	// The current actual mode comes from the policy, not the argument.
	// The argument is the NEW mode we're about to switch to.
	actualCurrent := s.model.mode
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

// replConfigAccess implements tool.ConfigAccess backed by the TUI Model's config.
type replConfigAccess struct {
	model *Model
}

func (a *replConfigAccess) Get(key string) (string, bool) {
	if a.model.config == nil {
		return "", false
	}
	switch key {
	case "vendor":
		return a.model.config.Vendor, true
	case "endpoint":
		return a.model.config.Endpoint, true
	case "model":
		return a.model.config.Model, true
	case "language":
		return a.model.config.Language, true
	case "max_iterations":
		return fmt.Sprintf("%d", a.model.config.MaxIterations), true
	case "default_mode":
		return a.model.config.DefaultMode, true
	default:
		return "", false
	}
}

func (a *replConfigAccess) Set(key, value string) error {
	// V1: read-only config tool; writing is not yet supported
	return fmt.Errorf("setting %q is not supported in V1 (use /config command)", key)
}

func (a *replConfigAccess) List() map[string]string {
	if a.model.config == nil {
		return nil
	}
	return map[string]string{
		"vendor":         a.model.config.Vendor,
		"endpoint":       a.model.config.Endpoint,
		"model":          a.model.config.Model,
		"language":       a.model.config.Language,
		"max_iterations": fmt.Sprintf("%d", a.model.config.MaxIterations),
		"default_mode":   a.model.config.DefaultMode,
	}
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
	debug.Log("repl", "Run() START resumeID=%q", r.resumeID)
	// Initialize session
	if r.store != nil {
		if r.resumeID != "" {
			r.loadSession(r.resumeID)
		} else {
			r.createSession()
		}
	}
	r.primeInitialWindowSize(term.GetSize)

	// TTY hygiene: drain any pending stdin bytes (e.g. terminal probe responses
	// from the previous shell, paste residue) before bubbletea grabs the TTY.
	// Also enable bubbletea v2's internal trace log so we can see readLoop /
	// cancelReader activity in the next debug bundle.
	enableBubbleteaTrace()
	drainStdinResidual()

	r.program = tea.NewProgram(r.model)
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
		defer r.mcpMgr.Close()
	}
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
						r.program.Send(skillsChangedMsg{})
					}
				case <-stop:
					return
				}
			}
		})
	}

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
		defer mu.Unlock()
		ses := r.model.Session()
		if ses == nil {
			return
		}
		if err := r.store.AppendCheckpoint(ses, messages, tokenCount); err != nil {
			debug.Log("repl", "checkpoint save failed: %v", err)
		} else {
			debug.Log("repl", "checkpoint saved: %d messages, %d tokens", len(messages), tokenCount)
		}
	})

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
		// Wait for Bubble Tea to complete initialization (raw mode, alt screen,
		// mouse mode, renderer start, readLoop start) before sending any messages.
		// Too short and messages arrive before the event loop is ready.
		time.Sleep(100 * time.Millisecond)
		r.program.Send(setProgramMsg{Program: r.program})
		r.program.Send(logoMsg{Vendor: vendorName, Endpoint: endpointName, Model: modelName})
		if r.projectMemoryLoader != nil {
			loader := r.projectMemoryLoader
			safego.Go("tui.repl.projectMemory", func() {
				content, files, err := loader()
				if r.program != nil {
					r.program.Send(projectMemoryLoadedMsg{Content: content, Files: files, Err: err})
				}
			})
		}
		if r.mcpMgr != nil {
			r.mcpMgr.StartBackground(context.Background())
		}
	})

	_, err := r.program.Run()
	debug.Log("repl", "program.Run() returned err=%v", err)
	if errors.Is(err, tea.ErrInterrupted) {
		err = nil
	}
	if r.imManager != nil {
		r.imManager.UnbindSession()
	}
	if r.model.instanceDetect != nil {
		r.model.instanceDetect.Unregister()
	}
	if err == nil && r.store != nil && r.model.session != nil {
		// Save session on clean exit
		r.model.session.Messages = r.agent.Messages()
		_ = r.store.Save(r.model.session)
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
func (r *REPL) createSession() {
	vendor := ""
	endpoint := ""
	model := ""
	if r.model.config != nil {
		vendor = r.model.config.Vendor
		endpoint = r.model.config.Endpoint
		model = r.model.config.Model
	}
	ses := session.NewSession(vendor, endpoint, model)
	if err := r.store.Save(ses); err == nil {
		r.model.SetSession(ses, r.store)
		r.model.chatWriteSystem(nextSystemID(), r.model.t("session.new", ses.ID))
	}
}

// loadSession loads a previous session and restores messages into the agent.
func (r *REPL) loadSession(id string) {
	ses, err := r.store.Load(id)
	if err != nil {
		r.model.chatWriteSystem(nextSystemID(), r.model.t("session.resume_failed", id, err))
		r.model.chatWriteSystem(nextSystemID(), r.model.t("session.resume_fallback"))
		r.createSession()
		return
	}
	for _, msg := range ses.Messages {
		r.agent.AddMessage(msg)
	}
	r.model.SetSession(ses, r.store)
	r.model.rebuildConversationFromMessages(ses.Messages)
	title := ses.Title
	if title == "" {
		title = r.model.t("session.untitled")
	}
	r.model.chatWriteSystem(nextSystemID(), r.model.t("session.resume", ses.ID, title, len(ses.Messages)))
}
