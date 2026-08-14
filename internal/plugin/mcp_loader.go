package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/mcp"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/tool"
)

type MCPStatus string

const (
	MCPStatusPending   MCPStatus = "pending"
	MCPStatusConnected MCPStatus = "connected"
	MCPStatusFailed    MCPStatus = "failed"
)

type MCPServerInfo struct {
	Name          string
	Transport     string
	Source        string
	ToolNames     []string
	PromptNames   []string
	ResourceNames []string
	Status        MCPStatus
	Error         string
	Migrated      bool
	Disabled      bool
	OAuthRequired bool
}

// MCPOAuthRequiredError signals that OAuth is needed for an MCP server.
type MCPOAuthRequiredError struct {
	ServerName string
	Handler    *mcp.OAuthHandler
}

func (e *MCPOAuthRequiredError) Error() string {
	return fmt.Sprintf("mcp server %q requires OAuth authentication", e.ServerName)
}

// MCPPlugin connects to an MCP server and registers its tools.
type MCPPlugin struct {
	cfg           config.MCPServerConfig
	client        *mcp.Client
	adapter       *mcp.Adapter
	mu            sync.RWMutex
	connected     bool
	awaitingOAuth bool
	status        MCPStatus
	lastError     string
	prompts       []string
	resources     []string

	// forceReauthPending signals Connect() to call ForceReauth on the new
	// OAuthHandler so it skips the canonical (shared) credential fallback.
	forceReauthPending bool

	// autoReconnect controls whether the plugin watches for unexpected
	// process exits and attempts to reconnect automatically.
	autoReconnect bool

	// reconnectCancel stops the auto-reconnect watcher goroutine.
	reconnectCancel context.CancelFunc

	// registry holds a reference to the tool registry for re-registering
	// tools after auto-reconnect.
	registry *tool.Registry

	// samplingHandler, if set, is propagated to each new MCP client so
	// the server can request LLM completions via sampling/createMessage.
	samplingHandler mcp.SamplingHandler

	// elicitationHandler, if set, is propagated to each new MCP client so
	// the server can request structured user input via elicitation/create.
	elicitationHandler mcp.ElicitationHandler
}

// NewMCPPlugin creates a plugin from an MCP server configuration.
func NewMCPPlugin(cfg config.MCPServerConfig) *MCPPlugin {
	return &MCPPlugin{
		cfg:    cfg,
		status: MCPStatusPending,
	}
}

func (m *MCPPlugin) Name() string { return m.cfg.Name }

// SetSamplingHandler sets the LLM sampling handler on this plugin.
// The handler is propagated to each new MCP client when it connects.
// Must be called before Connect.
func (m *MCPPlugin) SetSamplingHandler(h mcp.SamplingHandler) {
	m.samplingHandler = h
}

// SetElicitationHandler sets the user elicitation handler on this plugin.
// The handler is propagated to each new MCP client when it connects.
// Must be called before Connect.
func (m *MCPPlugin) SetElicitationHandler(h mcp.ElicitationHandler) {
	m.elicitationHandler = h
}

