package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/agentruntime"
	"github.com/topcheer/ggcode/internal/checkpoint"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/memory"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// AgentLoop runs the headless agent loop for ACP sessions.
// It reuses ggcode's existing Agent.RunStreamWithContent and converts
// provider.StreamEvents into ACP session/update notifications.
type AgentLoop struct {
	cfg        *config.Config
	registry   *tool.Registry
	transport  *Transport
	session    *Session
	clientCaps ClientCapabilities
	agent      *agent.Agent
	// cancelMu guards cancel: Stop() may be called from any goroutine
	// concurrently with ExecutePrompt's assignment (e.g. session/cancel vs a
	// new prompt racing on a cached loop). Without the mutex this is a data
	// race and Stop can observe nil or a stale cancel func (#1033).
	cancelMu   sync.Mutex
	cancel     context.CancelFunc
	cancelGen  uint64 // generation of the installed cancel (guards stale clears)
	modeMu     sync.Mutex
	mode       string // permission mode: "supervised", "auto", "bypass", "autopilot"
	modeLocked bool   // true once the Client explicitly set a mode via session/set_mode
}

// NewAgentLoop creates a new AgentLoop for the given session.
// It configures the agent identically to the TUI/daemon path:
//   - Project memory (GGCODE.md, AGENTS.md, etc.)
//   - Hook config (pre/post tool hooks)
//   - Auto compaction (via contextManager)
//   - Usage tracking
//   - Checkpoint support
//   - Vision support
func NewAgentLoop(
	cfg *config.Config,
	registry *tool.Registry,
	transport *Transport,
	session *Session,
	clientCaps ClientCapabilities,
	prov provider.Provider,
) *AgentLoop {
	systemPrompt := config.BuildSystemPrompt(cfg.ExtraPrompt, session.CWD, "", nil, "", nil, nil)
	maxIter := cfg.MaxIterations
	if maxIter == 0 {
		maxIter = 100
	}

	a := agent.NewAgent(prov, registry, systemPrompt, maxIter)
	a.SetWorkingDir(session.CWD)

	// --- Resolve endpoint for context window and model capabilities ---
	resolved, resolveErr := cfg.ResolveActiveEndpoint()
	if resolveErr != nil {
		debug.Log("acp", "warning: could not resolve endpoint: %v", resolveErr)
	}
	if resolveErr == nil {
		agentruntime.ApplyResolvedLimitsToAgent(a, resolved)
		agentruntime.StartAsyncRelayModelLimitRefresh(cfg, resolved, a, nil)
		a.SetSupportsVision(resolved.SupportsVision)
	}

	// --- Hook config ---
	a.SetHookConfig(cfg.Hooks)

	// --- Checkpoint manager ---
	a.SetCheckpointManager(checkpoint.NewManager(50))
	// #1047: the pre-write checkpoint hook is registered per ExecutePrompt run
	// (see ExecutePrompt) instead of the package global - the global was
	// overwritten by every NewAgentLoop, so with concurrent ACP sessions the
	// last-initialized loop received every session's checkpoint callbacks.

	// --- Default mode: auto ---
	defaultMode := "auto"

	al := &AgentLoop{
		cfg:        cfg,
		registry:   registry,
		transport:  transport,
		session:    session,
		clientCaps: clientCaps,
		agent:      a,
		mode:       defaultMode,
	}

	// --- Permission policy ---
	permMode := permission.AutoMode
	policy := permission.NewConfigPolicyWithMode(nil, cfg.AllowedDirs, permMode)
	a.SetPermissionPolicy(policy)

	// Route tool approvals through ACP permission request to Client
	a.SetApprovalHandler(func(ctx context.Context, toolName string, input string) permission.Decision {
		// #1085: Even in bypass/autopilot mode, respect the three hard security doors
		// (extremely dangerous commands, network exfiltration, sensitive path redirects).
		// Let the policy decide first; only Allow can skip client approval.
		//
		// #1093: consult the agent's CURRENT policy, not the `policy` local
		// captured here - applyModePolicy swaps the policy via SetPermissionPolicy
		// on SetMode, and a stale AutoMode closure let auto-allowed tools skip
		// client approval even after switching to supervised mode.
		currentPolicy := al.agent.PermissionPolicy()
		if currentPolicy == nil {
			currentPolicy = policy
		}
		decision, _ := currentPolicy.Check(toolName, json.RawMessage(input))
		if decision == permission.Allow {
			return permission.Allow
		}
		// Deny or Ask → ask the Client (or respect Deny)
		if decision == permission.Deny {
			return permission.Deny
		}

		// Ask case: request approval from Client (supervised/auto/bypass/autopilot all reach here)
		approved, err := al.RequestPermission(ctx, "tool_use",
			ToolCallUpdate{
				Title: fmt.Sprintf("Execute tool: %s", toolName),
				Kind:  ToolKindExecute,
			},
		)
		if err != nil {
			debug.Log("acp", "permission request error: %v", err)
			return permission.Deny
		}
		if approved {
			return permission.Allow
		}
		return permission.Deny
	})

	// Route diff confirmations through ACP permission request to Client
	a.SetDiffConfirm(func(ctx context.Context, filePath, diffText string) bool {
		mode := al.Mode()
		if mode == "bypass" || mode == "autopilot" {
			return true
		}
		if mode == "auto" {
			// Auto-approve file writes in auto mode
			return true
		}
		approved, err := al.RequestPermission(ctx, "fs_write",
			ToolCallUpdate{
				Title:     fmt.Sprintf("Write file: %s", filePath),
				Kind:      ToolKindEdit,
				Locations: []ToolCallLocation{{Path: filePath}},
			},
		)
		if err != nil {
			debug.Log("acp", "diff permission error: %v", err)
			return false
		}
		return approved
	})

	// --- Usage tracking ---
	a.SetUsageHandler(func(usage provider.TokenUsage) {
		debug.Log("acp", "token usage: input=%d output=%d total=%d", usage.InputTokens, usage.OutputTokens, usage.InputTokens+usage.OutputTokens)
	})

	// --- Checkpoint handler: persist compacted context to session ---
	a.SetCheckpointHandler(func(summaryMsgID, lastMsgID string, tokenCount int) {
		// ACP uses its own session storage, not JSONL checkpoints.
		// Save full message list to the ACP session.
		msgs, _ := a.ContextManager().MessagesAndTokenCount()
		acpMsgs := providerToACPMessage(msgs)
		al.session.ReplaceConversation(acpMsgs)
		if err := al.session.Save(al.session.SaveDir()); err != nil {
			debug.Log("acp", "checkpoint save failed: %v", err)
		} else {
			debug.Log("acp", "checkpoint saved: %d messages, %d tokens", len(acpMsgs), tokenCount)
		}
	})

	// --- Project memory ---
	al.loadProjectMemory()

	// --- AskUser handler: route through ACP permission request ---
	al.setupAskUserHandler()

	return al
}

