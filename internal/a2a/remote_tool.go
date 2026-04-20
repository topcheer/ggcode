package a2a

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/tool"
)

// RemoteTool lets a ggcode agent discover and call other ggcode instances
// via A2A protocol. This is for agent-to-agent communication within ggcode
// itself — NOT for external MCP clients.
//
// The agent says "call the order-service and ask it to list orders" and this
// tool figures out which instance matches "order-service", connects to it,
// and returns the result.
type RemoteTool struct {
	mu       sync.RWMutex
	registry *Registry
	apiKey   string
	cache    []InstanceInfo
	cacheAt  time.Time
}

// NewRemoteTool creates an A2A remote tool for agent-to-agent calls.
func NewRemoteTool(reg *Registry, apiKey string) *RemoteTool {
	return &RemoteTool{registry: reg, apiKey: apiKey}
}

func (t *RemoteTool) Name() string { return "a2a_remote" }

func (t *RemoteTool) Description() string {
	return "Call a remote ggcode instance via A2A protocol. Use this to delegate tasks to other ggcode instances running in different workspaces. The target is identified by project name (e.g. 'order-service', 'user-service')."
}

func (t *RemoteTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"target": {
				"type": "string",
				"description": "Target instance name (project name, e.g. 'order-service', 'user-service', 'gateway'). Use 'list' to see all available instances."
			},
			"skill": {
				"type": "string",
				"description": "Skill to invoke on the target instance",
				"enum": ["code-edit", "file-search", "command-exec", "git-ops", "code-review", "full-task"]
			},
			"message": {
				"type": "string",
				"description": "Task description to send to the target instance"
			}
		},
		"required": ["target", "skill", "message"]
	}`)
}

func (t *RemoteTool) Execute(ctx context.Context, input json.RawMessage) (tool.Result, error) {
	var params struct {
		Target  string `json:"target"`
		Skill   string `json:"skill"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return tool.Result{Content: fmt.Sprintf("Invalid input: %v", err), IsError: true}, nil
	}

	// Special case: list all instances.
	if params.Target == "list" || params.Target == "" {
		return t.listInstances(ctx)
	}

	// Find the target instance.
	inst, err := t.findInstance(params.Target)
	if err != nil {
		return tool.Result{Content: err.Error(), IsError: true}, nil
	}

	// Create A2A client and send task.
	client := NewClient(inst.Endpoint, t.apiKey)
	task, err := client.SendMessage(ctx, params.Skill, params.Message)
	if err != nil {
		return tool.Result{
			Content: fmt.Sprintf("A2A call to %s failed: %v", params.Target, err),
			IsError: true,
		}, nil
	}

	// Format result.
	result := fmt.Sprintf("📋 Task sent to %s (%s)\n", params.Target, inst.Workspace)
	result += fmt.Sprintf("Task ID: %s\n", task.ID)
	result += fmt.Sprintf("Status: %s\n", task.Status.State)

	if len(task.Artifacts) > 0 {
		for i, a := range task.Artifacts {
			for _, p := range a.Parts {
				if p.Kind == "text" && p.Text != "" {
					result += fmt.Sprintf("\n--- Artifact %d ---\n%s\n", i+1, p.Text)
				}
			}
		}
	}

	// Include last agent message from history if available.
	if len(task.History) > 0 {
		lastMsg := task.History[len(task.History)-1]
		if lastMsg.Role == "agent" && len(lastMsg.Parts) > 0 {
			result += fmt.Sprintf("\n--- Agent Response ---\n%s\n", lastMsg.Parts[0].Text)
		}
	}

	return tool.Result{Content: result}, nil
}

// listInstances returns a formatted list of all discovered instances.
func (t *RemoteTool) listInstances(ctx context.Context) (tool.Result, error) {
	instances, err := t.discover()
	if err != nil {
		return tool.Result{Content: fmt.Sprintf("Discover failed: %v", err), IsError: true}, nil
	}
	if len(instances) == 0 {
		return tool.Result{Content: "No other ggcode instances found. Start ggcode in other workspaces to enable A2A."}, nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d ggcode instance(s):\n\n", len(instances)))
	for _, inst := range instances {
		name := filepath.Base(inst.Workspace)
		sb.WriteString(fmt.Sprintf("• %s\n", name))
		sb.WriteString(fmt.Sprintf("  Workspace: %s\n", inst.Workspace))
		sb.WriteString(fmt.Sprintf("  Endpoint: %s\n", inst.Endpoint))
		sb.WriteString(fmt.Sprintf("  Status: %s\n", inst.Status))
		sb.WriteString(fmt.Sprintf("  Started: %s\n\n", inst.StartedAt))
	}
	return tool.Result{Content: sb.String()}, nil
}

// findInstance locates an instance by name (partial match on workspace basename).
func (t *RemoteTool) findInstance(target string) (*InstanceInfo, error) {
	instances, err := t.discover()
	if err != nil {
		return nil, fmt.Errorf("discover failed: %v", err)
	}

	target = strings.ToLower(target)
	for _, inst := range instances {
		name := strings.ToLower(filepath.Base(inst.Workspace))
		if name == target || strings.Contains(name, target) {
			return &inst, nil
		}
	}

	// List available instances in error message.
	var names []string
	for _, inst := range instances {
		names = append(names, filepath.Base(inst.Workspace))
	}
	return nil, fmt.Errorf("no instance matching %q found. Available: %s", target, strings.Join(names, ", "))
}

// discover returns cached instances or refreshes from registry if stale (>10s).
func (t *RemoteTool) discover() ([]InstanceInfo, error) {
	t.mu.RLock()
	if t.cache != nil && time.Since(t.cacheAt) < 10*time.Second {
		cache := t.cache
		t.mu.RUnlock()
		return cache, nil
	}
	t.mu.RUnlock()

	instances, err := t.registry.Discover()
	if err != nil {
		return nil, err
	}

	t.mu.Lock()
	t.cache = instances
	t.cacheAt = time.Now()
	t.mu.Unlock()

	return instances, nil
}

// RefreshCache forces a cache refresh. Called periodically by background goroutine.
func (t *RemoteTool) RefreshCache() {
	instances, err := t.registry.Discover()
	if err != nil {
		return
	}
	t.mu.Lock()
	t.cache = instances
	t.cacheAt = time.Now()
	t.mu.Unlock()
}