// Connect initializes the MCP server, discovers tools, and returns an adapter.
func (m *MCPPlugin) Connect(ctx context.Context) (*mcp.Adapter, error) {
	m.mu.RLock()
	if m.adapter != nil {
		adapter := m.adapter
		m.mu.RUnlock()
		return adapter, nil
	}
	m.mu.RUnlock()

	client := mcp.NewClientFromConfig(m.cfg)
	if m.samplingHandler != nil {
		client.SetSamplingHandler(m.samplingHandler)
	}
	if m.elicitationHandler != nil {
		client.SetElicitationHandler(m.elicitationHandler)
	}
	m.mu.Lock()
	forceReauth := m.forceReauthPending
	m.forceReauthPending = false // one-shot: clear immediately
	m.mu.Unlock()
	if forceReauth && client != nil {
		// New client was just created — tell its OAuthHandler to skip
		// the canonical credential so a fresh OAuth flow triggers.
		_ = client.ForceReauth()
	}
	if err := client.Start(ctx); err != nil {
		m.mu.Lock()
		m.status = MCPStatusFailed
		m.lastError = normalizeMCPError(err)
		m.mu.Unlock()
		return nil, err
	}
	tools, prompts, resources, err := discoverCapabilities(ctx, client)
	if err != nil {
		var oauthErr *mcp.OAuthRequiredError
		if errors.As(err, &oauthErr) {
			_ = client.Close()
			return nil, &MCPOAuthRequiredError{
				ServerName: m.cfg.Name,
				Handler:    oauthErr.Handler,
			}
		}
		client.Close()
		m.mu.Lock()
		m.status = MCPStatusFailed
		m.lastError = normalizeMCPError(err)
		m.mu.Unlock()
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.adapter != nil {
		_ = client.Close()
		return m.adapter, nil
	}
	m.client = client
	if m.cfg.ReadOnly {
		m.adapter = mcp.NewReadOnlyAdapter(m.cfg.Name, client, tools)
	} else {
		m.adapter = mcp.NewAdapter(m.cfg.Name, client, tools)
	}
	m.connected = true
	m.awaitingOAuth = false
	m.status = MCPStatusConnected
	m.lastError = ""
	m.prompts = prompts
	m.resources = resources
	m.setupNotificationHandler(client)
	m.startReconnectWatcher(client)
	return m.adapter, nil
}

// setupNotificationHandler registers a notification handler on the MCP client
// to process server-initiated notifications:
//   - notifications/tools/list_changed: triggers hot tool list refresh
//   - notifications/resources/list_changed: logs for awareness
//   - notifications/message (logging): forwards to debug log
//
// This enables dynamic MCP servers that add/remove tools at runtime.
func (m *MCPPlugin) setupNotificationHandler(client *mcp.Client) {
	client.SetNotificationHandler(func(method string, params json.RawMessage) {
		switch method {
		case "notifications/tools/list_changed":
			debug.Log("mcp-notif", "server=%s tools/list_changed received, refreshing tools", m.cfg.Name)
			m.refreshTools(client)
		case "notifications/resources/list_changed":
			debug.Log("mcp-notif", "server=%s resources/list_changed received, refreshing resources", m.cfg.Name)
			m.refreshResources(client)
		case "notifications/prompts/list_changed":
			debug.Log("mcp-notif", "server=%s prompts/list_changed received", m.cfg.Name)
		case "notifications/message":
			// Server logging notification — forward to debug log for visibility.
			var msg struct {
				Level string `json:"level"`
				Data  any    `json:"data"`
			}
			if json.Unmarshal(params, &msg) == nil {
				debug.Log("mcp-log", "server=%s level=%s data=%v", m.cfg.Name, msg.Level, msg.Data)
			} else {
				debug.Log("mcp-log", "server=%s raw=%s", m.cfg.Name, string(params))
			}
		case "notifications/progress":
			debug.Log("mcp-notif", "server=%s progress: %s", m.cfg.Name, string(params))
		default:
			debug.Log("mcp-notif", "server=%s unhandled notification: %s", m.cfg.Name, method)
		}
	})
}

// refreshTools re-fetches the tool list from the MCP server and updates the
// adapter and registry. Called when the server signals tools/list_changed.
func (m *MCPPlugin) refreshTools(client *mcp.Client) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tools, err := client.ListTools(ctx)
	if err != nil {
		debug.Log("mcp-notif", "server=%s tool refresh failed: %v", m.cfg.Name, err)
		return
	}

	m.mu.Lock()
	oldAdapter := m.adapter
	registry := m.registry
	readOnly := m.cfg.ReadOnly
	m.mu.Unlock()

	// Unregister old tools
	if oldAdapter != nil && registry != nil {
		for _, tn := range oldAdapter.ToolNames() {
			registry.Unregister(tn)
		}
	}

	// Create new adapter with updated tools
	var newAdapter *mcp.Adapter
	if readOnly {
		newAdapter = mcp.NewReadOnlyAdapter(m.cfg.Name, client, tools)
	} else {
		newAdapter = mcp.NewAdapter(m.cfg.Name, client, tools)
	}

	m.mu.Lock()
	m.adapter = newAdapter
	m.mu.Unlock()

	// Re-register tools if we have a registry
	if registry != nil {
		if regErr := newAdapter.RegisterTools(registry); regErr != nil {
			debug.Log("mcp-notif", "server=%s tool re-registration failed: %v", m.cfg.Name, regErr)
		}
	}

	debug.Log("mcp-notif", "server=%s tools refreshed: %d tools", m.cfg.Name, len(tools))
}

// refreshResources re-fetches the resource list from the MCP server.
func (m *MCPPlugin) refreshResources(client *mcp.Client) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resources := listResourceNames(client.ListResources(ctx))

	m.mu.Lock()
	m.resources = resources
	m.mu.Unlock()

	debug.Log("mcp-notif", "server=%s resources refreshed: %d items", m.cfg.Name, len(resources))
}

