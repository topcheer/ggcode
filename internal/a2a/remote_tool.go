package a2a

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
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
	// #5616 seat addressing: lanchat→a2a identity bridge (nil in tests/CLI).
	identity IdentityLookup
}

func NewRemoteTool(reg *Registry, apiKey string) *RemoteTool {
	if apiKey == "" {
		apiKey = config.DefaultA2AAPIKey
	}
	return &RemoteTool{registry: reg, apiKey: apiKey}
}

// SetIdentityLookup wires the lanchat seat-identity bridge so targets can
// be addressed by seat nick and same-name candidates resolve healthy-first.
func (t *RemoteTool) SetIdentityLookup(fn IdentityLookup) {
	t.mu.Lock()
	t.identity = fn
	t.mu.Unlock()
}

func (t *RemoteTool) Name() string { return "a2a_remote" }

func (t *RemoteTool) Description() string {
	return "Delegate a code-editing task to a remote ggcode instance via A2A protocol. " +
		"Use this for headless code tasks (e.g. \"edit file X in project Y\", \"review code in user-service\") — NOT for asking questions or chatting. " +
		"For real-time questions, status checks, or collaboration with other agents/users on the LAN, use the 'lanchat' tool instead. " +
		"The target is identified by project name (e.g. 'order-service', 'user-service'), by seat nick within a shared-workspace fleet (e.g. 'dev_ggcode'), or by 'name:port' to pin an exact instance."
}

func (t *RemoteTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"target": {
				"type": "string",
				"description": "Target instance: project name (e.g. 'order-service', 'user-service', 'gateway'), seat nick in a shared-workspace fleet (e.g. 'dev_ggcode'), or 'name:port' to pin an exact instance. Use 'list' to see all available instances."
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

	inst, err := t.FindInstance(params.Target)
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
	// #5616: list must render seat identity too - the same enrichment the
	// resolution path gets, so agents can pick nick targets from the roster.
	instances = enrichInstances(instances, t.identity)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d ggcode instance(s):\n\n", len(instances)))
	for _, inst := range instances {
		sb.WriteString(fmt.Sprintf("• %s\n", inst.DisplayName()))
		if inst.Nick != "" {
			sb.WriteString(fmt.Sprintf("  Seat: %s (role=%s)\n", inst.Nick, inst.Role))
		}
		sb.WriteString(fmt.Sprintf("  Workspace: %s\n", inst.Workspace))
		sb.WriteString(fmt.Sprintf("  Endpoint: %s\n", inst.Endpoint))
		sb.WriteString(fmt.Sprintf("  Status: %s\n", inst.Status))
		sb.WriteString(fmt.Sprintf("  Started: %s\n\n", inst.StartedAt))
	}
	return tool.Result{Content: sb.String()}, nil
}

func (t *RemoteTool) FindInstance(target string) (*InstanceInfo, error) {
	instances, err := t.discover()
	if err != nil {
		return nil, fmt.Errorf("discover failed: %v", err)
	}
	if len(instances) == 0 {
		return nil, fmt.Errorf("no ggcode instances found")
	}

	// #5616: enrich with seat identity (lanchat nick/role) so nick targets
	// and healthy-first disambiguation work in same-workspace fleets.
	instances = enrichInstances(instances, t.identity)
	now := time.Now()

	target = strings.ToLower(strings.TrimSpace(target))

	// T1: seat nick (dev_ggcode, reviewer_1, or composite nick_role).
	if nickMatches := matchNick(instances, target); len(nickMatches) > 0 {
		return pickHealthy(target, nickMatches, t.identity, now)
	}

	// T2: pinned endpoint (host:port or workspace-name:port) - unambiguous
	// by construction. #5616: display-name port derivation fails on
	// scheme-prefixed endpoints (SplitHostPort rejects "http://host:port"),
	// so ports are derived from the normalized endpoint instead.
	for i := range instances {
		ep := normEndpoint(instances[i].Endpoint)
		base := strings.ToLower(filepath.Base(instances[i].Workspace))
		epPort := ""
		if _, p, err := net.SplitHostPort(ep); err == nil {
			epPort = p
		}
		if ep == target || (epPort != "" && (base+":"+strings.ToLower(epPort)) == target) {
			return &instances[i], nil
		}
	}

	// T3: name tier (display name or workspace-base). Same-workspace fleets
	// collide here: healthy-first picks the live instance, dead-only yields
	// the explicit offline verdict, multiple live yields ambiguity. Bare
	// names must never short-circuit to discovery-order first hit — that is
	// how dead seats used to answer live lookups.
	var nameMatches []InstanceInfo
	seenName := make(map[string]bool, len(instances))
	for _, inst := range instances {
		dn := strings.ToLower(inst.DisplayName())
		base := strings.ToLower(filepath.Base(inst.Workspace))
		if dn == target || base == target {
			if !seenName[inst.ID] {
				seenName[inst.ID] = true
				nameMatches = append(nameMatches, inst)
			}
		}
	}
	if len(nameMatches) > 0 {
		return pickHealthy(target, nameMatches, t.identity, now)
	}

	// T4: substring fallback (legacy behavior).
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
		return pickHealthy(target, matches, t.identity, now)
	}
	alive, _ := partitionAlive(matches, t.identity, now)
	if len(alive) == 0 {
		return nil, offlineErr(target, matches)
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

	// #5616: mDNS TXT carries no seat nick; the local workspace roster file
	// does (SetSelfIdentity republishes it). Overlay roster identity onto
	// mDNS entries by normalized endpoint so cross-process resolvers and the
	// list view see seat nicks even without a live lanchat bridge.
	if ws := t.registry.SelfWorkspace(); ws != "" {
		for _, r := range ReadRoster(ws, t.registry.SelfID()) {
			if r.Nick == "" {
				continue
			}
			ep := normEndpoint(r.Endpoint)
			for i := range instances {
				if instances[i].Nick == "" && normEndpoint(instances[i].Endpoint) == ep {
					instances[i].Nick = r.Nick
					instances[i].Role = r.Role
					instances[i].Team = r.Team
				}
			}
		}
	}

	t.mu.Lock()
	t.cache = append([]InstanceInfo(nil), instances...)
	t.cacheAt = time.Now()
	t.mu.Unlock()

	return append([]InstanceInfo(nil), instances...), nil
}

type RemoteAgentMeta struct {
	Nick        string
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
		if m, ok := meta[normEndpoint(inst.Endpoint)]; ok {
			var extras []string
			if m.Nick != "" {
				extras = append(extras, "seat="+m.Nick)
			}
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
