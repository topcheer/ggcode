package a2a

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/tool"
)

type RemoteTool struct {
	mu       sync.RWMutex
	registry *Registry
	apiKey   string
	cache    []InstanceInfo
	cacheAt  time.Time
}

func NewRemoteTool(reg *Registry, apiKey string) *RemoteTool {
	if apiKey == "" {
		apiKey = config.DefaultA2AAPIKey
	}
	return &RemoteTool{registry: reg, apiKey: apiKey}
}

func (t *RemoteTool) Name() string { return "a2a_remote" }

func (t *RemoteTool) Description() string {
	return "Delegate a code-editing task to a remote ggcode instance via A2A protocol. " +
		"Use this for headless code tasks (e.g. \"edit file X in project Y\", \"review code in user-service\") — NOT for asking questions or chatting. " +
		"For real-time questions, status checks, or collaboration with other agents/users on the LAN, use the 'lanchat' tool instead. " +
		"The target is identified by project name (e.g. 'order-service', 'user-service')."
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

	if params.Target == "list" || params.Target == "" {
		return t.listInstances(ctx)
	}

	inst, err := t.findInstance(params.Target)
	if err != nil {
		return tool.Result{Content: err.Error(), IsError: true}, nil
	}

	client := NewClient(inst.Endpoint, t.apiKey)
	task, err := client.SendMessage(ctx, params.Skill, params.Message)
	if err != nil {
		return tool.Result{
			Content: fmt.Sprintf("A2A call to %s failed: %v", params.Target, err),
			IsError: true,
		}, nil
	}

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

	if len(task.History) > 0 {
		lastMsg := task.History[len(task.History)-1]
		if lastMsg.Role == "agent" && len(lastMsg.Parts) > 0 {
			result += fmt.Sprintf("\n--- Agent Response ---\n%s\n", lastMsg.Parts[0].Text)
		}
	}

	return tool.Result{Content: result}, nil
}

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
		sb.WriteString(fmt.Sprintf("• %s\n", inst.DisplayName()))
		sb.WriteString(fmt.Sprintf("  Workspace: %s\n", inst.Workspace))
		sb.WriteString(fmt.Sprintf("  Endpoint: %s\n", inst.Endpoint))
		sb.WriteString(fmt.Sprintf("  Status: %s\n", inst.Status))
		sb.WriteString(fmt.Sprintf("  Started: %s\n\n", inst.StartedAt))
	}
	return tool.Result{Content: sb.String()}, nil
}

func (t *RemoteTool) findInstance(target string) (*InstanceInfo, error) {
	instances, err := t.discover()
	if err != nil {
		return nil, fmt.Errorf("discover failed: %v", err)
	}
	if len(instances) == 0 {
		return nil, fmt.Errorf("no ggcode instances found")
	}

	target = strings.ToLower(target)

	for i := range instances {
		if strings.ToLower(instances[i].DisplayName()) == target {
			return &instances[i], nil
		}
	}

	var exactMatches []InstanceInfo
	for _, inst := range instances {
		if strings.ToLower(filepath.Base(inst.Workspace)) == target {
			exactMatches = append(exactMatches, inst)
		}
	}
	if len(exactMatches) == 1 {
		return &exactMatches[0], nil
	}
	if len(exactMatches) > 1 {
		var names []string
		for _, inst := range exactMatches {
			names = append(names, inst.DisplayName())
		}
		return nil, fmt.Errorf("ambiguous match %q: multiple instances (%s). Use name:port to disambiguate.", target, strings.Join(names, ", "))
	}

	var matches []InstanceInfo
	for _, inst := range instances {
		name := strings.ToLower(filepath.Base(inst.Workspace))
		if strings.Contains(name, target) {
			matches = append(matches, inst)
		}
	}

	if len(matches) == 0 {
		var names []string
		for _, inst := range instances {
			names = append(names, inst.DisplayName())
		}
		return nil, fmt.Errorf("no instance matching %q found. Available: %s", target, strings.Join(names, ", "))
	}
	if len(matches) > 1 {
		var names []string
		for _, inst := range matches {
			names = append(names, inst.DisplayName())
		}
		return nil, fmt.Errorf("ambiguous match %q: multiple instances (%s). Use name:port to disambiguate.", target, strings.Join(names, ", "))
	}
	return &matches[0], nil
}

func (t *RemoteTool) discover() ([]InstanceInfo, error) {
	t.mu.RLock()
	if t.cache != nil && time.Since(t.cacheAt) < 10*time.Second {
		cache := t.cache
		t.mu.RUnlock()
		return append([]InstanceInfo(nil), cache...), nil
	}
	t.mu.RUnlock()

	instances, err := t.registry.Discover()
	if err != nil {
		return nil, err
	}

	t.mu.Lock()
	t.cache = append([]InstanceInfo(nil), instances...)
	t.cacheAt = time.Now()
	t.mu.Unlock()

	return append([]InstanceInfo(nil), instances...), nil
}

type RemoteAgentMeta struct {
	Team        string
	Role        string
	Languages   []string
	ProjectName string
}

func FormatRemoteAgents(instances []InstanceInfo, meta map[string]RemoteAgentMeta) string {
	if len(instances) == 0 {
		return ""
	}
	// Deterministic rendering: the async discovery cache preserves network
	// discovery order, which varies between refreshes. Sort a copy by
	// display name then ID so identical instance sets always render
	// byte-identical text (prompt-cache stability).
	ordered := append([]InstanceInfo(nil), instances...)
	sort.Slice(ordered, func(i, j int) bool {
		ni, nj := filepath.Base(ordered[i].Workspace), filepath.Base(ordered[j].Workspace)
		if ni != nj {
			return ni < nj
		}
		return ordered[i].ID < ordered[j].ID
	})
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("The following %d ggcode instance(s) are online in other workspaces:\n", len(ordered)))
	for _, inst := range ordered {
		name := filepath.Base(inst.Workspace)
		line := fmt.Sprintf("- %s (%s) - %s", name, inst.Workspace, inst.Status)
		if m, ok := meta[inst.ID]; ok {
			var extras []string
			if m.Team != "" && m.Team != "dev-team" {
				extras = append(extras, "team="+m.Team)
			}
			if m.Role != "" && m.Role != "developer" {
				extras = append(extras, "role="+m.Role)
			}
			if len(m.Languages) > 0 {
				extras = append(extras, "langs="+strings.Join(m.Languages, "+"))
			}
			if len(extras) > 0 {
				line += " [" + strings.Join(extras, ", ") + "]"
			}
		}
		sb.WriteString(line + "\n")
	}
	sb.WriteString("\nUse lanchat to communicate with these instances (action='list' for details, action='send' to DM). Use a2a_remote for headless code-editing delegation.\n")
	return sb.String()
}

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
