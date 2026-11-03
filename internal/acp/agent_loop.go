package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
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

type AgentLoop struct {
	cfg        *config.Config
	registry   *tool.Registry
	transport  *Transport
	session    *Session
	clientCaps ClientCapabilities
	agent      *agent.Agent
	cancelMu   sync.Mutex
	cancel     context.CancelFunc
	cancelGen  uint64
	modeMu     sync.Mutex
	mode       string
	modeLocked bool
}

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

	resolved, resolveErr := cfg.ResolveActiveEndpoint()
	if resolveErr != nil {
		debug.Log("acp", "warning: could not resolve endpoint: %v", resolveErr)
	}
	if resolveErr == nil {
		agentruntime.ApplyResolvedLimitsToAgent(a, resolved)
		agentruntime.StartAsyncRelayModelLimitRefresh(cfg, resolved, a, nil)
		a.SetSupportsVision(resolved.SupportsVision)
	}

	a.SetHookConfig(cfg.Hooks)

	a.SetCheckpointManager(checkpoint.NewPersistentManager(50, session.CWD))

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

	permMode := permission.SupervisedMode
	policy := permission.NewConfigPolicyWithMode(nil, cfg.AllowedDirs, permMode)
	a.SetPermissionPolicy(policy)

	a.SetApprovalHandler(func(ctx context.Context, toolName string, input string) permission.Decision {
		currentPolicy := al.agent.PermissionPolicy()
		if currentPolicy == nil {
			currentPolicy = policy
		}
		decision, _ := currentPolicy.Check(toolName, json.RawMessage(input))
		if decision == permission.Allow {
			return permission.Allow
		}
		if decision == permission.Deny {
			return permission.Deny
		}

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

	a.SetDiffConfirm(func(ctx context.Context, filePath, diffText string) bool {
		mode := al.Mode()
		if mode == "bypass" || mode == "autopilot" {
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

	a.SetUsageHandler(func(usage provider.TokenUsage) {
		debug.Log("acp", "token usage: input=%d output=%d total=%d", usage.InputTokens, usage.OutputTokens, usage.InputTokens+usage.OutputTokens)
	})

	a.SetCheckpointHandler(func(summaryMsgID, lastMsgID string, tokenCount int) {
		msgs, _ := a.ContextManager().MessagesAndTokenCount()
		acpMsgs := providerToACPMessage(msgs)
		al.session.ReplaceConversation(acpMsgs)
		if err := al.session.Save(al.session.SaveDir()); err != nil {
			debug.Log("acp", "checkpoint save failed: %v", err)
		} else {
			debug.Log("acp", "checkpoint saved: %d messages, %d tokens", len(acpMsgs), tokenCount)
		}
	})

	al.loadProjectMemory()

	al.setupAskUserHandler()

	return al
}

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
	hint := memory.BuildProjectMemoryHint(files, al.session.CWD)
	if hint != "" {
		al.agent.AddMessage(provider.Message{
			Role:    "system",
			Content: []provider.ContentBlock{{Type: "text", Text: hint}},
		})
	}
	debug.Log("acp", "loaded %d project memory files", len(files))
}

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

func (al *AgentLoop) SetMode(mode string) {
	al.modeMu.Lock()
	al.mode = mode
	al.modeLocked = true
	al.modeMu.Unlock()
	al.applyModePolicy(mode)
}

func (al *AgentLoop) Mode() string {
	al.modeMu.Lock()
	defer al.modeMu.Unlock()
	return al.mode
}

func (al *AgentLoop) applyModePolicy(mode string) {
	permMode := permission.ParsePermissionMode(mode)
	policy := permission.NewConfigPolicyWithMode(nil, al.cfg.AllowedDirs, permMode)
	al.agent.SetPermissionPolicy(policy)
}

func (al *AgentLoop) ExecutePrompt(ctx context.Context, prompt []ContentBlock) error {
	ctx, cancel := context.WithCancel(ctx)
	al.cancelMu.Lock()
	al.cancelGen++
	myGen := al.cancelGen
	al.cancel = cancel
	al.cancelMu.Unlock()
	defer func() {
		al.cancelMu.Lock()
		if al.cancelGen == myGen {
			al.cancel = nil
		}
		al.cancelMu.Unlock()
		cancel()
	}()

	removePreWriteHook := tool.AddPreWriteHook(tool.CheckpointSaver(al.agent.CheckpointManager()))
	defer removePreWriteHook()

	providerContent := acpToProviderContent(prompt)

	al.session.AddMessage("user", prompt)

	var assistantText string
	var toolCalls []ContentBlock

	onEvent := func(event provider.StreamEvent) {
		if err := al.handleStreamEvent(event); err != nil {
			debug.Log("acp", "error handling stream event: %v", err)
		}
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

	ctx = context.WithValue(ctx, acpAskUserSessionKey{}, al.session.ID)

	err := al.agent.RunStreamWithContent(ctx, providerContent, onEvent, "human")
	if err != nil {
		return fmt.Errorf("agent execution: %w", err)
	}

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

	if err := al.session.Save(al.session.SaveDir()); err != nil {
		debug.Log("acp", "session save error: %v", err)
	}

	return nil
}

func (al *AgentLoop) Stop() {
	al.cancelMu.Lock()
	cancel := al.cancel
	al.cancelMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

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

func acpToProviderContent(blocks []ContentBlock) []provider.ContentBlock {
	out := make([]provider.ContentBlock, 0, len(blocks))
	for _, b := range blocks {
		switch b.Type {
		case "text":
			out = append(out, provider.TextBlock(b.Text))
		case "image":
			out = append(out, provider.ImageBlock(b.ImageMIME, b.ImageData))
		case "tool_use":
			out = append(out, provider.ContentBlock{
				Type:     "tool_use",
				ToolName: b.ToolName,
				ToolID:   b.ToolID,
				Input:    b.Input,
				IsError:  b.IsError,
			})
		case "tool_result":
			out = append(out, provider.ContentBlock{
				Type:     "tool_result",
				ToolName: b.ToolName,
				ToolID:   b.ToolID,
				Output:   b.Output,
				IsError:  b.IsError,
			})
		case "resource":
			if b.Resource != nil {
				if b.Resource.Text != nil && b.Resource.Text.Text != "" {
					out = append(out, provider.TextBlock(b.Resource.Text.Text))
				} else if b.Resource.Blob != nil {
					debug.Log("acp-bridge", "resource blob %q (%s) not inlinable - placeholder only", b.Resource.Blob.URI, b.Resource.Blob.MIMEType)
					out = append(out, provider.TextBlock(fmt.Sprintf("[embedded binary resource: %s (%s)]", b.Resource.Blob.URI, b.Resource.Blob.MIMEType)))
				} else {
					debug.Log("acp-bridge", "dropping resource block (no text and no blob content)")
				}
			} else {
				debug.Log("acp-bridge", "dropping resource block with nil resource object (malformed payload)")
			}
		default:
			if b.Text != "" {
				out = append(out, provider.TextBlock(b.Text))
			} else {
				debug.Log("acp-bridge", "dropping non-text content block type=%q (no text fallback)", b.Type)
			}
		}
	}
	return out
}

func providerToACPMessage(msgs []provider.Message) []Message {
	out := make([]Message, 0, len(msgs))
	for _, m := range msgs {
		blocks := make([]ContentBlock, 0, len(m.Content))
		for _, b := range m.Content {
			blocks = append(blocks, ContentBlock{
				Type:      b.Type,
				Text:      b.Text,
				ToolName:  b.ToolName,
				ToolID:    b.ToolID,
				Input:     b.Input,
				Output:    b.Output,
				IsError:   b.IsError,
				ImageMIME: b.ImageMIME,
				ImageData: b.ImageData,
			})
		}
		out = append(out, Message{Role: m.Role, Content: blocks})
	}
	return out
}

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

type acpAskUserSessionKey struct{}

var acpAskUserMu sync.Mutex

type acpAskUserSession struct {
	mu       sync.RWMutex
	handlers map[string]tool.AskUserHandler
	base     *tool.AskUserTool
}

func newACPAskUserSession(base *tool.AskUserTool) *acpAskUserSession {
	return &acpAskUserSession{
		handlers: make(map[string]tool.AskUserHandler),
		base:     base,
	}
}

func (s *acpAskUserSession) setHandler(sessionID string, h tool.AskUserHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers[sessionID] = h
}

func (s *acpAskUserSession) handlerFor(ctx context.Context) (tool.AskUserHandler, bool) {
	sessionID, _ := ctx.Value(acpAskUserSessionKey{}).(string)
	if sessionID == "" {
		return nil, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	h, ok := s.handlers[sessionID]
	return h, ok
}

func (s *acpAskUserSession) Name() string { return s.base.Name() }

func sessionIDOf(ctx context.Context) string {
	id, _ := ctx.Value(acpAskUserSessionKey{}).(string)
	return id
}

func (s *acpAskUserSession) Description() string { return s.base.Description() }

func (s *acpAskUserSession) Parameters() json.RawMessage { return s.base.Parameters() }

func (s *acpAskUserSession) Execute(ctx context.Context, input json.RawMessage) (tool.Result, error) {
	var req tool.AskUserRequest
	if err := json.Unmarshal(input, &req); err != nil {
		return tool.Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}
	handler, ok := s.handlerFor(ctx)
	if !ok {
		tag := sessionIDOf(ctx)
		if tag == "" {
			return s.base.Execute(ctx, input)
		}
		return tool.Result{IsError: true, Content: fmt.Sprintf("ask_user has no handler for ACP session %q", tag)}, nil
	}
	resp, err := handler(ctx, req)
	if err != nil {
		return tool.Result{IsError: true, Content: fmt.Sprintf("ask_user failed: %v", err)}, nil
	}
	if strings.TrimSpace(resp.Status) == "" {
		resp.Status = tool.AskUserStatusSubmitted
	}
	if strings.TrimSpace(resp.Title) == "" {
		resp.Title = req.Title
	}
	if resp.QuestionCount == 0 {
		resp.QuestionCount = len(req.Questions)
	}
	if resp.AnsweredCount == 0 && len(resp.Answers) > 0 {
		for _, answer := range resp.Answers {
			if answer.Answered {
				resp.AnsweredCount++
			}
		}
	}
	data, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		return tool.Result{IsError: true, Content: fmt.Sprintf("marshal ask_user response: %v", err)}, nil
	}
	return tool.Result{Content: string(data)}, nil
}

func ensureAskUserDispatcher(registry *tool.Registry, base *tool.AskUserTool) *acpAskUserSession {
	acpAskUserMu.Lock()
	defer acpAskUserMu.Unlock()

	cur, _ := registry.Get("ask_user")
	if disp, ok := cur.(*acpAskUserSession); ok {
		return disp
	}
	disp := newACPAskUserSession(base)
	if !registry.Unregister("ask_user") {
		return nil
	}
	if err := registry.Register(disp); err != nil {
		_ = registry.Register(base)
		return nil
	}
	return disp
}

func askUserPermissionOptions(question tool.AskUserQuestion) []PermissionOption {
	var options []PermissionOption
	switch question.Kind {
	case tool.AskUserKindSingle, tool.AskUserKindMulti:
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

	default:
		options = []PermissionOption{
			{OptionID: "ok", Name: "OK", Kind: PermissionOptionAllowOnce},
			{OptionID: "cancel", Name: "Cancel", Kind: PermissionOptionRejectOnce},
		}
	}
	return options
}

func (al *AgentLoop) setupAskUserHandler() {
	askTool, ok := al.registry.Get("ask_user")
	if !ok {
		return
	}
	var base *tool.AskUserTool
	switch t := askTool.(type) {
	case *tool.AskUserTool:
		base = t
	case *acpAskUserSession:
		base = t.base
	default:
		return
	}

	handler := func(ctx context.Context, req tool.AskUserRequest) (tool.AskUserResponse, error) {

		title := req.Title
		if len(req.Questions) > 0 && title == "" && req.Questions[0].Prompt != "" {
			title = req.Questions[0].Prompt
		}

		resp := tool.AskUserResponse{
			Status:        tool.AskUserStatusSubmitted,
			Title:         title,
			QuestionCount: len(req.Questions),
		}

		total := max(len(req.Questions), 1)
		for i := 0; i < total; i++ {
			var question tool.AskUserQuestion
			if i < len(req.Questions) {
				question = req.Questions[i]
			}
			options := askUserOptionsFor(question)

			result, err := al.sendAskUserPermission(ctx, title, options)
			if err != nil {
				return tool.AskUserResponse{}, fmt.Errorf("ask_user permission request for question %q: %w", question.ID, err)
			}

			var response RequestPermissionResponse
			if err := json.Unmarshal(result, &response); err != nil {
				return tool.AskUserResponse{}, fmt.Errorf("ask_user parse response for question %q: %w", question.ID, err)
			}

			selectedID, ok := requestPermissionSelectedID(response)
			if !ok {
				return tool.AskUserResponse{}, fmt.Errorf(
					"ask_user: the user dismissed the question in the IDE. " +
						"Please ask the user directly in your response text instead.",
				)
			}

			switch question.Kind {
			case tool.AskUserKindSingle, tool.AskUserKindMulti:
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
				resp.AnsweredCount++

			case tool.AskUserKindText, "":
				return tool.AskUserResponse{}, fmt.Errorf(
					"ask_user: the IDE does not support text input for question %q (%s). "+
						"Please ask the user directly in your response text instead.",
					question.ID, question.Title,
				)
			}
		}

		return resp, nil
	}

	disp := ensureAskUserDispatcher(al.registry, base)
	if disp == nil {
		debug.Log("acp", "ask_user dispatcher installation failed for session %s", al.session.ID)
		return
	}
	disp.setHandler(al.session.ID, handler)
	base.SetHandler(handler)
	debug.Log("acp", "ask_user handler registered for session %s", al.session.ID)
}

func (al *AgentLoop) sendAskUserPermission(ctx context.Context, title string, options []PermissionOption) (json.RawMessage, error) {
	return al.transport.SendRequest(
		ctx,
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
}

func askUserOptionsFor(question tool.AskUserQuestion) []PermissionOption {
	switch question.Kind {
	case tool.AskUserKindSingle, tool.AskUserKindMulti:
		return askUserPermissionOptions(question)
	case tool.AskUserKindText, "":
		return []PermissionOption{
			{OptionID: "submit", Name: "Submit", Kind: PermissionOptionAllowOnce},
			{OptionID: "cancel", Name: "Cancel", Kind: PermissionOptionRejectOnce},
		}
	default:
		return []PermissionOption{
			{OptionID: "ok", Name: "OK", Kind: PermissionOptionAllowOnce},
			{OptionID: "cancel", Name: "Cancel", Kind: PermissionOptionRejectOnce},
		}
	}
}

func requestPermissionSelectedID(resp RequestPermissionResponse) (string, bool) {
	switch resp.Outcome.Outcome {
	case "cancelled", "rejected":
		return "", false
	}
	if resp.Outcome.SelectedOption != nil {
		return string(resp.Outcome.SelectedOption.OptionID), true
	}
	return "", false
}
