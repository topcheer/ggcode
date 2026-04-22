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
}

// NewMCPPlugin creates a plugin from an MCP server configuration.
func NewMCPPlugin(cfg config.MCPServerConfig) *MCPPlugin {
	return &MCPPlugin{
		cfg:    cfg,
		status: MCPStatusPending,
	}
}

func (m *MCPPlugin) Name() string { return m.cfg.Name }

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
	m.adapter = mcp.NewAdapter(m.cfg.Name, client, tools)
	m.connected = true
	m.awaitingOAuth = false
	m.status = MCPStatusConnected
	m.lastError = ""
	m.prompts = prompts
	m.resources = resources
	return m.adapter, nil
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
	plugins      []*MCPPlugin
	registry     *tool.Registry
	onUpdate     func([]MCPServerInfo)
	mu           sync.RWMutex
	warnings     []string
	startOnce    sync.Once
	timeout      time.Duration
	stdioTimeout time.Duration
	pendingOAuth *MCPOAuthRequiredError
	urlOpener    func(string) error
}

func NewMCPManager(servers []config.MCPServerConfig, registry *tool.Registry) *MCPManager {
	plugins := make([]*MCPPlugin, 0, len(servers))
	for _, server := range servers {
		plugins = append(plugins, NewMCPPlugin(server))
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

func (m *MCPManager) PendingOAuth() *MCPOAuthRequiredError {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.pendingOAuth
}

func (m *MCPManager) ClearPendingOAuth() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pendingOAuth = nil
}

func (m *MCPManager) Snapshot() []MCPServerInfo {
	m.mu.RLock()
	plugins := append([]*MCPPlugin(nil), m.plugins...)
	m.mu.RUnlock()
	out := make([]MCPServerInfo, 0, len(plugins))
	for _, plugin := range plugins {
		info := plugin.Info()
		info.Disabled = MCPDisabled(plugin.Name())
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
			m.pendingOAuth = oauthErr
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
			go m.connectWithRetry(ctx, plugin)
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
		go m.connectOne(context.Background(), plugin)
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
		go m.connectOne(context.Background(), plugin)
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
