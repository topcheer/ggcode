package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/im"
	"github.com/topcheer/ggcode/internal/mcp"
	"github.com/topcheer/ggcode/internal/memory"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/plugin"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
	"github.com/topcheer/ggcode/internal/subagent"
	"github.com/topcheer/ggcode/internal/swarm"
	"github.com/topcheer/ggcode/internal/tool"
)

// AgentBridge wraps the agent loop with sub-agent and swarm support.
type AgentBridge struct {
	cfg      *config.Config
	prov     provider.Provider
	resolved *config.ResolvedEndpoint
	agent    *agent.Agent
	ui       *UIState

	mu        sync.Mutex
	cancel    context.CancelFunc
	cancelled bool
	working   bool

	pendingMu  sync.Mutex
	pendingMsg string
	hasPending bool

	startTime time.Time // when current agent loop started

	Emitter *im.IMEmitter

	imRound        imRoundState // per-round IM emission state
	mainWindow     fyne.Window
	permissionMode permission.PermissionMode

	registry     *tool.Registry
	workingDir   string
	sessionStore session.Store
	currentSes   *session.Session
	rebuildCB    func()

	// Sub-agent and swarm managers.
	subAgentMgr *subagent.Manager
	swarmMgr    *swarm.Manager
}

func NewAgentBridge(cfg *config.Config, prov provider.Provider, resolved *config.ResolvedEndpoint, workingDir string, ui *UIState) *AgentBridge {
	b := &AgentBridge{
		cfg:        cfg,
		prov:       prov,
		resolved:   resolved,
		ui:         ui,
		workingDir: workingDir,
	}

	// Initialize session store (session created lazily in ensureSession).
	if store, err := session.NewDefaultStore(); err == nil {
		b.sessionStore = store
	}

	return b
}