// loadProjectMemory injects a project memory index hint as a system message
// into the agent's context. Full content is loaded on demand via read_file.
func (al *AgentLoop) loadProjectMemory() {
	files, err := memory.ProjectMemoryFilesForPath(al.session.CWD)
	if err != nil {
		debug.Log("acp", "project memory load failed: %v", err)
		return
	}
	if len(files) == 0 {
		return
	}
	al.agent.SetProjectMemoryFiles(files)
	hint := memory.BuildProjectMemoryHint(files)
	if hint != "" {
		al.agent.AddMessage(provider.Message{
			Role:    "system",
			Content: []provider.ContentBlock{{Type: "text", Text: hint}},
		})
	}
	debug.Log("acp", "loaded %d project memory files", len(files))
}

// RestoreConversation restores previously saved messages into the agent's context.
// Used by session/resume to rebuild the conversation history.
func (al *AgentLoop) RestoreConversation(messages []Message) {
	for _, msg := range messages {
		providerContent := acpToProviderContent(msg.Content)
		al.agent.AddMessage(provider.Message{
			Role:    msg.Role,
			Content: providerContent,
		})
	}
	debug.Log("acp", "restored %d messages to agent context", len(messages))
}

// SetMode updates the permission mode for this agent loop and marks it as
// explicitly set by the Client. Once set, the mode survives loop recreation
// between prompts (see Handler.applySessionModes) — it never silently reverts
// to the default "auto". Guarded by modeMu so concurrent reads from the
// approval/diff-confirm handlers stay race-free.
func (al *AgentLoop) SetMode(mode string) {
	al.modeMu.Lock()
	al.mode = mode
	al.modeLocked = true
	al.modeMu.Unlock()
	al.applyModePolicy(mode)
}

// SetModeDefault installs the initial mode without marking it as user-chosen.
func (al *AgentLoop) SetModeDefault(mode string) {
	al.modeMu.Lock()
	al.mode = mode
	al.modeMu.Unlock()
	al.applyModePolicy(mode)
}