// startReconnectWatcher launches a goroutine that monitors the stdio MCP
// server process for unexpected exits and attempts automatic reconnection
// with exponential backoff. Only active for stdio transports when
// autoReconnect is true (default for stdio servers).
func (m *MCPPlugin) startReconnectWatcher(client *mcp.Client) {
	// Cancel any previous watcher
	if m.reconnectCancel != nil {
		m.reconnectCancel()
	}
	exitCh := client.ProcessExit()
	if exitCh == nil {
		return // non-stdio transport, no process to watch
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.reconnectCancel = cancel
	m.autoReconnect = true

	safego.Go("plugin.mcp.reconnectWatch", func() {
		select {
		case <-ctx.Done():
			return
		case <-exitCh:
			// Process exited unexpectedly; attempt reconnect.
		}
		debug.Log("mcp-reconnect", "server=%s detected crash, starting auto-reconnect", m.cfg.Name)
		backoff := []time.Duration{2 * time.Second, 5 * time.Second, 10 * time.Second, 20 * time.Second}
		for attempt, delay := range backoff {
			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
			}
			debug.Log("mcp-reconnect", "server=%s reconnect attempt %d", m.cfg.Name, attempt+1)
			// Mark as disconnected
			m.mu.Lock()
			oldAdapter := m.adapter
			m.adapter = nil
			m.connected = false
			m.client = nil
			m.status = MCPStatusPending
			m.mu.Unlock()
			// Unregister old tools so stale definitions don't linger
			if oldAdapter != nil && m.registry != nil {
				for _, tn := range oldAdapter.ToolNames() {
					m.registry.Unregister(tn)
				}
			}
			// Attempt reconnection
			adapter, err := m.Connect(ctx)
			if err == nil && adapter != nil {
				debug.Log("mcp-reconnect", "server=%s reconnected successfully", m.cfg.Name)
				// Re-register tools if we have a registry reference
				if m.registry != nil {
					if regErr := adapter.RegisterTools(m.registry); regErr != nil {
						debug.Log("mcp-reconnect", "server=%s tool re-registration failed: %v", m.cfg.Name, regErr)
					}
				}
				return
			}
			if err != nil {
				m.mu.Lock()
				m.status = MCPStatusFailed
				m.lastError = normalizeMCPError(err)
				m.mu.Unlock()
				debug.Log("mcp-reconnect", "server=%s attempt %d failed: %v", m.cfg.Name, attempt+1, err)
			}
		}
		// All retries exhausted
		debug.Log("mcp-reconnect", "server=%s all reconnect attempts exhausted", m.cfg.Name)
		m.mu.Lock()
		m.status = MCPStatusFailed
		m.lastError = "server crashed and auto-reconnect failed after all retries"
		m.mu.Unlock()
	})
}

func discoverCapabilities(ctx context.Context, client *mcp.Client) ([]mcp.ToolDefinition, []string, []string, error) {
	type result struct {
		tools     []mcp.ToolDefinition
		prompts   []string
		resources []string
		err       error
	}
	done := make(chan result, 1)
	safego.Go("plugin.mcp.discover", func() {
		initResult, err := client.Initialize(ctx)
		if err != nil {
			debug.Log("mcp-discover", "initialize_failed error=%v", err)
			done <- result{err: err}
			return
		}
		debug.Log("mcp-discover", "initialize_ok server=%s protocol=%s", initResult.ServerInfo.Name, initResult.ProtocolVersion)
		tools, err := client.ListTools(ctx)
		if err != nil {
			debug.Log("mcp-discover", "list_tools_failed error=%v", err)
			done <- result{err: err}
			return
		}
		debug.Log("mcp-discover", "list_tools_ok count=%d", len(tools))
		done <- result{
			tools:     tools,
			prompts:   listPromptNames(client.ListPrompts(ctx)),
			resources: listResourceNames(client.ListResources(ctx)),
		}
	})
	select {
	case <-ctx.Done():
		client.Abort()
		return nil, nil, nil, ctx.Err()
	case res := <-done:
		return res.tools, res.prompts, res.resources, res.err
	}
}

// RegisterTools discovers MCP tools and registers them into the registry.
func (m *MCPPlugin) RegisterTools(ctx context.Context, registry *tool.Registry) error {
	adapter, err := m.Connect(ctx)
	if err != nil {
		return err
	}
	return adapter.RegisterTools(registry)
}

// Adapter returns the MCP adapter (nil if not connected).
func (m *MCPPlugin) Adapter() *mcp.Adapter {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.adapter
}

// IsConnected returns whether the MCP server has been successfully contacted.
func (m *MCPPlugin) IsConnected() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.connected
}

func (m *MCPPlugin) Status() MCPStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.status
}

func (m *MCPPlugin) LastError() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.lastError
}

func (m *MCPPlugin) Info() MCPServerInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	info := MCPServerInfo{
		Name:          m.cfg.Name,
		Transport:     firstNonEmpty(strings.ToLower(strings.TrimSpace(m.cfg.Type)), "stdio"),
		Source:        firstNonEmpty(m.cfg.Source, "ggcode"),
		Status:        m.status,
		Error:         m.lastError,
		Migrated:      m.cfg.Migrated,
		PromptNames:   append([]string(nil), m.prompts...),
		ResourceNames: append([]string(nil), m.resources...),
	}
	if m.adapter != nil {
		info.ToolNames = m.adapter.ToolNames()
	}
	return info
}