func (b *AgentBridge) setupAgent() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.agent != nil {
		return nil
	}

	b.registry = tool.NewRegistry()

	// Apply impersonation from config (same as TUI startup).
	if b.cfg != nil && b.cfg.Impersonation.Preset != "" && b.cfg.Impersonation.Preset != "none" {
		if preset := provider.FindPresetByID(b.cfg.Impersonation.Preset); preset != nil {
			provider.SetActiveImpersonation(preset, b.cfg.Impersonation.CustomVersion, b.cfg.Impersonation.CustomHeaders)
		}
	}

	if err := tool.RegisterBuiltinTools(b.registry, nil, b.workingDir); err != nil {
		return fmt.Errorf("register builtin tools: %w", err)
	}

	mergedServers, _ := mcp.MergeStartupServers(b.workingDir, b.cfg.MCPServers)
	mcpMgr := plugin.NewMCPManager(mergedServers, b.registry)
	_ = b.registry.Register(tool.ListMCPCapabilitiesTool{Runtime: mcpMgr})
	_ = b.registry.Register(tool.GetMCPPromptTool{Runtime: mcpMgr})
	_ = b.registry.Register(tool.ReadMCPResourceTool{Runtime: mcpMgr})

	pluginMgr := plugin.NewManager()
	pluginMgr.LoadAll(b.cfg.Plugins)
	_ = pluginMgr.RegisterTools(b.registry)

	autoMem := memory.NewAutoMemory()
	_ = b.registry.Register(tool.NewSaveMemoryTool(autoMem, nil))

	// Sub-agent manager.
	b.subAgentMgr = subagent.NewManager(b.cfg.SubAgents)
	agentFactory := func(prov provider.Provider, t interface{}, systemPrompt string, maxTurns int) subagent.AgentRunner {
		return agent.NewAgent(prov, t.(*tool.Registry), systemPrompt, maxTurns)
	}
	b.registry.Register(tool.SpawnAgentTool{
		Manager:      b.subAgentMgr,
		Provider:     b.prov,
		Tools:        b.registry,
		AgentFactory: agentFactory,
		WorkingDir:   b.workingDir,
	})
	b.registry.Register(tool.WaitAgentTool{Manager: b.subAgentMgr})
	b.registry.Register(tool.ListAgentsTool{Manager: b.subAgentMgr})

	// Forward sub-agent events to UI.
	b.subAgentMgr.SetOnUpdate(func(sa *subagent.SubAgent) {
		b.ui.UpdateAgentPanel(sa.ID, agentPanelFromSubAgent(sa))
	})

	// Swarm manager.
	swarmFactory := func(prov provider.Provider, tools interface{}, systemPrompt string, maxTurns int) swarm.AgentRunner {
		return agent.NewAgent(prov, tools.(*tool.Registry), systemPrompt, maxTurns)
	}
	toolBuilder := func(allowedTools []string) interface{} {
		reg := tool.NewRegistry()
		_ = tool.RegisterBuiltinTools(reg, nil, b.workingDir)
		return reg
	}
	b.swarmMgr = swarm.NewManager(b.cfg.Swarm, b.prov, swarmFactory, toolBuilder)

	b.registry.Register(tool.TeamCreateTool{Manager: b.swarmMgr})
	b.registry.Register(tool.TeamDeleteTool{Manager: b.swarmMgr})
	b.registry.Register(tool.TeammateSpawnTool{Manager: b.swarmMgr})
	b.registry.Register(tool.TeammateListTool{Manager: b.swarmMgr})
	b.registry.Register(tool.TeammateShutdownTool{Manager: b.swarmMgr})
	b.registry.Register(tool.TeammateResultsTool{Manager: b.swarmMgr})
	b.registry.Register(tool.SwarmTaskCreateTool{Manager: b.swarmMgr})
	b.registry.Register(tool.SwarmTaskListTool{Manager: b.swarmMgr})
	b.registry.Register(tool.SwarmTaskClaimTool{Manager: b.swarmMgr})
	b.registry.Register(tool.SwarmTaskCompleteTool{Manager: b.swarmMgr})

	// Re-register send_message with both managers.
	b.registry.Register(tool.SendMessageTool{Manager: b.subAgentMgr, SwarmMgr: b.swarmMgr})

	// Forward swarm events to UI.
	b.swarmMgr.SetOnUpdate(func(ev swarm.Event) {
		b.ui.UpdateAgentPanel(ev.TeammateID, agentPanelFromSwarmEvent(b.swarmMgr, ev))
	})

	systemPrompt := buildSystemPrompt(b.workingDir)
	maxIter := b.cfg.MaxIterations
	if maxIter == 0 {
		maxIter = 200
	}
	b.agent = agent.NewAgent(b.prov, b.registry, systemPrompt, maxIter)

	// Permission policy
	mode := permission.ParsePermissionMode(b.cfg.DefaultMode)
	policy := permission.NewConfigPolicyWithMode(nil, []string{b.workingDir}, mode)
	b.agent.SetPermissionPolicy(policy)
	b.permissionMode = mode

	// Approval handler — popup dialog for tool approval
	b.agent.SetApprovalHandler(func(ctx context.Context, toolName string, input string) permission.Decision {
		if b.mainWindow == nil {
			return permission.Deny
		}
		resp := make(chan permission.Decision, 1)
		fyne.Do(func() {
			var d dialog.Dialog
			denyBtn := widget.NewButton("Deny", func() {
				resp <- permission.Deny
				d.Hide()
			})
			allowBtn := widget.NewButton("Allow", func() {
				resp <- permission.Allow
				d.Hide()
			})
			allowBtn.Importance = widget.HighImportance
			alwaysBtn := widget.NewButton("Always Allow", func() {
				if b.agent != nil {
					if p, ok := b.agent.PermissionPolicy().(*permission.ConfigPolicy); ok {
						p.SetOverride(toolName, permission.Allow)
					}
				}
				resp <- permission.Allow
				d.Hide()
			})
			alwaysBtn.Importance = widget.SuccessImportance

			// Format tool arguments as readable key-value pairs
			var displayArgs string
			var raw map[string]interface{}
			if json.Unmarshal([]byte(input), &raw) == nil {
				for k, v := range raw {
					displayArgs += fmt.Sprintf("  %s: %v\n", k, v)
				}
				displayArgs = strings.TrimSpace(displayArgs)
			} else {
				displayArgs = truncate(input, 800)
			}
			content := widget.NewLabel(fmt.Sprintf("Tool: %s\n\n%s", toolName, displayArgs))
			content.Wrapping = fyne.TextWrapWord

			d = dialog.NewCustomWithoutButtons("Tool Approval",
				container.NewVBox(
					content,
					widget.NewSeparator(),
					container.NewHBox(layout.NewSpacer(), denyBtn, allowBtn, alwaysBtn),
				), b.mainWindow)
			d.Show()
		})
		select {
		case d := <-resp:
			return d
		case <-ctx.Done():
			return permission.Deny
		}
	})

	// Ask user handler — popup dialog for questions
	if tl, ok := b.registry.Get("ask_user"); ok {
		if askTool, ok := tl.(*tool.AskUserTool); ok {
			askTool.SetHandler(func(ctx context.Context, req tool.AskUserRequest) (tool.AskUserResponse, error) {
				return b.handleAskUser(ctx, req)
			})
		}
	}

	if b.resolved.ContextWindow > 0 {
		b.agent.ContextManager().SetContextWindow(b.resolved.ContextWindow)
	}
	if b.resolved.MaxTokens > 0 {
		b.agent.ContextManager().SetOutputReserve(b.resolved.MaxTokens)
	}
	b.ensureSession()
	return nil
}

