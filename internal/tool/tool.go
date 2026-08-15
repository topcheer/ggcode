package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/provider"
)

// Result is the output returned to the LLM.
// IsError: true means the tool execution had a user-visible error (shown to LLM).
// The Go error return is for system-level failures only (panic recovery, etc).
// ToolProgressKey is a context key for tools that want to emit progress
// updates during execution. The value must be ToolProgressFunc.
// Used by wait_command to stream output lines during the wait period.
type ToolProgressKey struct{}

// ToolProgressFunc lets a running tool push intermediate output to the TUI.
// toolID and toolName identify the calling tool; output is the text to display.
type ToolProgressFunc func(toolID, toolName, output string)

type Result struct {
	Content string        `json:"content"`
	IsError bool          `json:"is_error"`
	Images  []ResultImage `json:"images,omitempty"`
	// SuggestedWorkingDir is an optional hint from a tool that the agent's
	// working directory should change. When non-empty, the agent loop will
	// update its WorkingDir to this value so subsequent tool calls (e.g.
	// run_command, read_file) operate in the new directory automatically.
	SuggestedWorkingDir string `json:"suggested_working_dir,omitempty"`
	// FollowUpMessages are additional messages injected after the tool_result.
	// Used by inline skills to inject skill instructions as a user message,
	// forcing the model to process and act on them.
	FollowUpMessages []provider.Message `json:"follow_up_messages,omitempty"`
}

// ResultImage carries a single image within a tool Result.
type ResultImage struct {
	MIME       string `json:"mime"`        // "image/png", "image/jpeg", etc.
	Base64     string `json:"base64"`      // base64-encoded image data
	Width      int    `json:"width"`       // original pixel width (0 if unknown)
	Height     int    `json:"height"`      // original pixel height (0 if unknown)
	SourcePath string `json:"source_path"` // file path the image was read from
}

// Tool is the interface every tool (built-in, MCP-adapted, or plugin) must implement.
type Tool interface {
	// Name returns the unique tool identifier (e.g., "read_file").
	Name() string

	// Description returns a human-readable description shown to the LLM.
	Description() string

	// Parameters returns a JSON Schema object describing the tool's input.
	// Must be a valid JSON object with "type": "object" at minimum.
	Parameters() json.RawMessage

	// Execute runs the tool with the given input and returns the result.
	Execute(ctx context.Context, input json.RawMessage) (Result, error)
}

// Closer is an optional interface for tools that hold resources (browser
// processes, network connections, file handles) that must be released when
// the agent shuts down. Tools that implement Close() will have it called
// during registry teardown via Registry.CloseAll().
type Closer interface {
	Close() error
}

// ToolMeta captures Gen 3 metadata for cost-aware and rate-aware tool selection.
// Based on 2026 best practices for Autonomous Tool Discovery (Data-Gate, Zylos).
type ToolMeta struct {
	// CostEstimate is the approximate cost per call in USD (or 0 if free/cached).
	// Helps the agent budget-aware tool selection.
	CostEstimate float64 `json:"cost_estimate,omitempty"`

	// RateLimitRPS is the maximum requests per second allowed by the tool's backend.
	// 0 means no explicit limit (local tools).
	RateLimitRPS float64 `json:"rate_limit_rps,omitempty"`

	// RetryPolicy describes retry behavior for transient failures.
	// "none": no retry, "fixed": fixed backoff, "exponential": exponential backoff.
	RetryPolicy string `json:"retry_policy,omitempty"`

	// MaxRetries is the maximum number of retry attempts before giving up.
	MaxRetries int `json:"max_retries,omitempty"`
}

// MetaProvider is an optional interface for tools that want to provide
// Gen 3 metadata for cost-aware and rate-aware selection.
type MetaProvider interface {
	ToolMeta() ToolMeta
}

// Registry manages the set of available tools.
type Registry struct {
	tools     map[string]Tool
	codeIndex *CodeIndexManager // optional: shared code index for @ fuzzy search
	mu        sync.RWMutex
}

// NewRegistry creates an empty tool registry.
func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]Tool)}
}

// CodeIndex returns the shared code index manager if one was registered.
func (r *Registry) CodeIndex() *CodeIndexManager {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.codeIndex
}

// Register adds a tool to the registry. Returns error if name is already taken.
func (r *Registry) Register(t Tool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tools[t.Name()]; exists {
		return fmt.Errorf("tool %q already registered", t.Name())
	}
	r.tools[t.Name()] = t
	return nil
}