func (m *MCPPlugin) Close() error {
	// Stop the auto-reconnect watcher first
	if m.reconnectCancel != nil {
		m.reconnectCancel()
		m.reconnectCancel = nil
	}
	m.mu.Lock()
	if m.client == nil {
		m.adapter = nil
		m.connected = false
		m.status = MCPStatusPending
		m.mu.Unlock()
		return nil
	}
	client := m.client
	m.client = nil
	m.adapter = nil
	m.connected = false
	m.status = MCPStatusPending
	m.mu.Unlock()
	return client.Close()
}

func (m *MCPPlugin) GetPrompt(ctx context.Context, name string, args map[string]interface{}) (*mcp.GetPromptResult, error) {
	m.mu.RLock()
	client := m.client
	m.mu.RUnlock()
	if client == nil {
		return nil, fmt.Errorf("mcp server %q is not connected", m.cfg.Name)
	}
	return client.GetPrompt(ctx, name, args)
}

func (m *MCPPlugin) ReadResource(ctx context.Context, uri string) (*mcp.ReadResourceResult, error) {
	m.mu.RLock()
	client := m.client
	m.mu.RUnlock()
	if client == nil {
		return nil, fmt.Errorf("mcp server %q is not connected", m.cfg.Name)
	}
	return client.ReadResource(ctx, uri)
}

func (m *MCPPlugin) markPending() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.adapter == nil {
		m.status = MCPStatusPending
		m.lastError = ""
	}
}

// Tools returns the registered tool names (requires prior Connect).
func (m *MCPPlugin) Tools() []tool.Tool {
	return nil
}

func (m *MCPPlugin) Init(cfg map[string]interface{}) error {
	return nil
}

type MCPManager struct {
	plugins            []*MCPPlugin
	registry           *tool.Registry
	onUpdate           func([]MCPServerInfo)
	mu                 sync.RWMutex
	warnings           []string
	startOnce          sync.Once
	timeout            time.Duration
	stdioTimeout       time.Duration
	pendingOAuth       map[string]*MCPOAuthRequiredError
	urlOpener          func(string) error
	samplingHandler    mcp.SamplingHandler
	elicitationHandler mcp.ElicitationHandler
}

func NewMCPManager(servers []config.MCPServerConfig, registry *tool.Registry) *MCPManager {
	plugins := make([]*MCPPlugin, 0, len(servers))
	for _, server := range servers {
		p := NewMCPPlugin(server)
		p.registry = registry
		plugins = append(plugins, p)
	}
	return &MCPManager{
		plugins:      plugins,
		registry:     registry,
		timeout:      8 * time.Second,
		stdioTimeout: 2 * time.Minute,
	}
}

func (m *MCPManager) SetOnUpdate(fn func([]MCPServerInfo)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onUpdate = fn
}

func (m *MCPManager) SetURLOpener(fn func(string) error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.urlOpener = fn
}

// SetSamplingHandler propagates the LLM sampling handler to all MCP plugins.
// Each plugin will pass the handler to its MCP client on connect, enabling
// servers to request LLM completions via sampling/createMessage.
func (m *MCPManager) SetSamplingHandler(h mcp.SamplingHandler) {
	m.mu.Lock()
	m.samplingHandler = h
	plugins := append([]*MCPPlugin(nil), m.plugins...)
	m.mu.Unlock()
	for _, plugin := range plugins {
		plugin.SetSamplingHandler(h)
	}
}

// SetElicitationHandler propagates the user elicitation handler to all MCP
// plugins. Each plugin will pass the handler to its MCP client on connect,
// enabling servers to request structured user input via elicitation/create.
func (m *MCPManager) SetElicitationHandler(h mcp.ElicitationHandler) {
	m.mu.Lock()
	m.elicitationHandler = h
	plugins := append([]*MCPPlugin(nil), m.plugins...)
	m.mu.Unlock()
	for _, plugin := range plugins {
		plugin.SetElicitationHandler(h)
	}
}

// PendingOAuthByName returns the pending OAuth error for a specific server,
// or nil if that server is not waiting for OAuth login. With multiple OAuth
// MCP servers pending simultaneously, each server keeps its own entry.
func (m *MCPManager) PendingOAuthByName(name string) *MCPOAuthRequiredError {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.pendingOAuth[name]
}

// PendingOAuth returns one pending OAuth error (alphabetically first server
// name) for legacy single-value consumers, or nil when none are pending.
func (m *MCPManager) PendingOAuth() *MCPOAuthRequiredError {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var first string
	for name := range m.pendingOAuth {
		if first == "" || name < first {
			first = name
		}
	}
	if first == "" {
		return nil
	}
	return m.pendingOAuth[first]
}