func (b *AgentBridge) Send(userMsg string) error {
	return b.SendContent([]provider.ContentBlock{provider.TextBlock(userMsg)})
}

func (b *AgentBridge) SendContent(content []provider.ContentBlock) error {
	if err := b.setupAgent(); err != nil {
		return err
	}

	b.mu.Lock()
	b.working = true
	b.startTime = time.Now()
	ctx, cancel := context.WithCancel(context.Background())
	b.cancel = cancel
	b.mu.Unlock()

	// Persist user message to disk immediately (incremental), same as TUI.
	b.appendUserMessageContent(content)

	go func() {
		defer func() {
			cancel()
			b.ui.FinalizeStreaming()
			b.saveSession()

			b.mu.Lock()
			wasCancelled := b.cancelled
			b.working = false
			b.cancel = nil
			b.cancelled = false
			b.mu.Unlock()
			if wasCancelled {
				b.ui.AppendChat(ChatMessage{
					Role:    "system",
					Content: "(cancelled)",
					Time:    time.Now(),
				})
			}

			// Check for queued message from user while busy.
			if msg, ok := b.drainPending(); ok {
				b.ui.AppendChat(ChatMessage{
					Role:    "system",
					Content: "Processing queued message...",
					Time:    time.Now(),
				})
				_ = b.Send(msg)
			}
		}()

		onEvent := func(ev provider.StreamEvent) {
			defer logPanic("agent event handler")

			switch ev.Type {
			case provider.StreamEventText:
				b.ui.AppendAssistantText(ev.Text)
				b.imRound.Text.WriteString(ev.Text)

			case provider.StreamEventToolCallDone:
				b.ui.FinalizeStreaming()

				name := ev.Tool.Name
				if name == "" {
					name = "tool"
				}
				description := toolDescription(name, string(ev.Tool.Arguments))
				args := toolArgSummary(name, string(ev.Tool.Arguments))

				b.ui.AppendChat(ChatMessage{
					Role:     "tool",
					ToolName: name,
					ToolID:   ev.Tool.ID,
					ToolDesc: description,
					ToolArgs: args,
					ToolRaw:  string(ev.Tool.Arguments),
					Content:  "",
					Time:     time.Now(),
				})

				// Track tool calls in round state.
				b.imRound.ToolCalls++

				// Emit tool call event to IM.
				if b.Emitter != nil {
					b.Emitter.EmitEvent(im.OutboundEvent{
						Kind: im.OutboundEventToolCall,
						ToolCall: &im.ToolCallInfo{
							ToolName: name,
							Args:     args,
						},
					})
				}

			case provider.StreamEventToolResult:
				content := ev.Result
				if len([]rune(content)) > 2000 {
					content = truncateRunes(content, 2000, "\n...(truncated)")
				}
				b.ui.UpdateToolResult(ev.Tool.ID, content, ev.IsError)
				if ev.IsError {
					b.imRound.ToolFailures++
				} else {
					b.imRound.ToolSuccesses++
				}

				// Emit tool result event to IM.
				if b.Emitter != nil {
					b.Emitter.EmitEvent(im.OutboundEvent{
						Kind: im.OutboundEventToolResult,
						ToolRes: &im.ToolResultInfo{
							ToolName: ev.Tool.Name,
							Result:   content,
							IsError:  ev.IsError,
						},
					})
				}

				// After spawn_agent completes, sync agent panels.
				if ev.Tool.Name == "spawn_agent" && b.subAgentMgr != nil {
					b.syncAgentPanels()
				}

			case provider.StreamEventSystem:
				b.ui.FinalizeStreaming()
				b.ui.AppendChat(ChatMessage{
					Role:    "system",
					Content: ev.Text,
					Time:    time.Now(),
				})

			case provider.StreamEventReasoning:
				if ev.Text != "" {
					b.ui.AppendReasoning(ev.Text)
				}

			case provider.StreamEventDone:
				// Each LLM turn ends with Done. Emit round summary to IM.
				if b.Emitter != nil {
					text := strings.TrimSpace(b.imRound.Text.String())
					if text != "" || b.imRound.ToolCalls > 0 {
						b.Emitter.EmitRoundSummary(text, b.imRound.ToolCalls, b.imRound.ToolSuccesses, b.imRound.ToolFailures)
					}
					b.imRound.Text.Reset()
					b.imRound.ToolCalls = 0
					b.imRound.ToolSuccesses = 0
					b.imRound.ToolFailures = 0
				}
			}
		}

		err := b.agent.RunStreamWithContent(ctx, content, onEvent)
		if err != nil {
			b.mu.Lock()
			c := b.cancelled
			b.mu.Unlock()
			if !c {
				b.ui.AppendChat(ChatMessage{
					Role:    "error",
					Content: err.Error(),
					Time:    time.Now(),
				})
			}
		}
	}()

	return nil
}

