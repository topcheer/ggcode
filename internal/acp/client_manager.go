package acp

import (
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/permission"
)

// ClientManager manages the lifecycle of all ACP agent clients.
type ClientManager struct {
	clients      map[string]*Client // keyed by agent name
	discoveries  map[string]DiscoveredAgent
	mu           sync.RWMutex
	workingDir   string
	policy       permission.PermissionPolicy
	mcpServers   []MCPServer
	onPermission PermissionHandler
	onApproval   ApprovalHandler
}

// NewClientManager discovers ACP agents and stores their shared startup config.
// ACP delegates intentionally send an empty mcpServers array because MCP
// passthrough is disabled for stability.
func NewClientManager(workingDir string, policy permission.PermissionPolicy) *ClientManager {
	mgr := &ClientManager{
		clients:     make(map[string]*Client),
		discoveries: make(map[string]DiscoveredAgent),
		workingDir:  workingDir,
		policy:      policy,
		mcpServers:  []MCPServer{},
	}

	agents := Discover()
	for _, agent := range agents {
		mgr.discoveries[agent.Def.Name] = agent
		mgr.clients[agent.Def.Name] = NewClient(agent, workingDir, policy, mgr.mcpServers)
		debug.Log("acp-client", "registered agent %q (%s)", agent.Def.Name, agent.Path)
	}

	return mgr
}

func (m *ClientManager) SetWorkingDir(dir string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.workingDir = dir
	for _, client := range m.clients {
		client.SetWorkingDir(dir)
	}
}

func (m *ClientManager) SetPermissionHandler(h PermissionHandler) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onPermission = h
	for _, client := range m.clients {
		client.SetPermissionHandler(h)
	}
}

func (m *ClientManager) SetApprovalHandler(h ApprovalHandler) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onApproval = h
	for _, client := range m.clients {
		client.SetApprovalHandler(h)
	}
}

// Available returns the list of available agent names.
func (m *ClientManager) Available() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	names := make([]string, 0, len(m.discoveries))
	for name := range m.discoveries {
		names = append(names, name)
	}
	return names
}

// AgentInfo returns display information for an agent.
func (m *ClientManager) AgentInfo(name string) (title, description string, ok bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	d, ok := m.discoveries[name]
	if !ok {
		return "", "", false
	}
	return d.Def.Title, d.Def.Description, true
}

// CloseAll shuts down all running agent processes.
func (m *ClientManager) CloseAll() {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for name, client := range m.clients {
		if err := client.Close(); err != nil {
			debug.Log("acp-client", "error closing agent %q: %v", name, err)
		}
	}
}

func (m *ClientManager) newClient(name string) (*Client, error) {
	m.mu.RLock()
	discovery, ok := m.discoveries[name]
	workingDir := m.workingDir
	onPermission := m.onPermission
	onApproval := m.onApproval
	policy := m.policy
	mcpServers := cloneMCPServers(m.mcpServers)
	m.mu.RUnlock()
	if !ok {
		return nil, ErrAgentNotFound{name: name}
	}
	client := NewClient(discovery, workingDir, policy, mcpServers)
	client.SetPermissionHandler(onPermission)
	client.SetApprovalHandler(onApproval)
	return client, nil
}