// ClearPendingOAuth clears the pending OAuth entry for the named server.
// An empty name clears nothing.
func (m *MCPManager) ClearPendingOAuth(name string) {
	if name == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.pendingOAuth, name)
}

func (m *MCPManager) Snapshot() []MCPServerInfo {
	m.mu.RLock()
	plugins := append([]*MCPPlugin(nil), m.plugins...)
	pendingOAuthNames := make(map[string]bool, len(m.pendingOAuth))
	for name := range m.pendingOAuth {
		pendingOAuthNames[name] = true
	}
	m.mu.RUnlock()
	out := make([]MCPServerInfo, 0, len(plugins))
	for _, plugin := range plugins {
		info := plugin.Info()
		info.Disabled = MCPDisabled(plugin.Name())
		info.OAuthRequired = pendingOAuthNames[info.Name]
		out = append(out, info)
	}
	return out
}

func (m *MCPManager) SnapshotMCP() []tool.MCPServerSnapshot {
	infos := m.Snapshot()
	out := make([]tool.MCPServerSnapshot, 0, len(infos))
	for _, info := range infos {
		out = append(out, tool.MCPServerSnapshot{
			Name:          info.Name,
			Connected:     info.Status == MCPStatusConnected,
			Pending:       info.Status == MCPStatusPending,
			Error:         info.Error,
			ToolNames:     append([]string(nil), info.ToolNames...),
			PromptNames:   append([]string(nil), info.PromptNames...),
			ResourceNames: append([]string(nil), info.ResourceNames...),
		})
	}
	return out
}

func (m *MCPManager) emitUpdate() {
	m.mu.RLock()
	fn := m.onUpdate
	m.mu.RUnlock()
	if fn != nil {
		fn(m.Snapshot())
	}
}

func (m *MCPManager) connectOne(ctx context.Context, p *MCPPlugin) {
	connectCtx, cancel := context.WithTimeout(ctx, m.connectTimeoutFor(p))
	defer cancel()
	p.markPending()
	p.registry = m.registry
	m.emitUpdate()
	debug.Log("mcp-connect", "start server=%s timeout=%v", p.Name(), m.connectTimeoutFor(p))
	if err := p.RegisterTools(connectCtx, m.registry); err != nil {
		var oauthErr *MCPOAuthRequiredError
		if errors.As(err, &oauthErr) {
			debug.Log("mcp-connect", "oauth_required server=%s", p.Name())
			p.mu.Lock()
			p.awaitingOAuth = true
			p.mu.Unlock()
			m.mu.Lock()
			if m.pendingOAuth == nil {
				m.pendingOAuth = make(map[string]*MCPOAuthRequiredError)
			}
			m.pendingOAuth[p.Name()] = oauthErr
			m.mu.Unlock()
			m.emitUpdate()
			return
		}
		debug.Log("mcp-connect", "failed server=%s error=%v", p.Name(), err)
		m.mu.Lock()
		m.warnings = append(m.warnings, fmt.Sprintf("warning: MCP server %s failed: %v", p.Name(), err))
		m.mu.Unlock()
	} else {
		debug.Log("mcp-connect", "connected server=%s tools=%d", p.Name(), len(p.Info().ToolNames))
		// Stale pending-OAuth entry (e.g. server connected after auth completed
		// by another path) must not linger.
		m.mu.Lock()
		delete(m.pendingOAuth, p.Name())
		m.mu.Unlock()
	}
	m.emitUpdate()
}

func (m *MCPManager) connectTimeoutFor(p *MCPPlugin) time.Duration {
	if strings.EqualFold(strings.TrimSpace(p.cfg.Type), "stdio") {
		if m.stdioTimeout > 0 {
			return m.stdioTimeout
		}
	}
	if m.timeout > 0 {
		return m.timeout
	}
	return 8 * time.Second
}

func (m *MCPManager) StartBackground(ctx context.Context) {
	m.startOnce.Do(func() {
		m.emitUpdate()
		for _, plugin := range m.plugins {
			plugin := plugin
			if MCPDisabled(plugin.Name()) {
				continue
			}
			pluginCopy := plugin
			safego.Go("plugin.mcp.connectWithRetry", func() { m.connectWithRetry(ctx, pluginCopy) })
		}
	})
}

func (m *MCPManager) ConnectAll(ctx context.Context) []string {
	m.emitUpdate()
	var wg sync.WaitGroup
	for _, plugin := range m.plugins {
		if MCPDisabled(plugin.Name()) {
			continue
		}
		wg.Add(1)
		pl := plugin
		safego.Go("plugin.mcp.connect", func() {
			defer wg.Done()
			m.connectOne(ctx, pl)
		})
	}
	wg.Wait()
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]string(nil), m.warnings...)
}