// syncAgentPanels reads all sub-agents and updates UIState.
func (b *AgentBridge) syncAgentPanels() {
	if b.subAgentMgr == nil {
		return
	}
	for _, sa := range b.subAgentMgr.List() {
		b.ui.UpdateAgentPanel(sa.ID, agentPanelFromSubAgent(sa))
	}
}

func (b *AgentBridge) Cancel() {
	b.mu.Lock()
	if b.cancel != nil {
		b.cancelled = true
		b.cancel()
	}
	b.mu.Unlock()
}

func (b *AgentBridge) Close() {
	b.Cancel()
}

// QueueMessage stores a user message to be sent after the current agent turn.
func (b *AgentBridge) QueueMessage(msg string) {
	b.pendingMu.Lock()
	b.pendingMsg = msg
	b.hasPending = true
	b.pendingMu.Unlock()
}

// drainPending returns and clears any queued message.
func (b *AgentBridge) drainPending() (string, bool) {
	b.pendingMu.Lock()
	msg := b.pendingMsg
	has := b.hasPending
	b.pendingMsg = ""
	b.hasPending = false
	b.pendingMu.Unlock()
	return msg, has
}

func (b *AgentBridge) IsWorking() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.working
}

func (b *AgentBridge) Elapsed() time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.working {
		return 0
	}
	return time.Since(b.startTime)
}

func (b *AgentBridge) ContextWindow() int {
	if b.agent == nil {
		return b.resolved.ContextWindow
	}
	return b.agent.ContextManager().ContextWindow()
}

func (b *AgentBridge) TokenCount() int {
	if b.agent == nil {
		return 0
	}
	return b.agent.ContextManager().TokenCount()
}

func (b *AgentBridge) Resolved() *config.ResolvedEndpoint {
	return b.resolved
}

// SubAgentPanels returns a snapshot of all active/finished agent panels.
func (b *AgentBridge) SubAgentPanels() []AgentPanelData {
	if b.subAgentMgr == nil {
		return nil
	}
	agents := b.subAgentMgr.List()
	panels := make([]AgentPanelData, 0, len(agents))
	for _, sa := range agents {
		panels = append(panels, agentPanelFromSubAgent(sa))
	}
	return panels
}

