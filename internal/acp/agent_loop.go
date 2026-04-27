package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/agent"
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
	cancel     context.CancelFunc
	mode       string // permission mode: "supervised", "auto", "bypass", "autopilot"
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
	systemPrompt := cfg.SystemPrompt
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
		if resolved.ContextWindow > 0 {
			a.ContextManager().SetMaxTokens(resolved.ContextWindow)
		}
		if resolved.MaxTokens > 0 {
			a.ContextManager().SetOutputReserve(resolved.MaxTokens)
		}
		a.SetSupportsVision(resolved.SupportsVision)
	}

	// --- Hook config ---
	a.SetHookConfig(cfg.Hooks)

	// --- Checkpoint manager ---
	a.SetCheckpointManager(checkpoint.NewManager(50))

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
		// In auto mode (default), allow safe operations automatically;
		// only ask the Client for dangerous tools
		if al.mode == "bypass" || al.mode == "autopilot" {
			return permission.Allow
		}
		if al.mode == "auto" {
			// Auto mode: let the policy decide. If it says Ask, escalate to Client.
			decision, _ := policy.Check(toolName, json.RawMessage(input))
			if decision == permission.Allow {
				return permission.Allow
			}
			// Deny or Ask → ask the Client
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

	// Route diff confirmations through ACP permission request to Client
	a.SetDiffConfirm(func(ctx context.Context, filePath, diffText string) bool {
		if al.mode == "bypass" || al.mode == "autopilot" {
			return true
		}
		if al.mode == "auto" {
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

	// --- Project memory ---
	al.loadProjectMemory()

	return al
}

// loadProjectMemory loads project memory files (GGCODE.md, AGENTS.md, etc.)
// and injects them as a system message into the agent's context.
func (al *AgentLoop) loadProjectMemory() {
	content, files, err := memory.LoadProjectMemory(al.session.CWD)
	if err != nil {
		debug.Log("acp", "project memory load failed: %v", err)
		return
	}
	if content == "" {
		return
	}
	al.agent.SetProjectMemoryFiles(files)
	al.agent.AddMessage(provider.Message{
		Role:    "system",
		Content: []provider.ContentBlock{{Type: "text", Text: "## Project Memory\n" + content}},
	})
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

// SetMode updates the permission mode for this agent loop.
func (al *AgentLoop) SetMode(mode string) {
	al.mode = mode
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
	al.cancel = cancel
	defer cancel()

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

	// Store assistant message in session for persistence
	var assistantContent []ContentBlock
	if assistantText != "" {
		assistantContent = append(assistantContent, ContentBlock{Type: "text", Text: assistantText})
	}
	assistantContent = append(assistantContent, toolCalls...)
	if len(assistantContent) > 0 {
		al.session.AddMessage("assistant", assistantContent)
	}

	// Persist session to disk
	if err := al.session.Save(al.session.SaveDir()); err != nil {
		debug.Log("acp", "session save error: %v", err)
	}

	return nil
}

// Stop cancels the current agent execution.
func (al *AgentLoop) Stop() {
	if al.cancel != nil {
		al.cancel()
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
		// tool_call_update is what the IDE actually renders — show formatted command + args
		title := formatToolTitle(event.Tool.Name, event.Tool.Arguments)
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
	case "read_file", "list_directory", "search_files", "glob", "grep",
		"git_status", "git_diff", "git_log":
		return "read"
	case "write_file", "edit_file", "multi_edit", "diff_apply":
		return "write"
	default:
		return "execute"
	}
}

// acpToProviderContent converts ACP ContentBlocks to provider ContentBlocks.
func acpToProviderContent(blocks []ContentBlock) []provider.ContentBlock {
	out := make([]provider.ContentBlock, 0, len(blocks))
	for _, b := range blocks {
		switch b.Type {
		case "text":
			out = append(out, provider.TextBlock(b.Text))
		case "image":
			out = append(out, provider.ImageBlock(b.ImageMIME, b.ImageData))
		default:
			if b.Text != "" {
				out = append(out, provider.TextBlock(b.Text))
			}
		}
	}
	return out
}

// ReadFile reads a file, using the Client's FS capability if available.
func (al *AgentLoop) ReadFile(ctx context.Context, path string) (string, error) {
	if al.clientCaps.FS != nil && al.clientCaps.FS.ReadTextFile {
		return al.requestClientReadFile(path)
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
		return al.requestClientWriteFile(path, content)
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
func (al *AgentLoop) requestClientReadFile(path string) (string, error) {
	result, err := al.transport.SendRequest(
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
func (al *AgentLoop) requestClientWriteFile(path, content string) error {
	result, err := al.transport.SendRequest(
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

// formatToolTitle produces a human-readable tool title with argument preview,
// matching the TUI's formatToolInline style: "read_file {"path":"/tmp/test.go"}".
func formatToolTitle(toolName string, rawArgs json.RawMessage) string {
	preview := compactArgsPreview(string(rawArgs))
	if preview == "" {
		return toolName
	}
	return toolName + " " + preview
}

// compactArgsPreview parses raw JSON args and returns a compact single-line preview.
func compactArgsPreview(raw string) string {
	raw = strings.TrimSpace(raw)
	if isTrivialDetail(raw) {
		return ""
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return compactSingleLine(raw)
	}
	if len(args) == 0 {
		return ""
	}
	// Shorten file paths for display
	for _, key := range []string{"file_path", "path", "directory", "file", "filename"} {
		if v, ok := args[key].(string); ok {
			args[key] = shortenPathForDisplay(v)
		}
	}
	b, err := json.Marshal(args)
	if err != nil {
		return compactSingleLine(raw)
	}
	return compactSingleLine(string(b))
}

// isTrivialDetail returns true for empty or meaningless arg values.
func isTrivialDetail(value string) bool {
	switch strings.TrimSpace(value) {
	case "", "{}", "[]", "null":
		return true
	default:
		return false
	}
}

// compactSingleLine collapses whitespace and truncates to 120 chars.
func compactSingleLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 120 {
		return s[:120] + "..."
	}
	return s
}

// shortenPathForDisplay trims trailing slashes from paths.
func shortenPathForDisplay(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimRight(value, `/\`)
	return value
}
