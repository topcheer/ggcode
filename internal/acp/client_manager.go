package acp

import (
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

// ClientManager manages the lifecycle of all ACP agent clients.
type ClientManager struct {
	clients      map[string]*Client // keyed by agent name
	discoveries  map[string]DiscoveredAgent
	mu           sync.RWMutex
	workingDir   string
	onPermission PermissionHandler
}

// NewClientManager discovers and prepares (but does not start) ACP clients.
// Agent processes are lazily started on first use via Get().
func NewClientManager(workingDir string, onPermission PermissionHandler) *ClientManager {
	mgr := &ClientManager{
		clients:      make(map[string]*Client),
		discoveries:  make(map[string]DiscoveredAgent),
		workingDir:   workingDir,
		onPermission: onPermission,
	}

	agents := Discover()
	for _, agent := range agents {
		mgr.discoveries[agent.Def.Name] = agent
		mgr.clients[agent.Def.Name] = NewClient(agent, workingDir)
		if onPermission != nil {
			mgr.clients[agent.Def.Name].SetPermissionHandler(onPermission)
		}
		debug.Log("acp-client", "registered agent %q (%s)", agent.Def.Name, agent.Path)
	}

	return mgr
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