func (m *MCPManager) Retry(name string) bool {
	m.mu.RLock()
	plugins := append([]*MCPPlugin(nil), m.plugins...)
	m.mu.RUnlock()
	for _, plugin := range plugins {
		if plugin.Name() != name {
			continue
		}
		pluginCopy := plugin
		safego.Go("plugin.mcp.connectOne", func() { m.connectOne(context.Background(), pluginCopy) })
		return true
	}
	return false
}

func (m *MCPManager) Install(ctx context.Context, server config.MCPServerConfig) error {
	plugin := NewMCPPlugin(server)

	var previous *MCPPlugin
	m.mu.Lock()
	for i, existing := range m.plugins {
		if existing.Name() != server.Name {
			continue
		}
		previous = existing
		m.plugins[i] = plugin
		m.mu.Unlock()
		if previous != nil {
			for _, toolName := range previous.Info().ToolNames {
				m.registry.Unregister(toolName)
			}
			_ = previous.Close()
		}
		m.connectOne(ctx, plugin)
		return nil
	}
	m.plugins = append(m.plugins, plugin)
	m.mu.Unlock()

	m.connectOne(ctx, plugin)
	return nil
}

func (m *MCPManager) Uninstall(name string) bool {
	m.mu.Lock()
	for i, plugin := range m.plugins {
		if plugin.Name() != name {
			continue
		}
		m.plugins = append(m.plugins[:i], m.plugins[i+1:]...)
		m.mu.Unlock()

		for _, toolName := range plugin.Info().ToolNames {
			m.registry.Unregister(toolName)
		}
		_ = plugin.Close()
		m.emitUpdate()
		return true
	}
	m.mu.Unlock()
	return false
}

// Disconnect closes the MCP server connection and unregisters its tools,
// but keeps the plugin in the list so it can be reconnected later.
// Runs asynchronously to avoid blocking the caller.
func (m *MCPManager) Disconnect(name string) bool {
	m.mu.RLock()
	plugins := append([]*MCPPlugin(nil), m.plugins...)
	m.mu.RUnlock()
	for _, plugin := range plugins {
		if plugin.Name() != name {
			continue
		}
		p := plugin
		safego.Go("plugin.mcp.unregister", func() {
			toolNames := p.Info().ToolNames
			for _, toolName := range toolNames {
				m.registry.Unregister(toolName)
			}
			_ = p.Close()
			m.emitUpdate()
		})
		return true
	}
	return false
}

// Reconnect reconnects a previously disconnected MCP server.
func (m *MCPManager) Reconnect(name string) bool {
	m.mu.RLock()
	plugins := append([]*MCPPlugin(nil), m.plugins...)
	m.mu.RUnlock()
	for _, plugin := range plugins {
		if plugin.Name() != name {
			continue
		}
		pluginCopy := plugin
		safego.Go("plugin.mcp.reconnect", func() { m.connectOne(context.Background(), pluginCopy) })
		return true
	}
	return false
}

// ForceReauth disconnects the server and marks it to skip the canonical
// (shared) credential on next connect. The new client's OAuthHandler will
// find no valid token and trigger a fresh OAuth flow.
func (m *MCPManager) ForceReauth(name string) bool {
	m.mu.RLock()
	plugins := append([]*MCPPlugin(nil), m.plugins...)
	m.mu.RUnlock()
	for _, p := range plugins {
		if p.Name() != name {
			continue
		}

		// 1. Unregister old tools so stale descriptions (e.g. embedded
		//    accountId) don't survive the reconnect.
		// Avoid nested RLock: Info() also acquires p.mu.RLock(), which
		// deadlocks when a writer is waiting (Go RWMutex is non-reentrant).
		p.mu.RLock()
		var oldToolNames []string
		if p.adapter != nil {
			oldToolNames = p.adapter.ToolNames()
		}
		p.mu.RUnlock()
		for _, toolName := range oldToolNames {
			m.registry.Unregister(toolName)
		}

		// 2. Disconnect: close old client, clear adapter so Connect() creates new ones.
		p.mu.Lock()
		if p.client != nil {
			_ = p.client.Close()
			p.client = nil
		}
		p.adapter = nil
		p.connected = false
		p.awaitingOAuth = true
		p.forceReauthPending = true
		p.mu.Unlock()

		// 3. Reconnect — Connect() will create a new client/handler, call
		//    ForceReauth() on it (skipCanonical=true), find no credential,
		//    and return OAuthRequiredError to trigger the auth flow.
		pCopy := p
		safego.Go("plugin.mcp.reauth", func() { m.connectOne(context.Background(), pCopy) })
		return true
	}
	return false
}