// SwarmPanels returns a snapshot of all teammate panels.
func (b *AgentBridge) SwarmPanels() []AgentPanelData {
	if b.swarmMgr == nil {
		return nil
	}
	panels := b.ui.GetAgentPanels()
	result := make([]AgentPanelData, 0)
	for _, p := range panels {
		if p.Kind == "teammate" {
			result = append(result, p)
		}
	}
	return result
}

// ── Helpers ──────────────────────────────────────────

func toolDescription(toolName, rawArgs string) string {
	var args map[string]json.RawMessage
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
		return toolName
	}
	// For task tools, prefer subject (short title) over description (long detail).
	if strings.HasPrefix(toolName, "task_") {
		if subj, ok := args["subject"]; ok {
			var s string
			if json.Unmarshal(subj, &s) == nil && s != "" {
				return s
			}
		}
	}
	if desc, ok := args["description"]; ok {
		var s string
		if json.Unmarshal(desc, &s) == nil && s != "" {
			return s
		}
	}
	return toolName
}

func toolArgSummary(toolName, rawArgs string) string {
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
		return ""
	}
	switch toolName {
	case "read_file", "write_file", "edit_file":
		if p, ok := args["path"].(string); ok {
			return p
		}
	case "run_command", "start_command":
		if c, ok := args["command"].(string); ok {
			return c
		}
	case "search_files", "grep":
		if p, ok := args["pattern"].(string); ok {
			return p
		}
	case "glob":
		if p, ok := args["pattern"].(string); ok {
			return p
		}
	case "list_directory":
		if p, ok := args["path"].(string); ok {
			return p
		}
	}
	for _, v := range args {
		if s, ok := v.(string); ok && len(s) > 0 {
			if len([]rune(s)) > 60 {
				return truncateRunes(s, 60, "...")
			}
			return s
		}
	}
	return ""
}

func extractJSONField(rawArgs, field string) string {
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
		return ""
	}
	if v, ok := args[field].(string); ok {
		return v
	}
	return ""
}

func agentPanelFromSubAgent(sa *subagent.SubAgent) AgentPanelData {
	name := sa.Name
	if name == "" {
		name = "agent"
	}
	task := sa.Task
	if sa.DisplayTask != "" {
		task = sa.DisplayTask
	}
	events := make([]AgentEventEntry, 0)
	for _, ev := range sa.Events() {
		entry := AgentEventEntry{
			Type:     agentEventTypeStr(ev.Type),
			ToolName: ev.ToolName,
			ToolID:   ev.ToolID,
			ToolArgs: ev.ToolArgs,
		}
		switch ev.Type {
		case subagent.AgentEventToolResult:
			entry.Content = ev.Result
			entry.IsError = ev.IsError
		case subagent.AgentEventToolCall:
			// ToolCall has no Text field; use toolArgSummary as description.
			entry.Content = toolArgSummary(ev.ToolName, ev.ToolArgs)
		default:
			entry.Content = ev.Text
		}
		events = append(events, entry)
	}
	errStr := ""
	if sa.Error != nil {
		errStr = sa.Error.Error()
	}
	p := AgentPanelData{
		ID:     sa.ID,
		Name:   name,
		Kind:   "subagent",
		Status: string(sa.Status),
		Task:   task,
		Result: sa.Result,
		Error:  errStr,
		Events: events,
	}
	if sa.Status == subagent.StatusCompleted || sa.Status == subagent.StatusFailed {
		if !sa.EndedAt.IsZero() {
			p.CompletedAt = sa.EndedAt
		} else {
			p.CompletedAt = time.Now()
		}
	}
	return p
}

