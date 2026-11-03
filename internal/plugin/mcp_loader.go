package plugin

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	Name                  string
	Transport             string
	Source                string
	ToolNames             []string
	PromptNames           []string
	ResourceNames         []string
	ResourceTemplateNames []string
	Status                MCPStatus
	Error                 string
	Migrated              bool
	Disabled              bool
	OAuthRequired         bool
}

type MCPOAuthRequiredError struct {
	ServerName string
	Handler    *mcp.OAuthHandler
}

func (e *MCPOAuthRequiredError) Error() string {
	return fmt.Sprintf("mcp server %q requires OAuth authentication", e.ServerName)
}

type MCPPlugin struct {
	cfg           config.MCPServerConfig
	client        *mcp.Client
	adapter       *mcp.Adapter
	mu            sync.RWMutex
	connected     bool
	awaitingOAuth bool

	closed            bool
	status            MCPStatus
	lastError         string
	prompts           []string
	resources         []string
	resourceTemplates []string

	subscribed      map[string]bool
	resourceUpdated map[string]time.Time

	toolsHash string

	baselines *ToolBaselineStore

	OnToolDrift func(server string, added, removed []string)

	notifyState func()

	lastRefreshAt time.Time

	forceReauthPending bool

	autoReconnect bool

	reconnectCancel context.CancelFunc

	healthCancel context.CancelFunc

	registry *tool.Registry

	samplingHandler mcp.SamplingHandler

	elicitationHandler mcp.ElicitationHandler
}

func NewMCPPlugin(cfg config.MCPServerConfig) *MCPPlugin {
	return &MCPPlugin{
		cfg:       cfg,
		status:    MCPStatusPending,
		baselines: NewToolBaselineStore(""),
	}
}

func (m *MCPPlugin) Name() string { return m.cfg.Name }

func (m *MCPPlugin) SetSamplingHandler(h mcp.SamplingHandler) {
	m.samplingHandler = h
}

func (m *MCPPlugin) SetElicitationHandler(h mcp.ElicitationHandler) {
	m.elicitationHandler = h
}

func (m *MCPPlugin) startWSHealthProbe(client *mcp.Client) {
	if !strings.EqualFold(strings.TrimSpace(m.cfg.Type), "ws") &&
		!strings.EqualFold(strings.TrimSpace(m.cfg.Type), "websocket") {
		return
	}

	if m.healthCancel != nil {
		m.healthCancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.healthCancel = cancel

	safego.Go("plugin.mcp.wsHealthProbe", func() {
		ticker := time.NewTicker(wsHealthProbeInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}

			m.mu.RLock()
			connected := m.connected && m.client == client
			m.mu.RUnlock()
			if !connected {
				continue
			}
			probeCtx, probeCancel := context.WithTimeout(ctx, 30*time.Second)
			_, err := client.ListTools(probeCtx)
			probeCancel()
			if err == nil {
				continue
			}
			if !isWSConnError(err) {
				continue
			}
			debug.Log("mcp-ws-health", "server=%s transport dropped (%v), reconnecting", m.cfg.Name, err)

			m.attemptReconnect(ctx)

			return
		}
	})
}

const wsHealthProbeInterval = 60 * time.Second

func isWSConnError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "websocket") ||
		strings.Contains(msg, "connection closed") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "use of closed") ||
		strings.Contains(msg, "eof")
}

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
	m.forceReauthPending = false
	m.mu.Unlock()
	if forceReauth && client != nil {

		_ = client.ForceReauth()
	}
	if err := client.Start(ctx); err != nil {
		m.mu.Lock()
		m.status = MCPStatusFailed
		m.lastError = normalizeMCPError(err)
		m.mu.Unlock()
		return nil, err
	}
	tools, prompts, resources, templates, err := discoverCapabilities(ctx, client)
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
	if m.closed {

		_ = client.Close()
		return nil, fmt.Errorf("mcp server %q closed during connect", m.cfg.Name)
	}
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

	m.adapter.SetServerInstructions(client.Instructions())
	m.connected = true
	m.awaitingOAuth = false
	m.status = MCPStatusConnected
	m.lastError = ""
	m.toolsHash = computeToolsHash(tools)
	if added, removed, fire := m.recordToolBaselineLocked(m.toolsHash, toolNamesOf(tools)); fire {
		m.notifyToolDrift(added, removed)
	}
	m.prompts = prompts
	m.resources = resources
	m.resourceTemplates = templates
	m.setupNotificationHandler(client)

	m.startReconnectWatcher(client)
	m.startWSHealthProbe(client)
	return m.adapter, nil
}

