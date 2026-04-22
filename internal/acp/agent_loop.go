package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/config"
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
		maxIter = 100 // sensible default for ACP
	}

	a := agent.NewAgent(prov, registry, systemPrompt, maxIter)
	a.SetWorkingDir(session.CWD)

	al := &AgentLoop{
		cfg:        cfg,
		registry:   registry,
		transport:  transport,
		session:    session,
		clientCaps: clientCaps,
		agent:      a,
		mode:       "supervised", // default: require Client approval
	}

	// Use supervised mode — tools require Client approval via ACP permission flow
	policy := permission.NewConfigPolicyWithMode(nil, cfg.AllowedDirs, permission.SupervisedMode)
	a.SetPermissionPolicy(policy)

	// Route tool approvals through ACP permission request to Client
	a.SetApprovalHandler(func(ctx context.Context, toolName string, input string) permission.Decision {
		// In bypass/autopilot mode, auto-approve
		if al.mode == "bypass" || al.mode == "autopilot" {
			return permission.Allow
		}

		approved, err := al.RequestPermission(ctx, PermissionRequest{
			Type:        "tool_use",
			Description: fmt.Sprintf("Execute tool: %s", toolName),
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "acp: permission request error: %v\n", err)
			return permission.Deny
		}
		if approved {
			return permission.Allow
		}
		return permission.Deny
	})

	// Route diff confirmations through ACP permission request to Client
	a.SetDiffConfirm(func(ctx context.Context, filePath, diffText string) bool {
		// In bypass/autopilot mode, auto-approve
		if al.mode == "bypass" || al.mode == "autopilot" {
			return true
		}

		approved, err := al.RequestPermission(ctx, PermissionRequest{
			Type:        "fs_write",
			Path:        filePath,
			Description: fmt.Sprintf("Write file: %s", filePath),
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "acp: diff permission error: %v\n", err)
			return false
		}
		return approved
	})

	return al
}

// SetMode updates the permission mode for this agent loop.
func (al *AgentLoop) SetMode(mode string) {
	al.mode = mode
	// Also update the agent's permission policy
	var permMode permission.PermissionMode
	switch mode {
	case "bypass":
		permMode = permission.BypassMode
	case "autopilot":
		permMode = permission.AutopilotMode
	case "auto":
		permMode = permission.AutoMode
	default:
		permMode = permission.SupervisedMode
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
			fmt.Fprintf(os.Stderr, "acp: error handling stream event: %v\n", err)
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
				SessionUpdateType: "agent_message_chunk",
				Content: &ContentBlock{
					Type: "text",
					Text: event.Text,
				},
			},
		})

	case provider.StreamEventToolCallDone:
		// Determine tool kind based on tool name
		kind := toolKind(event.Tool.Name)
		return al.transport.WriteNotification("session/update", SessionUpdateParams{
			SessionID: al.session.ID,
			Update: SessionUpdate{
				SessionUpdateType: "tool_call",
				ToolCall: &ToolCallUpdate{
					ToolCallID: event.Tool.ID,
					Title:      event.Tool.Name,
					Kind:       kind,
					Status:     "running",
					RawInput:   string(event.Tool.Arguments),
				},
				Content: &ContentBlock{
					Type:     "tool_use",
					ToolName: event.Tool.Name,
					ToolID:   event.Tool.ID,
					Input:    event.Tool.Arguments,
				},
			},
		})

	case provider.StreamEventToolResult:
		status := "completed"
		if event.IsError {
			status = "failed"
		}
		return al.transport.WriteNotification("session/update", SessionUpdateParams{
			SessionID: al.session.ID,
			Update: SessionUpdate{
				SessionUpdateType: "tool_result",
				ToolCall: &ToolCallUpdate{
					ToolCallID: event.Tool.ID,
					Title:      event.Tool.Name,
					Status:     status,
					RawOutput:  event.Result,
				},
				Content: &ContentBlock{
					Type:     "tool_result",
					ToolID:   event.Tool.ID,
					ToolName: event.Tool.Name,
					Output:   event.Result,
					IsError:  event.IsError,
				},
			},
		})

	case provider.StreamEventDone:
		// ACP spec: send a final agent_message_chunk with empty content to signal completion
		// followed by no further updates
		return nil

	case provider.StreamEventError:
		return al.transport.WriteNotification("session/update", SessionUpdateParams{
			SessionID: al.session.ID,
			Update: SessionUpdate{
				SessionUpdateType: "agent_message_chunk",
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
			// Pass through as text for unsupported types
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
	// Fallback: read directly
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
	// Fallback: write directly
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
// This sends a JSON-RPC request (not a notification) and blocks until the Client responds.
func (al *AgentLoop) RequestPermission(ctx context.Context, req PermissionRequest) (bool, error) {
	result, err := al.transport.SendRequest(
		"session/request_permission",
		PermissionRequestParams{
			SessionID: al.session.ID,
			Request:   req,
		},
		5*time.Minute,
	)
	if err != nil {
		return false, fmt.Errorf("requesting permission: %w", err)
	}

	// Parse the Client's approval response
	var response struct {
		Approved bool `json:"approved"`
	}
	if err := json.Unmarshal(result, &response); err != nil {
		return false, fmt.Errorf("parsing permission response: %w", err)
	}

	return response.Approved, nil
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

	// Response is empty on success
	_ = result
	return nil
}