func agentPanelFromSwarmEvent(mgr *swarm.Manager, ev swarm.Event) AgentPanelData {
	snap, ok := mgr.TeammateSnapshot(ev.TeammateID)
	if !ok {
		return AgentPanelData{ID: ev.TeammateID, Name: ev.TeammateName, Kind: "teammate", TeamID: ev.TeamID}
	}
	events := make([]AgentEventEntry, 0, len(snap.Events))
	for _, e := range snap.Events {
		entry := AgentEventEntry{
			Type:     teammateEventTypeStr(e.Type),
			ToolName: e.ToolName,
			ToolID:   e.ToolID,
			ToolArgs: e.ToolArgs,
		}
		switch e.Type {
		case swarm.TeammateEventToolResult:
			entry.Content = e.Result
			entry.IsError = e.IsError
		case swarm.TeammateEventToolCall:
			entry.Content = toolArgSummary(e.ToolName, e.ToolArgs)
		default:
			entry.Content = e.Text
		}
		events = append(events, entry)
	}
	errStr := ""
	if ev.Error != nil {
		errStr = ev.Error.Error()
	}
	p := AgentPanelData{
		ID:     snap.ID,
		Name:   snap.Name,
		Kind:   "teammate",
		Status: string(snap.Status),
		Task:   snap.CurrentTask,
		Result: snap.LastResult,
		Error:  errStr,
		TeamID: ev.TeamID,
		Events: events,
	}
	if snap.Status == swarm.TeammateIdle && !snap.EndedAt.IsZero() {
		p.CompletedAt = snap.EndedAt
	}
	return p
}

func teammateEventTypeStr(t swarm.TeammateEventType) string {
	switch t {
	case swarm.TeammateEventText:
		return "text"
	case swarm.TeammateEventToolCall:
		return "tool_call"
	case swarm.TeammateEventToolResult:
		return "tool_result"
	case swarm.TeammateEventError:
		return "error"
	}
	return "unknown"
}

func agentEventTypeStr(t subagent.AgentEventType) string {
	switch t {
	case subagent.AgentEventText:
		return "text"
	case subagent.AgentEventToolCall:
		return "tool_call"
	case subagent.AgentEventToolResult:
		return "tool_result"
	case subagent.AgentEventError:
		return "error"
	case subagent.AgentEventReasoning:
		return "reasoning"
	}
	return "unknown"
}

func buildSystemPrompt(workingDir string) string {
	hostname, _ := os.Hostname()
	cwd := workingDir
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	return fmt.Sprintf(`You are ggcode, an AI coding assistant running as a desktop application.

## Environment
- OS: %s
- Working directory: %s

## Instructions
- Be precise, concise, and proactive.
- Prefer small, reversible changes over broad rewrites.
- Read before you edit, and inspect results before claiming success.
`, hostname, cwd)
}

// saveSession persists the current conversation to the session store.
// If the session has no messages, it is deleted instead.
func (b *AgentBridge) saveSession() {
	if b.sessionStore == nil || b.currentSes == nil {
		return
	}
	b.mu.Lock()
	agent := b.agent
	b.mu.Unlock()
	if agent == nil {
		return
	}
	msgs := agent.Messages()
	b.currentSes.Messages = msgs

	// If no user messages, delete the empty session.
	if len(b.currentSes.Messages) == 0 {
		_ = b.sessionStore.Delete(b.currentSes.ID)
		return
	}

	// Auto-generate title from first user message if still default.
	if b.currentSes.Title == "" || b.currentSes.Title == "New session" {
		for _, m := range b.currentSes.Messages {
			if m.Role == "user" {
				for _, block := range m.Content {
					if block.Type == "text" && block.Text != "" {
						text := block.Text
						if len([]rune(text)) > 60 {
							text = string([]rune(text)[:57]) + "..."
						}
						b.currentSes.Title = text
						break
					}
				}
				break
			}
		}
	}

	_ = b.sessionStore.Save(b.currentSes)
}

// ensureSession creates a new session if one doesn't exist yet.
func (b *AgentBridge) ensureSession() {
	if b.currentSes != nil || b.sessionStore == nil {
		return
	}
	vendor := ""
	endpoint := ""
	model := ""
	if b.cfg != nil {
		vendor = b.cfg.Vendor
		endpoint = b.cfg.Endpoint
		model = b.cfg.Model
	}
	ses := session.NewSession(vendor, endpoint, model)
	_ = b.sessionStore.Save(ses)
	b.currentSes = ses
}