func (m *MCPPlugin) postConnect() {
	m.mu.RLock()
	client := m.client
	live := m.client != nil && !m.closed
	m.mu.RUnlock()
	if !live {
		return
	}
	m.setupModernSubscriptions(client)
}

func (m *MCPPlugin) setupModernSubscriptions(client *mcp.Client) {
	ctx, cancel := context.WithTimeout(context.Background(), mcp.SubscriptionsAckTimeout)
	defer cancel()
	if client.EnableModernSubscriptions(ctx) {
		debug.Log("mcp-notif", "server=%s modern subscription stream active", m.cfg.Name)
	}
}

func (m *MCPPlugin) setupNotificationHandler(client *mcp.Client) {
	client.SetNotificationHandler(func(method string, params json.RawMessage) {
		switch method {
		case "notifications/tools/list_changed":
			debug.Log("mcp-notif", "server=%s tools/list_changed received, refreshing tools", m.cfg.Name)
			m.refreshTools(client)
		case "notifications/resources/list_changed":
			debug.Log("mcp-notif", "server=%s resources/list_changed received, refreshing resources", m.cfg.Name)
			m.refreshResources(client)
		case "notifications/resources/updated":
			m.markResourceUpdatedFromParams(params)
		case "notifications/prompts/list_changed":
			debug.Log("mcp-notif", "server=%s prompts/list_changed received", m.cfg.Name)
		case "notifications/message":

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

func (m *MCPPlugin) refreshTools(client *mcp.Client) (changed bool, count int, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tools, err := client.ListTools(ctx)
	if err != nil {
		debug.Log("mcp-notif", "server=%s tool refresh failed: %v", m.cfg.Name, err)
		return false, 0, err
	}

	newHash := computeToolsHash(tools)
	m.mu.Lock()
	if newHash == m.toolsHash && m.adapter != nil {
		m.lastRefreshAt = time.Now()
		m.mu.Unlock()

		debug.Log("mcp-notif", "server=%s tools unchanged (hash match), skipping rebuild", m.cfg.Name)
		return false, len(tools), nil
	}
	m.toolsHash = newHash
	if added, removed, fire := m.recordToolBaselineLocked(newHash, toolNamesOf(tools)); fire {
		m.notifyToolDrift(added, removed)
	}
	m.lastRefreshAt = time.Now()
	oldAdapter := m.adapter
	registry := m.registry
	readOnly := m.cfg.ReadOnly
	m.mu.Unlock()

	if oldAdapter != nil && registry != nil {

		for _, tn := range oldAdapter.RegisteredNames() {
			registry.Unregister(tn)
		}
	}

	var newAdapter *mcp.Adapter
	if readOnly {
		newAdapter = mcp.NewReadOnlyAdapter(m.cfg.Name, client, tools)
	} else {
		newAdapter = mcp.NewAdapter(m.cfg.Name, client, tools)
	}

	newAdapter.SetServerInstructions(client.Instructions())

	m.mu.Lock()
	m.adapter = newAdapter
	m.mu.Unlock()

	if registry != nil {
		if regErr := newAdapter.RegisterTools(registry); regErr != nil {
			debug.Log("mcp-notif", "server=%s tool re-registration failed: %v", m.cfg.Name, regErr)
		}
	}

	debug.Log("mcp-notif", "server=%s tools refreshed: %d tools", m.cfg.Name, len(tools))

	m.notifyStateChange()
	return true, len(tools), nil
}

func computeToolsHash(tools []mcp.ToolDefinition) string {
	sorted := append([]mcp.ToolDefinition(nil), tools...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	h := sha256.New()
	for _, t := range sorted {
		h.Write([]byte(t.Name))
		h.Write([]byte{0})
		h.Write([]byte(t.Description))
		h.Write([]byte{0})
		h.Write(t.InputSchema)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (m *MCPPlugin) refreshResources(client *mcp.Client) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resources := listResourceNames(client.ListResources(ctx))
	templates := listResourceTemplateNames(client.ListResourceTemplates(ctx))

	m.mu.Lock()
	m.resources = resources
	m.resourceTemplates = templates
	m.mu.Unlock()

	debug.Log("mcp-notif", "server=%s resources refreshed: %d items, %d templates", m.cfg.Name, len(resources), len(templates))
}

func (m *MCPPlugin) startReconnectWatcher(client *mcp.Client) {

	if m.reconnectCancel != nil {
		m.reconnectCancel()
	}
	exitCh := client.ProcessExit()
	if exitCh == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.reconnectCancel = cancel
	m.autoReconnect = true

	safego.Go("plugin.mcp.reconnectWatch", func() {
		select {
		case <-ctx.Done():
			return
		case <-exitCh:

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
			if m.attemptReconnect(ctx) {
				return
			}
		}

		debug.Log("mcp-reconnect", "server=%s all fast retries exhausted, entering cooldown re-probe every %s", m.cfg.Name, mcpReconnectCooldown)
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(mcpReconnectCooldown):
			}
			if m.attemptReconnect(ctx) {
				debug.Log("mcp-reconnect", "server=%s recovered via cooldown re-probe", m.cfg.Name)
				return
			}
		}
	})
}

const mcpReconnectCooldown = 60 * time.Second

func (m *MCPPlugin) notifyStateChange() {
	m.mu.RLock()
	fn := m.notifyState
	m.mu.RUnlock()
	if fn != nil {
		fn()
	}
}

func (m *MCPPlugin) attemptReconnect(ctx context.Context) bool {

	m.mu.Lock()
	if m.closed {

		m.mu.Unlock()
		return false
	}
	oldAdapter := m.adapter
	m.adapter = nil
	m.connected = false
	m.client = nil
	m.status = MCPStatusPending
	m.mu.Unlock()

	m.notifyStateChange()

	if oldAdapter != nil && m.registry != nil {

		for _, tn := range oldAdapter.RegisteredNames() {
			m.registry.Unregister(tn)
		}
	}

	adapter, err := m.Connect(ctx)
	if err == nil && adapter != nil {
		m.postConnect()

		m.mu.Lock()
		if m.closed {
			client := m.client
			m.client = nil
			m.adapter = nil
			m.connected = false
			m.status = MCPStatusPending
			m.mu.Unlock()
			if m.registry != nil {

				for _, tn := range adapter.RegisteredNames() {
					m.registry.Unregister(tn)
				}
			}
			if client != nil {
				_ = client.Close()
			}
			debug.Log("mcp-reconnect", "server=%s closed during reconnect; dropping fresh connection", m.cfg.Name)
			return false
		}
		m.mu.Unlock()
		debug.Log("mcp-reconnect", "server=%s reconnected successfully", m.cfg.Name)

		if m.registry != nil {
			if regErr := adapter.RegisterTools(m.registry); regErr != nil {
				debug.Log("mcp-reconnect", "server=%s tool re-registration failed: %v", m.cfg.Name, regErr)
			}
		}

		m.notifyStateChange()
		return true
	}
	m.mu.Lock()
	m.status = MCPStatusFailed
	m.lastError = normalizeMCPError(err)
	m.mu.Unlock()

	m.notifyStateChange()
	debug.Log("mcp-reconnect", "server=%s reconnect attempt failed: %v", m.cfg.Name, err)
	return false
}

func discoverCapabilities(ctx context.Context, client *mcp.Client) ([]mcp.ToolDefinition, []string, []string, []string, error) {
	type result struct {
		tools     []mcp.ToolDefinition
		prompts   []string
		resources []string
		templates []string
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
			templates: listResourceTemplateNames(client.ListResourceTemplates(ctx)),
		}
	})
	select {
	case <-ctx.Done():
		client.Abort()
		return nil, nil, nil, nil, ctx.Err()
	case res := <-done:
		return res.tools, res.prompts, res.resources, res.templates, res.err
	}
}

func (m *MCPPlugin) RegisterTools(ctx context.Context, registry *tool.Registry) error {
	adapter, err := m.Connect(ctx)
	if err != nil {
		return err
	}
	if err := adapter.RegisterTools(registry); err != nil {
		return err
	}

	m.postConnect()
	return nil
}

func (m *MCPPlugin) Adapter() *mcp.Adapter {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.adapter
}

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
		Name:                  m.cfg.Name,
		Transport:             firstNonEmpty(strings.ToLower(strings.TrimSpace(m.cfg.Type)), "stdio"),
		Source:                firstNonEmpty(m.cfg.Source, "ggcode"),
		Status:                m.status,
		Error:                 m.lastError,
		Migrated:              m.cfg.Migrated,
		PromptNames:           append([]string(nil), m.prompts...),
		ResourceNames:         append([]string(nil), m.resources...),
		ResourceTemplateNames: append([]string(nil), m.resourceTemplates...),
	}
	if m.adapter != nil {
		info.ToolNames = m.adapter.ToolNames()
	}
	return info
}