func (m *MCPManager) GetPrompt(ctx context.Context, server, name string, args map[string]interface{}) (*tool.MCPPromptResult, error) {
	plugin := m.pluginByName(server)
	if plugin == nil {
		return nil, fmt.Errorf("MCP server %q not found", server)
	}
	result, err := plugin.GetPrompt(ctx, name, args)
	if err != nil {
		return nil, err
	}
	out := &tool.MCPPromptResult{Description: result.Description}
	for _, msg := range result.Messages {
		out.Messages = append(out.Messages, tool.MCPPromptMessage{
			Role: msg.Role,
			Text: extractPromptText(msg.Content),
			Raw:  compactJSON(msg.Content),
		})
	}
	return out, nil
}

func (m *MCPManager) ReadResource(ctx context.Context, server, uri string) (*tool.MCPResourceResult, error) {
	plugin := m.pluginByName(server)
	if plugin == nil {
		return nil, fmt.Errorf("MCP server %q not found", server)
	}
	result, err := plugin.ReadResource(ctx, uri)
	if err != nil {
		return nil, err
	}
	out := &tool.MCPResourceResult{Contents: make([]tool.MCPResourceContent, 0, len(result.Contents))}
	for _, content := range result.Contents {
		out.Contents = append(out.Contents, tool.MCPResourceContent{
			URI:      content.URI,
			MIMEType: content.MIMEType,
			Text:     content.Text,
			Blob:     content.Blob,
		})
	}
	return out, nil
}

func (m *MCPManager) connectWithRetry(ctx context.Context, plugin *MCPPlugin) {
	backoff := []time.Duration{0, time.Second, 3 * time.Second}
	for attempt, delay := range backoff {
		if attempt > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
		m.connectOne(ctx, plugin)
		if plugin.IsConnected() {
			return
		}
		// Stop retrying if OAuth is needed — the TUI will handle reconnection
		// after the user completes the browser auth flow.
		plugin.mu.RLock()
		waiting := plugin.awaitingOAuth
		plugin.mu.RUnlock()
		if waiting {
			return
		}
	}
}

func (m *MCPManager) pluginByName(name string) *MCPPlugin {
	m.mu.RLock()
	plugins := append([]*MCPPlugin(nil), m.plugins...)
	m.mu.RUnlock()
	for _, plugin := range plugins {
		if plugin.Name() == name {
			return plugin
		}
	}
	return nil
}

// Reload hot-swaps MCP server configurations without restarting the session.
// It diffs the new server list against the current one:
//   - Removed servers: disconnect + unregister tools + remove from list
//   - Changed servers: disconnect + unregister + replace plugin + reconnect
//   - New servers: add plugin + connect
//   - Unchanged servers: left alone
//
// This enables hot-reloading mcp_servers.yaml at runtime.
func (m *MCPManager) Reload(ctx context.Context, servers []config.MCPServerConfig) {
	m.mu.Lock()

	newByName := make(map[string]config.MCPServerConfig, len(servers))
	for _, s := range servers {
		newByName[s.Name] = s
	}

	// Categorize current plugins into kept/removed/changed.
	var removed, changed []*MCPPlugin
	finalPlugins := make([]*MCPPlugin, 0, len(servers))
	oldNames := make(map[string]bool, len(m.plugins))
	for _, p := range m.plugins {
		oldNames[p.Name()] = true
		newCfg, exists := newByName[p.Name()]
		if !exists {
			removed = append(removed, p)
			delete(m.pendingOAuth, p.Name()) // #314: no OAuth flow for removed servers
			continue
		}
		if !mcpServerConfigEqual(p.cfg, newCfg) {
			changed = append(changed, p)
			delete(m.pendingOAuth, p.Name()) // config replaced: stale handler
			continue
		}
		finalPlugins = append(finalPlugins, p)
	}

	// Create fresh plugins for changed + new servers.
	changedNames := make(map[string]bool, len(changed))
	for _, p := range changed {
		changedNames[p.Name()] = true
	}
	var connectPlugins []*MCPPlugin
	for _, s := range servers {
		isChanged := changedNames[s.Name]
		isNew := !oldNames[s.Name] && !isChanged
		if isChanged || isNew {
			p := m.newPluginFromConfig(s)
			finalPlugins = append(finalPlugins, p)
			connectPlugins = append(connectPlugins, p)
		}
	}

	m.plugins = finalPlugins
	m.mu.Unlock()

	// Unregister tools + close removed/changed plugins (outside lock).
	closeAndUnregister := func(p *MCPPlugin, reason string) {
		for _, toolName := range p.Info().ToolNames {
			m.registry.Unregister(toolName)
		}
		_ = p.Close()
		debug.Log("mcp-reload", "%s server=%s", reason, p.Name())
	}
	for _, p := range removed {
		closeAndUnregister(p, "removed")
	}
	for _, p := range changed {
		closeAndUnregister(p, "replaced")
	}

	m.emitUpdate()

	// Connect new + changed plugins in background.
	for _, p := range connectPlugins {
		if MCPDisabled(p.Name()) {
			continue
		}
		pluginCopy := p
		safego.Go("plugin.mcp.reloadConnect", func() {
			m.connectOne(ctx, pluginCopy)
		})
	}

	debug.Log("mcp-reload", "reload complete: removed=%d changed=%d total=%d", len(removed), len(changed), len(finalPlugins))
}