// SessionStore returns the session store for external use (e.g., sidebar).
func (b *AgentBridge) SessionStore() session.Store {
	return b.sessionStore
}

// CurrentSession returns the current session.
func (b *AgentBridge) CurrentSession() *session.Session {
	return b.currentSes
}

// ResetAgent clears the cached agent so the next request recreates it
// with fresh provider settings (e.g. new impersonation headers).
func (b *AgentBridge) ResetAgent() {
	b.mu.Lock()
	b.agent = nil
	b.mu.Unlock()
}

// ResumeSession loads a session by ID and restores its messages into the agent.
func (b *AgentBridge) ResumeSession(id string) error {
	if b.sessionStore == nil {
		return fmt.Errorf("no session store")
	}
	if err := b.setupAgent(); err != nil {
		return err
	}

	ses, err := b.sessionStore.Load(id)
	if err != nil {
		return fmt.Errorf("load session: %w", err)
	}

	// Feed all messages into the agent context.
	for _, msg := range ses.Messages {
		b.agent.AddMessage(msg)
	}

	b.currentSes = ses
	return nil
}

// RebuildCallback is set by the UI to rebuild the chat view after resume.
func (b *AgentBridge) SetRebuildCallback(cb func()) {
	b.rebuildCB = cb
}

// appendUserMessageContent persists the user message to disk immediately (incremental),
// matching TUI's appendUserMessage behavior. Accepts full content blocks (text + image).
func (b *AgentBridge) appendUserMessageContent(content []provider.ContentBlock) {
	if b.sessionStore == nil || b.currentSes == nil {
		return
	}
	msg := provider.Message{
		Role:    "user",
		Content: content,
	}
	b.currentSes.Messages = append(b.currentSes.Messages, msg)
	b.currentSes.UpdatedAt = time.Now()

	// Auto-generate title from first text block.
	if b.currentSes.Title == "" || b.currentSes.Title == "New session" {
		for _, block := range content {
			if block.Type == "text" && block.Text != "" {
				text := block.Text
				if len([]rune(text)) > 60 {
					text = string([]rune(text)[:57]) + "..."
				}
				b.currentSes.Title = text
				break
			}
		}
	}

	// Incremental append to disk (same as TUI).
	if jsonlStore, ok := b.sessionStore.(*session.JSONLStore); ok {
		_ = jsonlStore.AppendMessageToDisk(b.currentSes, msg)
	} else {
		_ = b.sessionStore.Save(b.currentSes)
	}
}