func (m *MCPPlugin) OwnedToolNames() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.adapter == nil {
		return nil
	}
	return m.adapter.RegisteredNames()
}

func (m *MCPPlugin) Close() error {

	m.mu.Lock()
	m.closed = true
	if m.reconnectCancel != nil {
		m.reconnectCancel()
		m.reconnectCancel = nil
	}
	if m.healthCancel != nil {
		m.healthCancel()
		m.healthCancel = nil
	}
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
	m.unsubscribeAll(client)
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
	result, err := client.ReadResource(ctx, uri)
	if err == nil {

		m.subscribeResourceAsync(client, uri)
	}
	return result, err
}

func (m *MCPPlugin) subscribeResourceAsync(client *mcp.Client, uri string) {
	if client == nil || !client.HasResourceSubscribe() {
		return
	}
	m.mu.Lock()
	if m.subscribed == nil {
		m.subscribed = make(map[string]bool)
	}
	if m.subscribed[uri] {
		m.mu.Unlock()
		return
	}
	m.subscribed[uri] = true
	m.mu.Unlock()
	safego.Go("mcp-resource-subscribe", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := client.SubscribeResource(ctx, uri); err != nil {
			m.mu.Lock()
			delete(m.subscribed, uri)
			m.mu.Unlock()
			debug.Log("mcp-notif", "server=%s resource subscribe %s failed: %v", m.cfg.Name, uri, err)
			return
		}
		debug.Log("mcp-notif", "server=%s subscribed to resource %s", m.cfg.Name, uri)
	})
}

