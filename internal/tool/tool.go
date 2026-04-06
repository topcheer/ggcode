package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/topcheer/ggcode/internal/provider"
)

// Result is the output returned to the LLM.
// IsError: true means the tool execution had a user-visible error (shown to LLM).
// The Go error return is for system-level failures only (panic recovery, etc).
type Result struct {
	Content string `json:"content"`
	IsError bool   `json:"is_error"`
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

// Registry manages the set of available tools.
type Registry struct {
	tools map[string]Tool
	mu    sync.RWMutex
}

// NewRegistry creates an empty tool registry.
func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]Tool)}
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

// ToDefinitions converts all tools to provider.ToolDefinition for the LLM.
func (r *Registry) ToDefinitions() []provider.ToolDefinition {
	tools := r.List()
	defs := make([]provider.ToolDefinition, len(tools))
	for i, t := range tools {
		defs[i] = provider.ToolDefinition{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters:  t.Parameters(),
		}
	}
	return defs
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