// extractTextFromBlocks extracts plain text from content blocks.
func extractTextFromBlocks(blocks []provider.ContentBlock) string {
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// imRoundState tracks per-LLM-turn state for IM emission.
type imRoundState struct {
	Text          strings.Builder
	ToolCalls     int
	ToolSuccesses int
	ToolFailures  int
}

// handleAskUser shows a dialog for ask_user tool questions.
func (b *AgentBridge) handleAskUser(ctx context.Context, req tool.AskUserRequest) (tool.AskUserResponse, error) {
	if b.mainWindow == nil || len(req.Questions) == 0 {
		return tool.AskUserResponse{Status: "skipped"}, nil
	}

	resp := make(chan tool.AskUserResponse, 1)
	fyne.Do(func() {
		// Build form from questions
		var answers []tool.AskUserAnswer
		var items []*widget.FormItem
		// labelToID maps choice label back to choice ID for each question index.
		labelToID := make(map[int]map[string]string)

		for _, q := range req.Questions {
			switch q.Kind {
			case "text":
				entry := widget.NewMultiLineEntry()
				entry.PlaceHolder = q.Placeholder
				items = append(items, &widget.FormItem{Text: q.Title, Widget: entry})
				answers = append(answers, tool.AskUserAnswer{ID: q.ID, Title: q.Title, Kind: q.Kind, Answered: true})

			case "single":
				choices := make([]string, len(q.Choices))
				for i, c := range q.Choices {
					choices[i] = c.Label
				}
				sel := widget.NewSelect(choices, nil)
				labelToID[len(items)] = make(map[string]string)
				for _, c := range q.Choices {
					labelToID[len(items)][c.Label] = c.ID
				}
				if len(choices) > 0 {
					sel.SetSelectedIndex(0)
				}
				notesEntry := widget.NewEntry()
				notesEntry.PlaceHolder = "Additional notes (optional)"
				box := container.NewVBox(sel, notesEntry)
				items = append(items, &widget.FormItem{Text: q.Title, Widget: box})
				answers = append(answers, tool.AskUserAnswer{ID: q.ID, Title: q.Title, Kind: q.Kind, Answered: true})

			case "multi":
				labels := make([]string, len(q.Choices))
				for i, c := range q.Choices {
					labels[i] = c.Label
				}
				checks := widget.NewCheckGroup(labels, nil)
				labelToID[len(items)] = make(map[string]string)
				for _, c := range q.Choices {
					labelToID[len(items)][c.Label] = c.ID
				}
				notesEntry := widget.NewEntry()
				notesEntry.PlaceHolder = "Additional notes (optional)"
				box := container.NewVBox(checks, notesEntry)
				items = append(items, &widget.FormItem{Text: q.Title, Widget: box})
				answers = append(answers, tool.AskUserAnswer{ID: q.ID, Title: q.Title, Kind: q.Kind, Answered: true})

			default:
				entry := widget.NewEntry()
				entry.PlaceHolder = q.Placeholder
				items = append(items, &widget.FormItem{Text: q.Title, Widget: entry})
				answers = append(answers, tool.AskUserAnswer{ID: q.ID, Title: q.Title, Kind: q.Kind, Answered: true})
			}
		}

		d := dialog.NewForm(req.Title, "Submit", "Skip",
			items,
			func(ok bool) {
				if !ok {
					resp <- tool.AskUserResponse{Status: "skipped"}
					return
				}
				// Collect answers from form items
				for i, item := range items {
					switch w := item.Widget.(type) {
					case *widget.Entry:
						answers[i].FreeformText = w.Text
					case *widget.Select:
						answers[i].SelectedChoiceIDs = []string{w.Selected}
						answers[i].SelectedChoices = []string{w.Selected}
					case *fyne.Container:
						// VBox with main widget + notes entry
						for _, obj := range w.Objects {
							switch obj.(type) {
							case *widget.Select:
								sel := obj.(*widget.Select)
								if m := labelToID[i]; m != nil {
									if id, ok := m[sel.Selected]; ok {
										answers[i].SelectedChoiceIDs = []string{id}
									}
								}
								answers[i].SelectedChoices = []string{sel.Selected}
							case *widget.CheckGroup:
								cg := obj.(*widget.CheckGroup)
								answers[i].SelectedChoices = cg.Selected
								var ids []string
								if m := labelToID[i]; m != nil {
									for _, lbl := range cg.Selected {
										if id, ok := m[lbl]; ok {
											ids = append(ids, id)
										}
									}
								}
								answers[i].SelectedChoiceIDs = ids
							case *widget.Entry:
								answers[i].FreeformText = obj.(*widget.Entry).Text
							}
						}
					}
				}
				resp <- tool.AskUserResponse{
					Status:        "answered",
					Title:         req.Title,
					QuestionCount: len(req.Questions),
					AnsweredCount: len(answers),
					Answers:       answers,
				}
			}, b.mainWindow)
		d.Resize(fyne.NewSize(500, 400))
		d.Show()
	})

	select {
	case r := <-resp:
		return r, nil
	case <-ctx.Done():
		return tool.AskUserResponse{Status: "cancelled"}, ctx.Err()
	}
}

// truncate shortens a string for display.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// SetMainWindow sets the main window reference for dialogs.
func (b *AgentBridge) SetMainWindow(w fyne.Window) {
	b.mainWindow = w
}

// SetPermissionMode updates the agent permission mode at runtime.
func (b *AgentBridge) SetPermissionMode(mode permission.PermissionMode) {
	if b.agent == nil {
		return
	}
	b.permissionMode = mode
	policy := permission.NewConfigPolicyWithMode(nil, []string{b.workingDir}, mode)
	b.agent.SetPermissionPolicy(policy)
}