func (m *MCPPlugin) markResourceUpdatedFromParams(params json.RawMessage) {
	var p struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(params, &p); err != nil || strings.TrimSpace(p.URI) == "" {
		debug.Log("mcp-notif", "server=%s resource updated with malformed params: %s", m.cfg.Name, string(params))
		return
	}
	m.markResourceUpdated(p.URI)
}

func (m *MCPPlugin) markResourceUpdated(uri string) {
	m.mu.Lock()
	if m.resourceUpdated == nil {
		m.resourceUpdated = make(map[string]time.Time)
	}
	m.resourceUpdated[uri] = time.Now()
	m.mu.Unlock()
	debug.Log("mcp-notif", "server=%s resource updated: %s", m.cfg.Name, uri)
}

func (m *MCPPlugin) ResourceUpdatedAt(uri string) (time.Time, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	t, ok := m.resourceUpdated[uri]
	return t, ok
}

func (m *MCPPlugin) unsubscribeAll(client *mcp.Client) {
	m.mu.Lock()
	uris := make([]string, 0, len(m.subscribed))
	for uri := range m.subscribed {
		uris = append(uris, uri)
	}
	m.subscribed = nil
	m.mu.Unlock()
	if client == nil || len(uris) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for _, uri := range uris {
		if err := client.UnsubscribeResource(ctx, uri); err != nil {
			debug.Log("mcp-notif", "server=%s resource unsubscribe %s failed: %v", m.cfg.Name, uri, err)
		}
	}
}