// Mode returns the current permission mode.
func (al *AgentLoop) Mode() string {
	al.modeMu.Lock()
	defer al.modeMu.Unlock()
	return al.mode
}

// applyModePolicy rebuilds the permission policy for the given mode.
func (al *AgentLoop) applyModePolicy(mode string) {
	var permMode permission.PermissionMode
	switch mode {
	case "bypass":
		permMode = permission.BypassMode
	case "autopilot":
		permMode = permission.AutopilotMode
	case "auto":
		permMode = permission.AutoMode
	case "supervised":
		permMode = permission.SupervisedMode
	default:
		permMode = permission.AutoMode
	}
	policy := permission.NewConfigPolicyWithMode(nil, al.cfg.AllowedDirs, permMode)
	al.agent.SetPermissionPolicy(policy)
}

// ExecutePrompt runs a single prompt through the agent loop.
// Stream events are converted to ACP session/update notifications.
func (al *AgentLoop) ExecutePrompt(ctx context.Context, prompt []ContentBlock) error {
	ctx, cancel := context.WithCancel(ctx)
	al.cancelMu.Lock()
	al.cancelGen++
	myGen := al.cancelGen
	al.cancel = cancel
	al.cancelMu.Unlock()
	defer func() {
		// Clear only if this run's cancel is still installed (generation
		// check): a newer run may have replaced it, and funcs cannot be
		// compared directly in Go.
		al.cancelMu.Lock()
		if al.cancelGen == myGen {
			al.cancel = nil
		}
		al.cancelMu.Unlock()
		cancel()
	}()

	// #1047: scope the pre-write checkpoint hook to this run so concurrent
	// sessions do not overwrite each other's hook (package-global
	// last-writer-wins hijacked checkpoints to the newest loop). Tools only
	// execute within ExecutePrompt, so run scope covers all writes.
	removePreWriteHook := tool.AddPreWriteHook(tool.CheckpointSaver(al.agent.CheckpointManager()))
	defer removePreWriteHook()

	// Convert ACP ContentBlocks to provider ContentBlocks
	providerContent := acpToProviderContent(prompt)

	// Store user message in session
	al.session.AddMessage("user", prompt)

	// Accumulate assistant response for session persistence
	var assistantText string
	var toolCalls []ContentBlock

	// Run agent and convert stream events to ACP notifications
	onEvent := func(event provider.StreamEvent) {
		if err := al.handleStreamEvent(event); err != nil {
			debug.Log("acp", "error handling stream event: %v", err)
		}
		// Accumulate for persistence
		switch event.Type {
		case provider.StreamEventText:
			assistantText += event.Text
		case provider.StreamEventToolCallDone:
			toolCalls = append(toolCalls, ContentBlock{
				Type:     "tool_use",
				ToolName: event.Tool.Name,
				ToolID:   event.Tool.ID,
				Input:    event.Tool.Arguments,
			})
		case provider.StreamEventToolResult:
			toolCalls = append(toolCalls, ContentBlock{
				Type:     "tool_result",
				ToolID:   event.Tool.ID,
				ToolName: event.Tool.Name,
				Output:   event.Result,
				IsError:  event.IsError,
			})
		}
	}

	err := al.agent.RunStreamWithContent(ctx, providerContent, onEvent)
	if err != nil {
		return fmt.Errorf("agent execution: %w", err)
	}

	// Store assistant message in session for persistence.
	// #1048: tool_result blocks must NOT ride inside the assistant message:
	// the merged assistant+tool_result shape is an illegal request structure
	// for the Anthropic API after restore (tool results belong in a follow-up
	// user-role message). Split them so persisted sessions replay legally.
	var assistantContent []ContentBlock
	var toolResultBlocks []ContentBlock
	if assistantText != "" {
		assistantContent = append(assistantContent, ContentBlock{Type: "text", Text: assistantText})
	}
	for _, tc := range toolCalls {
		if tc.Type == "tool_result" {
			toolResultBlocks = append(toolResultBlocks, tc)
		} else {
			assistantContent = append(assistantContent, tc)
		}
	}
	if len(assistantContent) > 0 {
		al.session.AddMessage("assistant", assistantContent)
	}
	if len(toolResultBlocks) > 0 {
		al.session.AddMessage("user", toolResultBlocks)
	}

	// Persist session to disk
	if err := al.session.Save(al.session.SaveDir()); err != nil {
		debug.Log("acp", "session save error: %v", err)
	}

	return nil
}