// newPluginFromConfig creates an MCPPlugin from a server config, propagating
// the manager's sampling and elicitation handlers.
func (m *MCPManager) newPluginFromConfig(s config.MCPServerConfig) *MCPPlugin {
	p := NewMCPPlugin(s)
	p.registry = m.registry
	if m.samplingHandler != nil {
		p.SetSamplingHandler(m.samplingHandler)
	}
	if m.elicitationHandler != nil {
		p.SetElicitationHandler(m.elicitationHandler)
	}
	return p
}

// mcpServerConfigEqual compares two MCP server configs to determine if
// anything that affects the connection has changed.
func mcpServerConfigEqual(a, b config.MCPServerConfig) bool {
	if a.Name != b.Name {
		return false
	}
	if a.Type != b.Type {
		return false
	}
	if a.Command != b.Command {
		return false
	}
	if a.URL != b.URL {
		return false
	}
	if a.ReadOnly != b.ReadOnly {
		return false
	}
	if len(a.Args) != len(b.Args) {
		return false
	}
	for i := range a.Args {
		if a.Args[i] != b.Args[i] {
			return false
		}
	}
	if len(a.Env) != len(b.Env) {
		return false
	}
	for k, v := range a.Env {
		if bv, ok := b.Env[k]; !ok || bv != v {
			return false
		}
	}
	if len(a.Headers) != len(b.Headers) {
		return false
	}
	for k, v := range a.Headers {
		if bv, ok := b.Headers[k]; !ok || bv != v {
			return false
		}
	}
	return true
}

func (m *MCPManager) Close() error {
	var firstErr error
	m.mu.RLock()
	plugins := append([]*MCPPlugin(nil), m.plugins...)
	m.mu.RUnlock()
	for _, plugin := range plugins {
		if err := plugin.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func normalizeMCPError(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, context.DeadlineExceeded):
		return decorateContextError("connection timed out", err, context.DeadlineExceeded.Error())
	case errors.Is(err, context.Canceled):
		return decorateContextError("connection canceled", err, context.Canceled.Error())
	default:
		return err.Error()
	}
}

func decorateContextError(prefix string, err error, base string) string {
	if err == nil {
		return prefix
	}
	message := strings.TrimSpace(err.Error())
	if message == "" || message == base {
		return prefix
	}
	if strings.HasPrefix(message, base) {
		message = strings.TrimSpace(strings.TrimPrefix(message, base))
		message = strings.TrimPrefix(message, ":")
		message = strings.TrimSpace(message)
	}
	if message == "" {
		return prefix
	}
	return prefix + ": " + message
}

func extractPromptText(raw json.RawMessage) string {
	var single struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &single); err == nil && single.Text != "" {
		return single.Text
	}
	var list []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &list); err == nil {
		parts := make([]string, 0, len(list))
		for _, item := range list {
			if strings.TrimSpace(item.Text) != "" {
				parts = append(parts, item.Text)
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

func compactJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var out bytes.Buffer
	if err := json.Compact(&out, raw); err != nil {
		return string(raw)
	}
	return out.String()
}

func listPromptNames(prompts []mcp.PromptDefinition, err error) []string {
	if isOptionalCapabilityUnavailable(err) || err != nil {
		return nil
	}
	names := make([]string, 0, len(prompts))
	for _, prompt := range prompts {
		if name := strings.TrimSpace(prompt.Name); name != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func listResourceNames(resources []mcp.ResourceDefinition, err error) []string {
	if isOptionalCapabilityUnavailable(err) || err != nil {
		return nil
	}
	names := make([]string, 0, len(resources))
	for _, resource := range resources {
		name := strings.TrimSpace(resource.Name)
		if name == "" {
			name = strings.TrimSpace(resource.URI)
		}
		if name != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func isOptionalCapabilityUnavailable(err error) bool {
	if err == nil {
		return false
	}
	var rpcErr *mcp.Error
	if errors.As(err, &rpcErr) && rpcErr.Code == -32601 {
		return true
	}
	if strings.Contains(err.Error(), "JSON-RPC error -32601") {
		return true
	}
	return false
}