func (m *MCPPlugin) markPending() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.adapter == nil {
		m.status = MCPStatusPending
		m.lastError = ""
	}
}

func (m *MCPPlugin) Tools() []tool.Tool {
	return nil
}

func (m *MCPPlugin) Init(cfg map[string]interface{}) error {
	return nil
}

type MCPManager struct {
	plugins []*MCPPlugin

	scope              string
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

func NewMCPManager(servers []config.MCPServerConfig, registry *tool.Registry, scope string) *MCPManager {
	plugins := make([]*MCPPlugin, 0, len(servers))
	for _, server := range servers {
		p := NewMCPPlugin(server)
		p.registry = registry
		plugins = append(plugins, p)
	}
	m := &MCPManager{
		plugins:      plugins,
		registry:     registry,
		scope:        scope,
		timeout:      8 * time.Second,
		stdioTimeout: 2 * time.Minute,
	}

	for _, p := range m.plugins {
		p.OnToolDrift = m.handleToolDrift
	}
	return m
}

func (m *MCPManager) isDisabled(name string) bool {
	return MCPDisabledIn(m.scope, name)
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

func (m *MCPManager) SetSamplingHandler(h mcp.SamplingHandler) {
	m.mu.Lock()
	m.samplingHandler = h
	plugins := append([]*MCPPlugin(nil), m.plugins...)
	m.mu.Unlock()
	for _, plugin := range plugins {
		plugin.SetSamplingHandler(h)
	}
}

func (m *MCPManager) SetElicitationHandler(h mcp.ElicitationHandler) {
	m.mu.Lock()
	m.elicitationHandler = h
	plugins := append([]*MCPPlugin(nil), m.plugins...)
	m.mu.Unlock()
	for _, plugin := range plugins {
		plugin.SetElicitationHandler(h)
	}
}

func (m *MCPManager) PendingOAuthByName(name string) *MCPOAuthRequiredError {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.pendingOAuth[name]
}

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
		info.Disabled = m.isDisabled(plugin.Name())
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
			Name:                  info.Name,
			Connected:             info.Status == MCPStatusConnected,
			Pending:               info.Status == MCPStatusPending,
			Error:                 info.Error,
			ToolNames:             append([]string(nil), info.ToolNames...),
			PromptNames:           append([]string(nil), info.PromptNames...),
			ResourceNames:         append([]string(nil), info.ResourceNames...),
			ResourceTemplateNames: append([]string(nil), info.ResourceTemplateNames...),
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

func (m *MCPManager) handleToolDrift(server string, added, removed []string) {
	m.mu.Lock()
	m.warnings = append(m.warnings, fmt.Sprintf(
		"warning: MCP server %s tool definitions changed after approval (added=%v removed=%v) - verify this is expected before trusting the new tools",
		server, added, removed))
	m.mu.Unlock()
	debug.Log("mcp-drift", "server=%s TOOL DRIFT alert surfaced via manager warnings: added=%v removed=%v", server, added, removed)
	m.emitUpdate()
}

func (m *MCPManager) connectOne(ctx context.Context, p *MCPPlugin) {

	if m.isDisabled(p.Name()) {
		debug.Log("mcp-connect", "refusing connect of disabled server=%s", p.Name())
		return
	}
	connectCtx, cancel := context.WithTimeout(ctx, m.connectTimeoutFor(p))
	defer cancel()
	p.markPending()

	p.mu.Lock()
	p.registry = m.registry
	p.closed = false
	p.mu.Unlock()
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
			if m.isDisabled(plugin.Name()) {
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
		if m.isDisabled(plugin.Name()) {
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

func (m *MCPManager) Refresh(name string) (found bool, outcome RefreshOutcome, count int) {
	p := m.pluginByName(name)
	if p == nil {
		return false, RefreshNotConnected, 0
	}
	outcome, count = p.refreshToolsNow()

	m.emitUpdate()
	return true, outcome, count
}

func (m *MCPManager) Install(ctx context.Context, server config.MCPServerConfig) error {

	plugin := m.newPluginFromConfig(server)

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
			for _, toolName := range previous.OwnedToolNames() {
				m.registry.Unregister(toolName)
			}
			_ = previous.Close()
		}

		m.mu.Lock()
		delete(m.pendingOAuth, server.Name)
		m.mu.Unlock()
		m.connectOne(ctx, plugin)

		m.maybeStartInitialRecovery(ctx, plugin)
		return nil
	}
	m.plugins = append(m.plugins, plugin)
	m.mu.Unlock()

	m.connectOne(ctx, plugin)
	m.maybeStartInitialRecovery(ctx, plugin)
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

		for _, toolName := range plugin.OwnedToolNames() {
			m.registry.Unregister(toolName)
		}
		_ = plugin.Close()
		m.emitUpdate()
		return true
	}
	m.mu.Unlock()
	return false
}

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
			toolNames := p.OwnedToolNames()
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

func (m *MCPManager) ForceReauth(name string) bool {
	m.mu.RLock()
	plugins := append([]*MCPPlugin(nil), m.plugins...)
	m.mu.RUnlock()
	for _, p := range plugins {
		if p.Name() != name {
			continue
		}

		p.mu.RLock()
		var oldToolNames []string
		if p.adapter != nil {

			oldToolNames = p.adapter.RegisteredNames()
		}
		p.mu.RUnlock()
		for _, toolName := range oldToolNames {
			m.registry.Unregister(toolName)
		}

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

		plugin.mu.RLock()
		waiting := plugin.awaitingOAuth
		plugin.mu.RUnlock()
		if waiting {
			return
		}
	}

	m.maybeStartInitialRecovery(ctx, plugin)
}

func (m *MCPManager) maybeStartInitialRecovery(ctx context.Context, plugin *MCPPlugin) {
	if plugin.IsConnected() {
		return
	}
	plugin.mu.RLock()
	waiting := plugin.awaitingOAuth
	plugin.mu.RUnlock()
	if waiting {
		return
	}
	m.startInitialRecoveryLoop(ctx, plugin)
}

func (m *MCPManager) initialRecoveryShouldProbe(p *MCPPlugin) bool {
	if p.IsConnected() {
		return false
	}
	if m.isDisabled(p.Name()) {
		return false
	}
	p.mu.RLock()
	closed, waiting := p.closed, p.awaitingOAuth
	p.mu.RUnlock()
	if closed || waiting {
		return false
	}

	if m.pluginByName(p.Name()) != p {
		return false
	}
	return true
}

func (m *MCPManager) startInitialRecoveryLoop(ctx context.Context, plugin *MCPPlugin) {
	safego.Go("plugin.mcp.initialRecovery", func() {
		debug.Log("mcp-recovery", "server=%s initial connect failed; entering cooldown re-probe every %s", plugin.Name(), mcpReconnectCooldown)
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(mcpReconnectCooldown):
			}
			if !m.initialRecoveryShouldProbe(plugin) {
				debug.Log("mcp-recovery", "server=%s recovery loop stopping (connected/closed/oauth/disabled/replaced)", plugin.Name())
				return
			}
			debug.Log("mcp-recovery", "server=%s initial-connect recovery probe", plugin.Name())
			m.connectOne(ctx, plugin)
			if !m.initialRecoveryShouldProbe(plugin) {
				debug.Log("mcp-recovery", "server=%s recovery loop stopping after probe (connected/closed/oauth/disabled/replaced)", plugin.Name())
				return
			}
		}
	})
}