// Stop cancels the current agent execution.
func (al *AgentLoop) Stop() {
	al.cancelMu.Lock()
	cancel := al.cancel
	al.cancelMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// handleStreamEvent converts a provider.StreamEvent to an ACP session/update notification.
func (al *AgentLoop) handleStreamEvent(event provider.StreamEvent) error {
	switch event.Type {
	case provider.StreamEventText:
		return al.transport.WriteNotification("session/update", SessionUpdateParams{
			SessionID: al.session.ID,
			Update: SessionUpdate{
				Type: "agent_message_chunk",
				Content: &ContentBlock{
					Type: "text",
					Text: event.Text,
				},
			},
		})

	case provider.StreamEventToolCallDone:
		kind := toolKind(event.Tool.Name)
		return al.transport.WriteNotification("session/update", SessionUpdateParams{
			SessionID: al.session.ID,
			Update: SessionUpdate{
				Type:       "tool_call",
				ToolCallID: event.Tool.ID,
				Title:      event.Tool.Name,
				Kind:       ToolKind(kind),
				Status:     "pending",
				RawInput:   event.Tool.Arguments,
			},
		})

	case provider.StreamEventToolResult:
		status := "completed"
		if event.IsError {
			status = "failed"
		}
		// tool_call_update is what the IDE actually renders — show human-readable command
		present := tool.DescribeTool(event.Tool.Name, string(event.Tool.Arguments))
		title := tool.FormatToolInline(present.DisplayName, present.Detail)
		return al.transport.WriteNotification("session/update", SessionUpdateParams{
			SessionID: al.session.ID,
			Update: SessionUpdate{
				Type:       "tool_call_update",
				ToolCallID: event.Tool.ID,
				Title:      title,
				Status:     ToolCallStatus(status),
			},
		})

	case provider.StreamEventDone:
		return nil

	case provider.StreamEventError:
		return al.transport.WriteNotification("session/update", SessionUpdateParams{
			SessionID: al.session.ID,
			Update: SessionUpdate{
				Type: "agent_message_chunk",
				Content: &ContentBlock{
					Type: "text",
					Text: "Error: " + event.Error.Error(),
				},
			},
		})
	}

	return nil
}

// toolKind maps tool names to ACP tool kinds: "read", "write", or "execute".
func toolKind(toolName string) string {
	switch toolName {
	case "read_file", "list_directory", "search_files", "glob", "grep", "code_search",
		"git_status", "git_diff", "git_log":
		return "read"
	case "write_file", "edit_file", "multi_edit_file", "batch_replace", "diff_apply":
		return "write"
	default:
		return "execute"
	}
}

// acpToProviderContent converts ACP ContentBlocks to provider ContentBlocks.
// #1086: Preserve tool_use/tool_result blocks during conversation restore.
// This mirrors providerToACPMessage which stores all fields to disk.
func acpToProviderContent(blocks []ContentBlock) []provider.ContentBlock {
	out := make([]provider.ContentBlock, 0, len(blocks))
	for _, b := range blocks {
		switch b.Type {
		case "text":
			out = append(out, provider.TextBlock(b.Text))
		case "image":
			out = append(out, provider.ImageBlock(b.ImageMIME, b.ImageData))
		case "tool_use":
			// #1086: Preserve tool_use block with all fields
			out = append(out, provider.ContentBlock{
				Type:     "tool_use",
				ToolName: b.ToolName,
				ToolID:   b.ToolID,
				Input:    b.Input,
				IsError:  b.IsError,
			})
		case "tool_result":
			// #1086: Preserve tool_result block with all fields
			out = append(out, provider.ContentBlock{
				Type:     "tool_result",
				ToolName: b.ToolName,
				ToolID:   b.ToolID,
				Output:   b.Output,
				IsError:  b.IsError,
			})
		default:
			if b.Text != "" {
				out = append(out, provider.TextBlock(b.Text))
			}
		}
	}
	return out
}

// providerToACPMessage converts provider Messages to ACP Messages.
// Used by the checkpoint handler to persist compacted context.
func providerToACPMessage(msgs []provider.Message) []Message {
	out := make([]Message, 0, len(msgs))
	for _, m := range msgs {
		blocks := make([]ContentBlock, 0, len(m.Content))
		for _, b := range m.Content {
			blocks = append(blocks, ContentBlock{
				Type:     b.Type,
				Text:     b.Text,
				ToolName: b.ToolName,
				ToolID:   b.ToolID,
				Input:    b.Input,
				Output:   b.Output,
				IsError:  b.IsError,
				// #1071: image fields must survive the roundtrip - dropping them
				// produced empty image blocks that vision providers reject with
				// a request-level 400 after compaction/restore.
				ImageMIME: b.ImageMIME,
				ImageData: b.ImageData,
			})
		}
		out = append(out, Message{Role: m.Role, Content: blocks})
	}
	return out
}

// ReadFile reads a file, using the Client's FS capability if available.
func (al *AgentLoop) ReadFile(ctx context.Context, path string) (string, error) {
	if al.clientCaps.FS != nil && al.clientCaps.FS.ReadTextFile {
		return al.requestClientReadFile(ctx, path)
	}
	absPath := path
	if !filepath.IsAbs(absPath) {
		absPath = filepath.Join(al.session.CWD, path)
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		return "", fmt.Errorf("reading file %s: %w", path, err)
	}
	return string(data), nil
}

// WriteFile writes a file, using the Client's FS capability if available.
func (al *AgentLoop) WriteFile(ctx context.Context, path, content string) error {
	if al.clientCaps.FS != nil && al.clientCaps.FS.WriteTextFile {
		return al.requestClientWriteFile(ctx, path, content)
	}
	absPath := path
	if !filepath.IsAbs(absPath) {
		absPath = filepath.Join(al.session.CWD, path)
	}
	dir := filepath.Dir(absPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating directory %s: %w", dir, err)
	}
	return os.WriteFile(absPath, []byte(content), 0o644)
}

// RequestPermission sends a permission request to the Client and waits for response.
func (al *AgentLoop) RequestPermission(ctx context.Context, permType string, toolCall ToolCallUpdate) (bool, error) {
	options := []PermissionOption{
		{OptionID: "allow", Name: "Allow", Kind: PermissionOptionAllowOnce},
		{OptionID: "reject", Name: "Reject", Kind: PermissionOptionRejectOnce},
	}

	result, err := al.transport.SendRequest(
		ctx,
		"session/request_permission",
		RequestPermissionRequest{
			SessionID: al.session.ID,
			ToolCall:  &toolCall,
			Options:   options,
		},
		5*time.Minute,
	)
	if err != nil {
		return false, fmt.Errorf("requesting permission: %w", err)
	}

	var response RequestPermissionResponse
	if err := json.Unmarshal(result, &response); err != nil {
		return false, fmt.Errorf("parsing permission response: %w", err)
	}

	return response.Outcome.Outcome == "selected" && response.Outcome.SelectedOption != nil && response.Outcome.SelectedOption.OptionID == "allow", nil
}

// requestClientReadFile requests the Client to read a file via fs/read_text_file.
func (al *AgentLoop) requestClientReadFile(ctx context.Context, path string) (string, error) {
	result, err := al.transport.SendRequest(
		ctx,
		"fs/read_text_file",
		FSReadTextFileParams{Path: path},
		30*time.Second,
	)
	if err != nil {
		return "", fmt.Errorf("requesting client read file: %w", err)
	}

	var response FSReadTextFileResult
	if err := json.Unmarshal(result, &response); err != nil {
		return "", fmt.Errorf("parsing read file response: %w", err)
	}
	return response.Content, nil
}

// requestClientWriteFile requests the Client to write a file via fs/write_text_file.
func (al *AgentLoop) requestClientWriteFile(ctx context.Context, path, content string) error {
	result, err := al.transport.SendRequest(
		ctx,
		"fs/write_text_file",
		FSWriteTextFileParams{Path: path, Content: content},
		30*time.Second,
	)
	if err != nil {
		return fmt.Errorf("requesting client write file: %w", err)
	}

	_ = result
	return nil
}

// setupAskUserHandler wires up the ask_user tool to route through ACP
// session/request_permission. For single/multi choice questions, the choices
// are mapped to PermissionOption entries. For text questions, we provide
// Submit/Cancel options (the IDE may support freeform text in the permission UI).
func (al *AgentLoop) setupAskUserHandler() {
	askTool, ok := al.registry.Get("ask_user")
	if !ok {
		return
	}
	askUser, ok := askTool.(*tool.AskUserTool)
	if !ok {
		return
	}

	askUser.SetHandler(func(ctx context.Context, req tool.AskUserRequest) (tool.AskUserResponse, error) {
		// Build permission options from the first question's choices
		var options []PermissionOption
		var question tool.AskUserQuestion

		if len(req.Questions) > 0 {
			question = req.Questions[0]
		}

		// Build title from request title or first question prompt
		title := req.Title
		if title == "" && question.Prompt != "" {
			title = question.Prompt
		}

		switch question.Kind {
		case tool.AskUserKindSingle, tool.AskUserKindMulti:
			// Map choices to permission options
			for _, choice := range question.Choices {
				options = append(options, PermissionOption{
					OptionID: choice.ID,
					Name:     choice.Label,
					Kind:     PermissionOptionAllowOnce,
				})
			}
			if len(options) == 0 {
				options = []PermissionOption{
					{OptionID: "ok", Name: "OK", Kind: PermissionOptionAllowOnce},
					{OptionID: "cancel", Name: "Cancel", Kind: PermissionOptionRejectOnce},
				}
			}

		case tool.AskUserKindText, "":
			// Text question — offer Submit/Cancel
			options = []PermissionOption{
				{OptionID: "submit", Name: "Submit", Kind: PermissionOptionAllowOnce},
				{OptionID: "cancel", Name: "Cancel", Kind: PermissionOptionRejectOnce},
			}

		default:
			options = []PermissionOption{
				{OptionID: "ok", Name: "OK", Kind: PermissionOptionAllowOnce},
				{OptionID: "cancel", Name: "Cancel", Kind: PermissionOptionRejectOnce},
			}
		}

		result, err := al.transport.SendRequest(
			context.Background(),
			"session/request_permission",
			RequestPermissionRequest{
				SessionID: al.session.ID,
				ToolCall: &ToolCallUpdate{
					Title: title,
					Kind:  ToolKindExecute,
				},
				Options: options,
			},
			5*time.Minute,
		)
		if err != nil {
			return tool.AskUserResponse{}, fmt.Errorf("ask_user permission request: %w", err)
		}

		var response RequestPermissionResponse
		if err := json.Unmarshal(result, &response); err != nil {
			return tool.AskUserResponse{}, fmt.Errorf("ask_user parse response: %w", err)
		}

		// Build AskUserResponse from permission response
		resp := tool.AskUserResponse{
			Status:        tool.AskUserStatusSubmitted,
			Title:         title,
			QuestionCount: len(req.Questions),
		}

		if response.Outcome.Outcome == "cancelled" || response.Outcome.Outcome == "rejected" {
			return tool.AskUserResponse{}, fmt.Errorf(
				"ask_user: the user dismissed the question in the IDE. " +
					"Please ask the user directly in your response text instead.",
			)
		}

		// User selected an option
		selectedID := ""
		if response.Outcome.SelectedOption != nil {
			selectedID = string(response.Outcome.SelectedOption.OptionID)
		}

		switch question.Kind {
		case tool.AskUserKindSingle:
			resp.Answers = append(resp.Answers, tool.AskUserAnswer{
				ID:                question.ID,
				Title:             question.Title,
				Kind:              tool.AskUserKindSingle,
				CompletionStatus:  tool.AskUserCompletionAnswered,
				AnswerMode:        tool.AskUserAnswerModeSelectionOnly,
				Answered:          true,
				SelectedChoiceIDs: []string{selectedID},
				SelectedChoices:   []string{selectedID},
			})
			resp.AnsweredCount = 1

		case tool.AskUserKindMulti:
			// Permission options only allow single selection, treat as single
			resp.Answers = append(resp.Answers, tool.AskUserAnswer{
				ID:                question.ID,
				Title:             question.Title,
				Kind:              tool.AskUserKindSingle,
				CompletionStatus:  tool.AskUserCompletionAnswered,
				AnswerMode:        tool.AskUserAnswerModeSelectionOnly,
				Answered:          true,
				SelectedChoiceIDs: []string{selectedID},
				SelectedChoices:   []string{selectedID},
			})
			resp.AnsweredCount = 1

		case tool.AskUserKindText, "":
			// Text question — IDE permission UI doesn't support freeform text input.
			// Return an error so the LLM falls back to asking in plain text.
			return tool.AskUserResponse{}, fmt.Errorf(
				"ask_user: the IDE does not support text input for this question. " +
					"Please ask the user directly in your response text instead.",
			)
		}

		return resp, nil
	})
}