// Get returns a tool by name.
func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// Unregister removes a tool by name.
func (r *Registry) Unregister(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.tools[name]; !ok {
		return false
	}
	delete(r.tools, name)
	return true
}

// List returns all registered tools.
func (r *Registry) List() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Tool, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t)
	}
	return out
}

// CloseAll calls Close() on every registered tool that implements Closer.
// This releases resources like browser processes, network connections, etc.
// Errors are collected but do not stop cleanup — all tools are attempted.
func (r *Registry) CloseAll() []error {
	r.mu.RLock()
	tools := make([]Tool, 0, len(r.tools))
	for _, t := range r.tools {
		tools = append(tools, t)
	}
	r.mu.RUnlock()

	var errs []error
	for _, t := range tools {
		if c, ok := t.(Closer); ok {
			if err := c.Close(); err != nil {
				errs = append(errs, fmt.Errorf("close %s: %w", t.Name(), err))
			}
		}
	}
	return errs
}

// ToDefinitions converts all available tools to provider.ToolDefinition for
// the LLM. Tools implementing AvailabilityChecker with Available()==false are
// excluded — e.g. restart before a host injects its requester (#346), so
// hosts without restart support never advertise a guaranteed-failing tool.
func (r *Registry) ToDefinitions() []provider.ToolDefinition {
	tools := r.List()
	defs := make([]provider.ToolDefinition, 0, len(tools))
	for _, t := range tools {
		if ac, ok := t.(AvailabilityChecker); ok && !ac.Available() {
			continue
		}
		defs = append(defs, provider.ToolDefinition{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters:  t.Parameters(),
		})
	}
	return defs
}

// AvailabilityChecker is an optional interface a Tool may implement to
// control whether it is advertised to the LLM. Registered-but-unavailable
// tools remain callable via Get() (direct invocation still returns a clear
// error) but are hidden from the tool list sent to the provider.
type AvailabilityChecker interface {
	Available() bool
}

// ToolNames returns the names of all registered tools.
func (r *Registry) ToolNames() []string {
	tools := r.List()
	names := make([]string, len(tools))
	for i, t := range tools {
		names[i] = t.Name()
	}
	return names
}

// GetMeta returns Gen 3 metadata for a tool if it implements MetaProvider.
// Returns zero-value ToolMeta and false if the tool doesn't provide metadata.
func (r *Registry) GetMeta(name string) (ToolMeta, bool) {
	if t, ok := r.Get(name); ok {
		if mp, ok := t.(MetaProvider); ok {
			return mp.ToolMeta(), true
		}
	}
	return ToolMeta{}, false
}

// Cloner is an optional interface that tools can implement to provide a deep copy.
// Tools that hold mutable state (e.g., WorkingDir) MUST implement Clone so that
// each agent gets its own independent tool instances. Tools without mutable state
// can safely skip this interface — they will be shared between agents.
//
// This is critical for correctness in concurrent scenarios (sub-agents, swarm
// teammates using different worktrees). Without cloning, syncToolWorkingDir would
// mutate a shared WorkingDir field, causing tool executions to land in the wrong
// directory.
type Cloner interface {
	Clone() Tool
}

// Clone creates a deep copy of the registry. Tools implementing the Cloner
// interface are individually cloned; others are shared (they are stateless).
// This is used when creating sub-agents and swarm teammates so each agent
// has its own independent tool instances with separate WorkingDir fields.
func (r *Registry) Clone() *Registry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	newReg := &Registry{tools: make(map[string]Tool, len(r.tools))}
	for name, t := range r.tools {
		if c, ok := t.(Cloner); ok {
			newReg.tools[name] = c.Clone()
		} else {
			// Stateless tool — safe to share the same instance.
			newReg.tools[name] = t
		}
	}
	return newReg
}

// CheckRequired validates that the given fields are non-empty (after trimming
// whitespace for string fields). Returns a user-friendly error message listing
// which fields are missing, or "" if all fields are present.
//
// Usage:
//
//	if msg := CheckRequired("file_path", args.FilePath, "old_text", args.OldText); msg != "" {
//	    return Result{IsError: true, Content: msg}, nil
//	}
func CheckRequired(fields ...string) string {
	var missing []string
	for i := 0; i+1 < len(fields); i += 2 {
		name := fields[i]
		value := fields[i+1]
		if strings.TrimSpace(value) == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return ""
	}
	if len(missing) == 1 {
		return fmt.Sprintf("missing required parameter: %s", missing[0])
	}
	return fmt.Sprintf("missing required parameters: %s", strings.Join(missing, ", "))
}