const mcpRefreshMinInterval = 5 * time.Second

type RefreshOutcome int

const (
	RefreshThrottled RefreshOutcome = iota

	RefreshNotConnected

	RefreshFailed

	RefreshUnchanged

	RefreshChanged
)

func (m *MCPPlugin) refreshToolsNow() (RefreshOutcome, int) {
	m.mu.Lock()
	if !m.connected || m.client == nil || m.closed {
		m.mu.Unlock()
		return RefreshNotConnected, 0
	}
	if time.Since(m.lastRefreshAt) < mcpRefreshMinInterval {
		m.mu.Unlock()
		return RefreshThrottled, 0
	}
	client := m.client
	m.mu.Unlock()
	changed, count, err := m.refreshTools(client)

	if err != nil {
		return RefreshFailed, 0
	}
	if changed {
		return RefreshChanged, count
	}
	return RefreshUnchanged, count
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

func (m *MCPManager) Reload(ctx context.Context, servers []config.MCPServerConfig) {
	m.mu.Lock()

	newByName := make(map[string]config.MCPServerConfig, len(servers))
	for _, s := range servers {
		newByName[s.Name] = s
	}

	var removed, changed []*MCPPlugin
	finalPlugins := make([]*MCPPlugin, 0, len(servers))
	oldNames := make(map[string]bool, len(m.plugins))
	for _, p := range m.plugins {
		oldNames[p.Name()] = true
		newCfg, exists := newByName[p.Name()]
		if !exists {
			removed = append(removed, p)
			delete(m.pendingOAuth, p.Name())
			continue
		}
		if !mcpServerConfigEqual(p.cfg, newCfg) {
			changed = append(changed, p)
			delete(m.pendingOAuth, p.Name())
			continue
		}
		finalPlugins = append(finalPlugins, p)
	}

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

	closeAndUnregister := func(p *MCPPlugin, reason string) {
		for _, toolName := range p.OwnedToolNames() {
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

	for _, p := range connectPlugins {
		if m.isDisabled(p.Name()) {
			continue
		}
		pluginCopy := p
		safego.Go("plugin.mcp.reloadConnect", func() {
			m.connectOne(ctx, pluginCopy)
			m.maybeStartInitialRecovery(ctx, pluginCopy)
		})
	}

	debug.Log("mcp-reload", "reload complete: removed=%d changed=%d total=%d", len(removed), len(changed), len(finalPlugins))
}

func (m *MCPManager) newPluginFromConfig(s config.MCPServerConfig) *MCPPlugin {
	p := NewMCPPlugin(s)
	p.registry = m.registry

	p.notifyState = m.emitUpdate

	p.OnToolDrift = m.handleToolDrift
	if m.samplingHandler != nil {
		p.SetSamplingHandler(m.samplingHandler)
	}
	if m.elicitationHandler != nil {
		p.SetElicitationHandler(m.elicitationHandler)
	}
	return p
}

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

	if a.OAuthClientID != b.OAuthClientID {
		return false
	}
	if a.OAuthClientSecret != b.OAuthClientSecret {
		return false
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

func listResourceTemplateNames(templates []mcp.ResourceTemplate, err error) []string {
	if isOptionalCapabilityUnavailable(err) || err != nil {
		return nil
	}
	names := make([]string, 0, len(templates))
	for _, tmpl := range templates {
		uri := strings.TrimSpace(tmpl.URITemplate)
		if uri == "" {
			continue
		}
		name := strings.TrimSpace(tmpl.Name)
		if name == "" {
			names = append(names, uri)
		} else {
			names = append(names, fmt.Sprintf("%s (%s)", name, uri))
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
