package webui

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/topcheer/ggcode/internal/auth"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
)

// MCPStatusFunc returns runtime MCP server statuses. Keys are server names.
type MCPStatusFunc func() map[string]MCPRuntimeStatus

// MCPRuntimeStatus describes a running MCP server's state.
type MCPRuntimeStatus struct {
	Connected bool     `json:"connected"`
	Pending   bool     `json:"pending"`
	Disabled  bool     `json:"disabled"`
	Error     string   `json:"error,omitempty"`
	Tools     []string `json:"tools,omitempty"`
}

// IMStatusFunc returns runtime IM adapter states. Keys are adapter names.
type IMStatusFunc func() []IMRuntimeStatus

// IMRuntimeStatus describes a running IM adapter's state.
type IMRuntimeStatus struct {
	Adapter   string   `json:"adapter"`
	Platform  string   `json:"platform"`
	Healthy   bool     `json:"healthy"`
	Status    string   `json:"status"`
	LastError string   `json:"last_error,omitempty"`
	BoundDir  string   `json:"bound_dir,omitempty"`
	ChannelID string   `json:"channel_id,omitempty"`
	TargetID  string   `json:"target_id,omitempty"`
	Muted     bool     `json:"muted"`
	Disabled  bool     `json:"disabled"`
	AllDirs   []string `json:"all_dirs,omitempty"` // all persisted bound directories
}

// IMActionFunc performs an IM action (enable/disable/mute/unmute/unbind).
type IMActionFunc func(adapter string, action string) error

// A2ADiscoverFunc returns discovered A2A instances (other running ggcode processes).
type A2ADiscoverFunc func() []A2ADiscoveredInstance

// A2ADiscoveredInstance describes a remote ggcode instance discovered via A2A.
type A2ADiscoveredInstance struct {
	ID        string `json:"id"`
	Workspace string `json:"workspace"`
	Endpoint  string `json:"endpoint"`
	Status    string `json:"status"`
	StartedAt string `json:"started_at"`
}

// ChatBridge is the interface between webui and the agent.
// Implemented by im.DaemonBridge — webchat sends messages through the
// bridge (which routes them as interruptions to the running agent loop)
// and subscribes to agent events via the EventSubscriber.
type ChatBridge interface {
	// Messages returns the current agent conversation history.
	Messages() []provider.Message
	// SendUserMessage injects a user message into the agent.
	// If the agent is idle, it starts a new run. If running, the message
	// is queued as an interruption (same as TUI/IM mid-run input).
	SendUserMessage(content []provider.ContentBlock)
	// Subscribe registers a callback for agent streaming events.
	// Returns an unsubscribe function.
	Subscribe(fn func(provider.StreamEvent)) func()
}

// AgentRunner is kept for backward compatibility — ChatBridge replaces it.
type AgentRunner interface {
	RunStream(ctx context.Context, userMsg string, onEvent func(provider.StreamEvent)) error
	RunStreamWithContent(ctx context.Context, content []provider.ContentBlock, onEvent func(provider.StreamEvent)) error
	Messages() []provider.Message
}

// Server provides a built-in WebUI for configuration and chat.
type Server struct {
	cfg           *config.Config
	mcpStatusFn   MCPStatusFunc
	imStatusFn    IMStatusFunc
	imActionFn    IMActionFunc
	restartFn     func()          // called when user triggers restart from WebUI
	a2aDiscoverFn A2ADiscoverFunc // returns discovered A2A instances
	sessionStore  session.Store
	workspace     string // current ggcode working directory
	saveScope     string // "global" or "instance" — where config saves go
	chatBridge    ChatBridge
	agent         AgentRunner // legacy, for non-bridge setups
	agentMu       sync.Mutex
	agentBusy     atomic.Bool
	mu            sync.RWMutex
	addr          string
	listener      net.Listener
	mux           *http.ServeMux
}

// SetMCPStatusFn sets the runtime MCP status provider.
func (s *Server) SetMCPStatusFn(fn MCPStatusFunc) {
	s.mcpStatusFn = fn
}

// SetIMStatusFn sets the runtime IM status provider.
func (s *Server) SetIMStatusFn(fn IMStatusFunc) {
	s.imStatusFn = fn
}

// SetIMActionFn sets the IM action handler.
func (s *Server) SetIMActionFn(fn IMActionFunc) {
	s.imActionFn = fn
}

// SetRestartFn sets the restart callback (triggers process self-restart).
func (s *Server) SetRestartFn(fn func()) {
	s.restartFn = fn
}

// SetA2ADiscoverFn sets the A2A instance discovery provider.
func (s *Server) SetA2ADiscoverFn(fn A2ADiscoverFunc) {
	s.a2aDiscoverFn = fn
}

// SetSessionStore sets the session store for browsing history.
func (s *Server) SetSessionStore(store session.Store, workspace string) {
	s.sessionStore = store
	s.workspace = workspace
}

// SetAgent sets the agent runner for chat functionality (legacy mode).
func (s *Server) SetAgent(a AgentRunner) {
	s.agent = a
}

// SetChatBridge sets the chat bridge for WebChat (preferred mode).
// When set, WebChat uses bridge's interruption/subscription model instead
// of directly running the agent.
func (s *Server) SetChatBridge(b ChatBridge) {
	s.chatBridge = b
}

// NewServer creates a WebUI server bound to the given config.
func NewServer(cfg *config.Config) *Server {
	s := &Server{
		cfg:       cfg,
		mux:       http.NewServeMux(),
		saveScope: "global",
	}
	s.routes()
	return s
}

// saveConfig persists config using the current save scope.
func (s *Server) saveConfig() error {
	return s.cfg.SaveScoped(s.saveScope)
}

// SetSaveScope sets the config save scope for WebUI operations.
func (s *Server) SetSaveScope(scope string) {
	s.saveScope = scope
}

// Start starts the HTTP server on the given address (e.g. "127.0.0.1:0" for auto).
// Returns the actual listening address.
func (s *Server) Start(addr string) (string, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return "", fmt.Errorf("webui listen: %w", err)
	}
	s.listener = ln
	s.addr = ln.Addr().String()

	go func() {
		_ = http.Serve(ln, s.mux)
	}()

	return s.addr, nil
}

// Addr returns the listening address (empty if not started).
func (s *Server) Addr() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.addr
}

// Close shuts down the server.
func (s *Server) Close() error {
	if s.listener != nil {
		return s.listener.Close()
	}
	return nil
}

func (s *Server) routes() {
	// Static SPA
	s.mux.HandleFunc("/", s.serveSPA)

	// Config REST API
	s.mux.HandleFunc("/api/config", s.handleConfig)
	s.mux.HandleFunc("/api/config/scope", s.handleConfigScope)
	s.mux.HandleFunc("/api/config/active", s.handleActiveSelection)
	s.mux.HandleFunc("/api/vendors", s.handleVendors)
	s.mux.HandleFunc("/api/vendors/", s.handleVendorDetail)
	s.mux.HandleFunc("/api/vendors/{vendor}/endpoints", s.handleEndpoints)
	s.mux.HandleFunc("/api/vendors/{vendor}/endpoints/{endpoint}", s.handleEndpointDetail)
	s.mux.HandleFunc("/api/vendors/{vendor}/endpoints/{endpoint}/apikey", s.handleAPIKey)
	s.mux.HandleFunc("/api/mcp", s.handleMCP)
	s.mux.HandleFunc("/api/mcp/status", s.handleMCPStatus)
	s.mux.HandleFunc("/api/mcp/", s.handleMCPDetail)
	s.mux.HandleFunc("/api/im", s.handleIM)
	s.mux.HandleFunc("/api/im/status", s.handleIMStatus)
	s.mux.HandleFunc("/api/im/action", s.handleIMAction)
	s.mux.HandleFunc("/api/im/adapters", s.handleIMAdapters)
	s.mux.HandleFunc("/api/im/adapters/", s.handleIMAdapterDetail)
	s.mux.HandleFunc("/api/general", s.handleGeneral)
	s.mux.HandleFunc("/api/impersonate", s.handleImpersonate)
	s.mux.HandleFunc("/api/a2a", s.handleA2A)
	s.mux.HandleFunc("/api/a2a/discover", s.handleA2ADiscover)
	s.mux.HandleFunc("/api/sessions", s.handleSessions)
	s.mux.HandleFunc("/api/sessions/", s.handleSessionDetail)
	s.mux.HandleFunc("/api/chat/ws", s.handleChatWS)
	s.mux.HandleFunc("/api/chat/history", s.handleChatHistory)
	s.mux.HandleFunc("/api/restart", s.handleRestart)
}

// --- Static SPA ---

func (s *Server) serveSPA(w http.ResponseWriter, r *http.Request) {
	// All non-API routes serve the SPA index.html
	data, err := fs.ReadFile(spafs, "index.html")
	if err != nil {
		// Fallback: try reading from the raw embed FS
		data, err = fs.ReadFile(spaFS, "dist/index.html")
		if err != nil {
			http.Error(w, "SPA not found: "+err.Error(), http.StatusNotFound)
			return
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(data)
}

// --- Config API ---

// GET /api/config — full config as JSON (includes scope info)
func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := sanitizeConfigForAPI(s.cfg)
	result["_scope"] = map[string]interface{}{
		"current":         s.saveScope,
		"hasInstance":     s.cfg.HasInstanceConfigAttached(),
		"hasInstanceFile": s.cfg.HasInstanceConfigFile(),
		"instanceDir":     s.cfg.InstanceDirPath(),
	}
	writeJSON(w, result)
}

// GET/PUT /api/config/scope — read/switch config save scope
func (s *Server) handleConfigScope(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.mu.RLock()
		defer s.mu.RUnlock()
		writeJSON(w, map[string]interface{}{
			"scope":           s.saveScope,
			"hasInstance":     s.cfg.HasInstanceConfigAttached(),
			"hasInstanceFile": s.cfg.HasInstanceConfigFile(),
			"instanceDir":     s.cfg.InstanceDirPath(),
		})
	case http.MethodPut:
		var req struct {
			Scope string `json:"scope"`
		}
		if err := readJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if req.Scope != "global" && req.Scope != "instance" {
			writeError(w, http.StatusBadRequest, "scope must be 'global' or 'instance'")
			return
		}
		if req.Scope == "instance" && !s.cfg.HasInstanceConfigAttached() {
			writeError(w, http.StatusBadRequest, "no instance config available for this workspace")
			return
		}
		s.mu.Lock()
		s.saveScope = req.Scope
		s.mu.Unlock()
		writeJSON(w, map[string]string{"scope": req.Scope})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// GET/PUT /api/config/active — read/switch active vendor+endpoint+model
func (s *Server) handleActiveSelection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.mu.RLock()
		defer s.mu.RUnlock()
		writeJSON(w, map[string]string{
			"vendor":   s.cfg.Vendor,
			"endpoint": s.cfg.Endpoint,
			"model":    s.cfg.Model,
		})
	case http.MethodPut:
		var req struct {
			Vendor   string `json:"vendor"`
			Endpoint string `json:"endpoint"`
			Model    string `json:"model"`
		}
		if err := readJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if err := s.cfg.SetActiveSelection(req.Vendor, req.Endpoint, req.Model); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := s.saveConfig(); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, map[string]string{
			"vendor":   s.cfg.Vendor,
			"endpoint": s.cfg.Endpoint,
			"model":    s.cfg.Model,
		})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// GET /api/vendors — list all vendor names
func (s *Server) handleVendors(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.mu.RLock()
		defer s.mu.RUnlock()

		type vendorSummary struct {
			ID          string   `json:"id"`
			DisplayName string   `json:"display_name"`
			HasAPIKey   bool     `json:"has_api_key"`
			EndpointIDs []string `json:"endpoint_ids"`
		}

		vendors := make([]vendorSummary, 0)
		for _, name := range s.cfg.VendorNames() {
			vc := s.cfg.Vendors[name]
			epIDs := s.cfg.EndpointNames(name)
			vendors = append(vendors, vendorSummary{
				ID:          name,
				DisplayName: vc.DisplayName,
				HasAPIKey:   strings.TrimSpace(vc.APIKey) != "",
				EndpointIDs: epIDs,
			})
		}
		writeJSON(w, vendors)

	case http.MethodPost:
		var req struct {
			Name        string `json:"name"`
			DisplayName string `json:"display_name"`
			APIKey      string `json:"api_key"`
		}
		if err := readJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if err := s.cfg.AddVendor(req.Name, req.DisplayName, req.APIKey); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := s.saveConfig(); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, map[string]string{"status": "ok", "vendor": req.Name})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// GET/PUT/DELETE /api/vendors/{vendor}
func (s *Server) handleVendorDetail(w http.ResponseWriter, r *http.Request) {
	vendor := strings.TrimPrefix(r.URL.Path, "/api/vendors/")

	switch r.Method {
	case http.MethodGet:
		s.mu.RLock()
		defer s.mu.RUnlock()
		vc, ok := s.cfg.Vendors[vendor]
		if !ok {
			writeError(w, http.StatusNotFound, "vendor not found")
			return
		}
		writeJSON(w, map[string]interface{}{
			"id":           vendor,
			"display_name": vc.DisplayName,
			"has_api_key":  strings.TrimSpace(vc.APIKey) != "",
			"endpoints":    vc.Endpoints,
		})

	case http.MethodPut:
		var req struct {
			DisplayName string `json:"display_name"`
			APIKey      string `json:"api_key"`
		}
		if err := readJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		vc, ok := s.cfg.Vendors[vendor]
		if !ok {
			writeError(w, http.StatusNotFound, "vendor not found")
			return
		}
		if req.DisplayName != "" {
			vc.DisplayName = req.DisplayName
		}
		// Update API key if provided (empty string = clear, "__unchanged__" = keep)
		if req.APIKey != "__unchanged__" {
			if err := s.cfg.SetVendorAPIKey(vendor, req.APIKey); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			// Re-read after SetVendorAPIKey modified it
			vc = s.cfg.Vendors[vendor]
		}
		s.cfg.Vendors[vendor] = vc
		if err := s.saveConfig(); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, map[string]string{"status": "ok"})

	case http.MethodDelete:
		s.mu.Lock()
		defer s.mu.Unlock()
		if err := s.cfg.RemoveVendor(vendor); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := s.saveConfig(); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, map[string]string{"status": "deleted"})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// GET/POST /api/vendors/{vendor}/endpoints
func (s *Server) handleEndpoints(w http.ResponseWriter, r *http.Request) {
	vendor := r.PathValue("vendor")

	switch r.Method {
	case http.MethodGet:
		s.mu.RLock()
		defer s.mu.RUnlock()
		vc, ok := s.cfg.Vendors[vendor]
		if !ok {
			writeError(w, http.StatusNotFound, "vendor not found")
			return
		}
		writeJSON(w, vc.Endpoints)

	case http.MethodPost:
		var req struct {
			Name     string `json:"name"`
			Protocol string `json:"protocol"`
			BaseURL  string `json:"base_url"`
			APIKey   string `json:"api_key"`
		}
		if err := readJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if err := s.cfg.AddEndpoint(vendor, req.Name, req.Protocol, req.BaseURL, req.APIKey); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := s.saveConfig(); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, map[string]string{"status": "ok", "endpoint": req.Name})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// GET/PUT/DELETE /api/vendors/{vendor}/endpoints/{endpoint}
func (s *Server) handleEndpointDetail(w http.ResponseWriter, r *http.Request) {
	vendor := r.PathValue("vendor")
	endpoint := r.PathValue("endpoint")

	switch r.Method {
	case http.MethodGet:
		s.mu.RLock()
		defer s.mu.RUnlock()
		vc, ok := s.cfg.Vendors[vendor]
		if !ok {
			writeError(w, http.StatusNotFound, "vendor not found")
			return
		}
		ep, ok := vc.Endpoints[endpoint]
		if !ok {
			writeError(w, http.StatusNotFound, "endpoint not found")
			return
		}
		writeJSON(w, ep)

	case http.MethodPut:
		var ep config.EndpointConfig
		if err := readJSON(r, &ep); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		vc, ok := s.cfg.Vendors[vendor]
		if !ok {
			writeError(w, http.StatusNotFound, "vendor not found")
			return
		}
		if vc.Endpoints == nil {
			vc.Endpoints = make(map[string]config.EndpointConfig)
		}
		vc.Endpoints[endpoint] = ep
		s.cfg.Vendors[vendor] = vc
		if err := s.saveConfig(); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, ep)

	case http.MethodDelete:
		s.mu.Lock()
		defer s.mu.Unlock()
		if err := s.cfg.RemoveEndpoint(vendor, endpoint); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := s.saveConfig(); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, map[string]string{"status": "deleted"})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// PUT /api/vendors/{vendor}/endpoints/{endpoint}/apikey
func (s *Server) handleAPIKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	vendor := r.PathValue("vendor")
	endpoint := r.PathValue("endpoint")

	var req struct {
		APIKey       string `json:"api_key"`
		VendorScoped bool   `json:"vendor_scoped"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.cfg.SetEndpointAPIKey(vendor, endpoint, req.APIKey, req.VendorScoped); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.saveConfig(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

// GET/POST/DELETE /api/mcp
func (s *Server) handleMCP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.mu.RLock()
		cfg := s.cfg
		s.mu.RUnlock()

		// Merge config with runtime status
		type mcpEntry struct {
			config.MCPServerConfig
			Runtime *MCPRuntimeStatus `json:"runtime,omitempty"`
		}
		entries := make([]mcpEntry, len(cfg.MCPServers))
		statusMap := s.getRuntimeMCPStatus()
		for i, srv := range cfg.MCPServers {
			e := mcpEntry{MCPServerConfig: srv}
			if st, ok := statusMap[srv.Name]; ok {
				e.Runtime = &st
			}
			entries[i] = e
		}
		writeJSON(w, entries)

	case http.MethodPost:
		var server config.MCPServerConfig
		if err := readJSON(r, &server); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		s.cfg.UpsertMCPServer(server)
		if err := s.saveConfig(); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, server)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) getRuntimeMCPStatus() map[string]MCPRuntimeStatus {
	if s.mcpStatusFn == nil {
		return nil
	}
	return s.mcpStatusFn()
}

// GET /api/mcp/status — runtime MCP status only
func (s *Server) handleMCPStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, s.getRuntimeMCPStatus())
}

// DELETE /api/mcp/{name}
func (s *Server) handleMCPDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/api/mcp/")
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.cfg.RemoveMCPServer(name) {
		writeError(w, http.StatusNotFound, "MCP server not found")
		return
	}
	if err := s.saveConfig(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]string{"status": "deleted"})
}

// GET/PUT /api/im — IM config
func (s *Server) handleIM(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.mu.RLock()
		defer s.mu.RUnlock()
		writeJSON(w, s.cfg.IM)
	case http.MethodPut:
		var im config.IMConfig
		if err := readJSON(r, &im); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		s.cfg.IM = im
		if err := s.saveConfig(); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, im)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// GET /api/im/status — runtime IM adapter status with bindings
func (s *Server) handleIMStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.imStatusFn != nil {
		writeJSON(w, s.imStatusFn())
		return
	}
	writeJSON(w, []IMRuntimeStatus{})
}

// POST /api/im/action — perform IM action (enable/disable/mute/unmute)
func (s *Server) handleIMAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Adapter string `json:"adapter"`
		Action  string `json:"action"` // enable, disable, mute, unmute
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.imActionFn == nil {
		writeError(w, http.StatusServiceUnavailable, "IM runtime not available")
		return
	}
	if err := s.imActionFn(req.Adapter, req.Action); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

// GET /api/im/adapters — list all IM adapters
func (s *Server) handleIMAdapters(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	writeJSON(w, s.cfg.IM.Adapters)
}

// GET/POST/PUT/DELETE /api/im/adapters/{name}
func (s *Server) handleIMAdapterDetail(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/api/im/adapters/")
	if name == "" {
		writeError(w, http.StatusBadRequest, "adapter name required")
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.mu.RLock()
		defer s.mu.RUnlock()
		adapter, ok := s.cfg.IM.Adapters[name]
		if !ok {
			writeError(w, http.StatusNotFound, "adapter not found")
			return
		}
		writeJSON(w, adapter)

	case http.MethodPost, http.MethodPut:
		var adapter config.IMAdapterConfig
		if err := readJSON(r, &adapter); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if err := s.cfg.AddIMAdapter(name, adapter); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := s.saveConfig(); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, map[string]string{"status": "ok", "adapter": name})

	case http.MethodDelete:
		s.mu.Lock()
		defer s.mu.Unlock()
		if err := s.cfg.RemoveIMAdapter(name); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := s.saveConfig(); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, map[string]string{"status": "deleted", "adapter": name})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// GET/PUT /api/impersonate — read/update impersonation settings
func (s *Server) handleImpersonate(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.mu.RLock()
		defer s.mu.RUnlock()
		imp := s.cfg.Impersonation
		presets := make([]map[string]interface{}, 0)
		for _, p := range provider.DefaultImpersonationPresets() {
			presets = append(presets, map[string]interface{}{
				"id":              p.ID,
				"display_name":    p.DisplayName,
				"default_version": p.DefaultVersion,
				"extra_headers":   p.ExtraHeaders,
			})
		}
		writeJSON(w, map[string]interface{}{
			"current": map[string]interface{}{
				"preset":         imp.Preset,
				"custom_version": imp.CustomVersion,
				"custom_headers": imp.CustomHeaders,
			},
			"presets": presets,
		})

	case http.MethodPut:
		var req struct {
			Preset        string            `json:"preset"`
			CustomVersion string            `json:"custom_version"`
			CustomHeaders map[string]string `json:"custom_headers"`
		}
		if err := readJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		// Apply to runtime
		var presetPtr *provider.ImpersonationPreset
		if req.Preset != "" && req.Preset != "none" {
			presetPtr = provider.FindPresetByID(req.Preset)
			if presetPtr == nil {
				writeError(w, http.StatusBadRequest, "unknown preset: "+req.Preset)
				return
			}
		}
		provider.SetActiveImpersonation(presetPtr, req.CustomVersion, req.CustomHeaders)

		// Persist
		s.mu.Lock()
		defer s.mu.Unlock()
		impCfg := config.ImpersonationConfig{
			Preset:        req.Preset,
			CustomVersion: req.CustomVersion,
			CustomHeaders: req.CustomHeaders,
		}
		if err := s.cfg.SaveImpersonation(impCfg); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.cfg.Impersonation = impCfg
		writeJSON(w, map[string]string{"status": "ok"})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// GET/PUT /api/a2a — A2A protocol configuration
func (s *Server) handleA2A(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.mu.RLock()
		defer s.mu.RUnlock()
		a2a := s.cfg.A2A
		writeJSON(w, map[string]interface{}{
			"disabled":     a2a.Disabled,
			"port":         a2a.Port,
			"host":         a2a.Host,
			"max_tasks":    a2a.MaxTasks,
			"task_timeout": a2a.TaskTimeout,
			"auth": map[string]interface{}{
				"has_api_key": strings.TrimSpace(a2a.Auth.APIKey) != "",
				"api_keys":    a2a.Auth.APIKeys,
				"oauth2":      sanitizeOAuth2(a2a.Auth.OAuth2),
				"oidc":        sanitizeOIDC(a2a.Auth.OIDC),
				"mtls":        sanitizeMTLS(a2a.Auth.MTLS),
			},
			"has_legacy_api_key": strings.TrimSpace(a2a.APIKey) != "",
			"presets":            a2aAuthPresets(),
		})
	case http.MethodPut:
		var req struct {
			Disabled    *bool  `json:"disabled"`
			Port        *int   `json:"port"`
			Host        string `json:"host"`
			MaxTasks    *int   `json:"max_tasks"`
			TaskTimeout string `json:"task_timeout"`
			Auth        *struct {
				APIKey      string                  `json:"api_key"`
				APIKeys     []string                `json:"api_keys"`
				OAuth2      *config.A2AOAuth2Config `json:"oauth2"`
				OAuth2Clear bool                    `json:"oauth2_clear"`
				OIDC        *config.A2AOIDCConfig   `json:"oidc"`
				OIDCClear   bool                    `json:"oidc_clear"`
				MTLS        *config.A2AMTLSConfig   `json:"mtls"`
				MTLSClear   bool                    `json:"mtls_clear"`
			} `json:"auth"`
		}
		if err := readJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if req.Disabled != nil {
			s.cfg.A2A.Disabled = *req.Disabled
		}
		if req.Port != nil {
			s.cfg.A2A.Port = *req.Port
		}
		if req.Host != "" {
			s.cfg.A2A.Host = req.Host
		}
		if req.MaxTasks != nil {
			s.cfg.A2A.MaxTasks = *req.MaxTasks
		}
		if req.TaskTimeout != "" {
			s.cfg.A2A.TaskTimeout = req.TaskTimeout
		}
		if req.Auth != nil {
			if req.Auth.APIKey != "" {
				s.cfg.A2A.Auth.APIKey = req.Auth.APIKey
			}
			if req.Auth.APIKeys != nil {
				// Replace entire list (empty slice = clear all)
				s.cfg.A2A.Auth.APIKeys = req.Auth.APIKeys
			}
			if req.Auth.OAuth2Clear {
				s.cfg.A2A.Auth.OAuth2 = nil
			} else if req.Auth.OAuth2 != nil {
				s.cfg.A2A.Auth.OAuth2 = req.Auth.OAuth2
			}
			if req.Auth.OIDCClear {
				s.cfg.A2A.Auth.OIDC = nil
			} else if req.Auth.OIDC != nil {
				s.cfg.A2A.Auth.OIDC = req.Auth.OIDC
			}
			if req.Auth.MTLSClear {
				s.cfg.A2A.Auth.MTLS = nil
			} else if req.Auth.MTLS != nil {
				s.cfg.A2A.Auth.MTLS = req.Auth.MTLS
			}
		}
		if err := s.saveConfig(); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, map[string]string{"status": "ok"})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// GET /api/a2a/discover — list discovered A2A instances
func (s *Server) handleA2ADiscover(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.a2aDiscoverFn == nil {
		writeJSON(w, []A2ADiscoveredInstance{})
		return
	}
	instances := s.a2aDiscoverFn()
	if instances == nil {
		instances = []A2ADiscoveredInstance{}
	}
	writeJSON(w, instances)
}

// GET /api/sessions — list sessions grouped by workspace
func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.sessionStore == nil {
		writeJSON(w, map[string]interface{}{"groups": []interface{}{}, "total": 0, "current_workspace": ""})
		return
	}
	sessions, err := s.sessionStore.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Group by workspace
	type sessionSummary struct {
		ID        string `json:"id"`
		Title     string `json:"title"`
		CreatedAt string `json:"created_at"`
		UpdatedAt string `json:"updated_at"`
		MsgCount  int    `json:"msg_count"`
		Vendor    string `json:"vendor,omitempty"`
		Endpoint  string `json:"endpoint,omitempty"`
		Model     string `json:"model,omitempty"`
	}
	type workspaceGroup struct {
		Workspace string           `json:"workspace"`
		ShortName string           `json:"short_name"`
		Count     int              `json:"count"`
		Sessions  []sessionSummary `json:"sessions"`
		Current   bool             `json:"current"`
	}
	groupMap := map[string]*workspaceGroup{}
	var groupOrder []string
	// Ensure current workspace is first
	if s.workspace != "" {
		groupMap[s.workspace] = &workspaceGroup{
			Workspace: s.workspace,
			ShortName: filepath.Base(s.workspace),
			Current:   true,
		}
		groupOrder = append(groupOrder, s.workspace)
	}
	for _, ses := range sessions {
		ws := ses.Workspace
		if ws == "" {
			ws = "(no workspace)"
		}
		if _, ok := groupMap[ws]; !ok {
			groupMap[ws] = &workspaceGroup{
				Workspace: ws,
				ShortName: filepath.Base(ws),
			}
			groupOrder = append(groupOrder, ws)
		}
		g := groupMap[ws]
		g.Sessions = append(g.Sessions, sessionSummary{
			ID:        ses.ID,
			Title:     ses.Title,
			CreatedAt: ses.CreatedAt.Format(time.RFC3339),
			UpdatedAt: ses.UpdatedAt.Format(time.RFC3339),
			MsgCount:  len(ses.Messages),
			Vendor:    ses.Vendor,
			Endpoint:  ses.Endpoint,
			Model:     ses.Model,
		})
		g.Count++
	}
	groups := make([]workspaceGroup, 0, len(groupOrder))
	for _, ws := range groupOrder {
		g := groupMap[ws]
		groups = append(groups, *g)
	}
	writeJSON(w, map[string]interface{}{
		"groups":            groups,
		"total":             len(sessions),
		"current_workspace": s.workspace,
	})
}

// GET /api/sessions/{id} — get session detail with messages
func (s *Server) handleSessionDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.sessionStore == nil {
		writeError(w, http.StatusServiceUnavailable, "session store not available")
		return
	}
	id := r.URL.Path[len("/api/sessions/"):]
	if id == "" {
		writeError(w, http.StatusBadRequest, "session id required")
		return
	}
	ses, err := s.sessionStore.Load(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	// Serialize messages, omitting large image data
	type contentBlock struct {
		Type     string          `json:"type"`
		Text     string          `json:"text,omitempty"`
		ToolName string          `json:"tool_name,omitempty"`
		ToolID   string          `json:"tool_id,omitempty"`
		Input    json.RawMessage `json:"input,omitempty"`
		Output   string          `json:"output,omitempty"`
		IsError  bool            `json:"is_error,omitempty"`
	}
	type message struct {
		Role    string         `json:"role"`
		Content []contentBlock `json:"content"`
	}
	msgs := make([]message, 0, len(ses.Messages))
	for _, m := range ses.Messages {
		// Skip system messages — they contain internal prompts and should
		// not be shown in the history detail view.
		if m.Role == "system" {
			continue
		}
		blocks := make([]contentBlock, 0, len(m.Content))
		for _, b := range m.Content {
			blocks = append(blocks, contentBlock{
				Type:     b.Type,
				Text:     b.Text,
				ToolName: b.ToolName,
				ToolID:   b.ToolID,
				Input:    b.Input,
				Output:   b.Output,
				IsError:  b.IsError,
			})
		}
		msgs = append(msgs, message{Role: m.Role, Content: blocks})
	}
	writeJSON(w, map[string]interface{}{
		"id":         ses.ID,
		"title":      ses.Title,
		"workspace":  ses.Workspace,
		"vendor":     ses.Vendor,
		"endpoint":   ses.Endpoint,
		"model":      ses.Model,
		"created_at": ses.CreatedAt.Format(time.RFC3339),
		"updated_at": ses.UpdatedAt.Format(time.RFC3339),
		"messages":   msgs,
	})
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// handleChatWS handles the WebSocket connection for chat.
// Protocol:
//
//	Client → Server: {"type":"user_message","text":"..."}
//	Server → Client: {"type":"text_delta","text":"..."}
//	Server → Client: {"type":"tool_call","id":"...","name":"...","arguments":"..."}
//	Server → Client: {"type":"tool_result","name":"...","result":"...","is_error":false}
//	Server → Client: {"type":"done","usage":{"input_tokens":0,"output_tokens":0}}
//	Server → Client: {"type":"error","error":"..."}
//
// GET /api/chat/history — returns current agent conversation messages
func (s *Server) handleChatHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var msgs []provider.Message
	switch {
	case s.chatBridge != nil:
		msgs = s.chatBridge.Messages()
	case s.agent != nil:
		s.agentMu.Lock()
		msgs = s.agent.Messages()
		s.agentMu.Unlock()
	default:
		writeJSON(w, []interface{}{})
		return
	}

	type contentBlock struct {
		Type     string          `json:"type"`
		Text     string          `json:"text,omitempty"`
		ToolName string          `json:"tool_name,omitempty"`
		ToolID   string          `json:"tool_id,omitempty"`
		Input    json.RawMessage `json:"input,omitempty"`
		Output   string          `json:"output,omitempty"`
		IsError  bool            `json:"is_error,omitempty"`
	}
	type message struct {
		Role    string         `json:"role"`
		Content []contentBlock `json:"content"`
	}
	result := make([]message, 0, len(msgs))
	for _, m := range msgs {
		if m.Role == "system" {
			continue
		}
		blocks := make([]contentBlock, 0, len(m.Content))
		for _, b := range m.Content {
			blocks = append(blocks, contentBlock{
				Type:     b.Type,
				Text:     b.Text,
				ToolName: b.ToolName,
				ToolID:   b.ToolID,
				Input:    b.Input,
				Output:   b.Output,
				IsError:  b.IsError,
			})
		}
		result = append(result, message{Role: m.Role, Content: blocks})
	}
	writeJSON(w, result)
}

func (s *Server) handleChatWS(w http.ResponseWriter, r *http.Request) {
	if s.chatBridge == nil && s.agent == nil {
		http.Error(w, "agent not available", http.StatusServiceUnavailable)
		return
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		debug.Log("webui", "ws upgrade error: %v", err)
		return
	}
	defer conn.Close()

	// Dedicated write goroutine to serialize all WS writes.
	// Gorilla WebSocket requires read and write to be on different goroutines
	// and all writes serialized. This channel achieves both.
	writeCh := make(chan interface{}, 64)
	writeDone := make(chan struct{})
	go func() {
		defer close(writeDone)
		for msg := range writeCh {
			if err := conn.WriteJSON(msg); err != nil {
				debug.Log("webui", "ws write error: %v", err)
				return
			}
		}
	}()

	send := func(msg interface{}) {
		select {
		case writeCh <- msg:
		default:
			debug.Log("webui", "ws write channel full, dropping message")
		}
	}

	// In bridge mode: subscribe immediately so all agent events are forwarded
	var unsub func()
	if s.chatBridge != nil {
		unsub = s.chatBridge.Subscribe(func(event provider.StreamEvent) {
			send(streamEventToJSON(event))
		})
		defer unsub()
	}

	for {
		_, msgBytes, err := conn.ReadMessage()
		if err != nil {
			close(writeCh)
			<-writeDone
			return
		}

		var msg struct {
			Type   string `json:"type"`
			Text   string `json:"text"`
			Images []struct {
				MIME string `json:"mime"`
				Data string `json:"data"` // base64
			} `json:"images"`
			Files []struct {
				Name string `json:"name"`
				MIME string `json:"mime"`
				Data string `json:"data"` // base64
			} `json:"files"`
		}
		if err := json.Unmarshal(msgBytes, &msg); err != nil {
			send(map[string]interface{}{"type": "error", "error": "invalid message format"})
			continue
		}

		if msg.Type != "user_message" {
			send(map[string]interface{}{"type": "error", "error": "expected user_message"})
			continue
		}
		if msg.Text == "" && len(msg.Images) == 0 && len(msg.Files) == 0 {
			send(map[string]interface{}{"type": "error", "error": "message must contain text, images, or files"})
			continue
		}

		// Build content blocks
		content := []provider.ContentBlock{}
		if msg.Text != "" {
			content = append(content, provider.TextBlock(msg.Text))
		}
		for _, img := range msg.Images {
			if img.MIME == "" || img.Data == "" {
				continue
			}
			content = append(content, provider.ImageBlock(img.MIME, img.Data))
		}
		for _, f := range msg.Files {
			if f.Name == "" || f.Data == "" {
				continue
			}
			decoded, err := base64.StdEncoding.DecodeString(f.Data)
			if err != nil {
				send(map[string]interface{}{"type": "error", "error": fmt.Sprintf("invalid base64 for file %s", f.Name)})
				continue
			}
			fileText := fmt.Sprintf("--- File: %s ---\n%s\n--- End of %s ---", f.Name, string(decoded), f.Name)
			content = append(content, provider.TextBlock(fileText))
		}

		// Send user acknowledgment with attachment info
		ackExtras := map[string]interface{}{"type": "user_ack", "text": msg.Text}
		if len(msg.Images) > 0 {
			ackExtras["image_count"] = len(msg.Images)
		}
		if len(msg.Files) > 0 {
			fileNames := make([]string, len(msg.Files))
			for i, f := range msg.Files {
				fileNames[i] = f.Name
			}
			ackExtras["file_names"] = fileNames
		}
		send(ackExtras)

		// Route through bridge or direct agent
		if s.chatBridge != nil {
			s.chatBridge.SendUserMessage(content)
		} else {
			// Legacy mode: directly run agent
			if !s.agentBusy.CompareAndSwap(false, true) {
				send(map[string]interface{}{"type": "error", "error": "agent is busy processing another request, please wait"})
				continue
			}
			s.agentMu.Lock()
			ctx, cancel := context.WithCancel(r.Context())
			done := make(chan struct{})
			go func() {
				for {
					_, _, err := conn.ReadMessage()
					if err != nil {
						cancel()
						return
					}
				}
			}()
			go func() {
				defer close(done)
				err := s.agent.RunStreamWithContent(ctx, content, func(event provider.StreamEvent) {
					send(streamEventToJSON(event))
				})
				if err != nil && ctx.Err() == nil {
					send(map[string]interface{}{"type": "error", "error": err.Error()})
				}
			}()
			<-done
			cancel()
			s.agentMu.Unlock()
			s.agentBusy.Store(false)
		}
	}
}

// streamEventToJSON converts a StreamEvent to a JSON-serializable map.
func streamEventToJSON(event provider.StreamEvent) map[string]interface{} {
	switch event.Type {
	case provider.StreamEventText:
		return map[string]interface{}{"type": "text_delta", "text": event.Text}
	case provider.StreamEventToolCallChunk:
		return map[string]interface{}{
			"type": "tool_call_chunk", "id": event.Tool.ID,
			"name": event.Tool.Name, "arguments": string(event.Tool.Arguments),
		}
	case provider.StreamEventToolCallDone:
		return map[string]interface{}{
			"type": "tool_call", "id": event.Tool.ID,
			"name": event.Tool.Name, "arguments": string(event.Tool.Arguments),
		}
	case provider.StreamEventToolResult:
		return map[string]interface{}{
			"type": "tool_result", "name": event.Tool.Name,
			"result": event.Result, "is_error": event.IsError,
		}
	case provider.StreamEventDone:
		doneMsg := map[string]interface{}{"type": "done"}
		if event.Usage != nil {
			doneMsg["usage"] = map[string]interface{}{
				"input_tokens":  event.Usage.InputTokens,
				"output_tokens": event.Usage.OutputTokens,
			}
		}
		return doneMsg
	case provider.StreamEventError:
		errMsg := "unknown error"
		if event.Error != nil {
			errMsg = event.Error.Error()
		}
		return map[string]interface{}{"type": "error", "error": errMsg}
	default:
		return nil
	}
}

func (s *Server) wsSend(conn *websocket.Conn, msg interface{}) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := conn.WriteJSON(msg); err != nil {
		debug.Log("webui", "ws write error: %v", err)
	}
}

func (s *Server) wsSendError(conn *websocket.Conn, errMsg string) {
	s.wsSend(conn, map[string]interface{}{"type": "error", "error": errMsg})
}

// wsSendEvent is used in legacy (non-bridge) mode only.
func (s *Server) wsSendEvent(conn *websocket.Conn, event provider.StreamEvent) {
	msg := streamEventToJSON(event)
	if msg != nil {
		s.wsSend(conn, msg)
	}
}

// a2aAuthPresets returns the built-in OAuth2/OIDC provider presets for the webui.
// The frontend uses these to auto-fill issuer_url, scopes, and flow when the user
// selects a provider preset.
func a2aAuthPresets() map[string]interface{} {
	out := make(map[string]interface{}, len(auth.ProviderPresets))
	for name, p := range auth.ProviderPresets {
		out[name] = map[string]interface{}{
			"name":              p.Name,
			"authorize_url":     p.AuthorizeURL,
			"token_url":         p.TokenURL,
			"device_auth_url":   p.DeviceAuthURL,
			"oidc_discovery":    p.OIDCDiscovery,
			"default_scopes":    p.DefaultScopes,
			"default_client_id": p.DefaultClientID,
			"supports_pkce":     p.SupportsPKCE,
			"supports_device":   p.SupportsDevice,
		}
	}
	return out
}

func sanitizeOAuth2(o *config.A2AOAuth2Config) interface{} {
	if o == nil {
		return nil
	}
	return map[string]interface{}{
		"provider":   o.Provider,
		"client_id":  o.ClientID,
		"has_secret": strings.TrimSpace(o.ClientSecret) != "",
		"issuer_url": o.IssuerURL,
		"scopes":     o.Scopes,
		"flow":       o.Flow,
	}
}

func sanitizeOIDC(o *config.A2AOIDCConfig) interface{} {
	if o == nil {
		return nil
	}
	return map[string]interface{}{
		"provider":   o.Provider,
		"client_id":  o.ClientID,
		"has_secret": strings.TrimSpace(o.ClientSecret) != "",
		"issuer_url": o.IssuerURL,
		"scopes":     o.Scopes,
		"flow":       o.Flow,
	}
}

func sanitizeMTLS(m *config.A2AMTLSConfig) interface{} {
	if m == nil {
		return nil
	}
	return map[string]interface{}{
		"cert_file": m.CertFile,
		"key_file":  m.KeyFile,
		"ca_file":   m.CAFile,
	}
}

// GET/PUT /api/general — general settings (language, mode, max_iterations, allowed_dirs)
func (s *Server) handleGeneral(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.mu.RLock()
		defer s.mu.RUnlock()
		writeJSON(w, map[string]interface{}{
			"language":       s.cfg.Language,
			"default_mode":   s.cfg.DefaultMode,
			"max_iterations": s.cfg.MaxIterations,
			"allowed_dirs":   s.cfg.AllowedDirs,
			"system_prompt":  s.cfg.SystemPrompt,
		})
	case http.MethodPut:
		var req struct {
			Language      string   `json:"language"`
			DefaultMode   string   `json:"default_mode"`
			MaxIterations int      `json:"max_iterations"`
			AllowedDirs   []string `json:"allowed_dirs"`
			SystemPrompt  string   `json:"system_prompt"`
		}
		if err := readJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if req.Language != "" {
			s.cfg.Language = req.Language
		}
		if req.DefaultMode != "" {
			s.cfg.DefaultMode = req.DefaultMode
		}
		s.cfg.MaxIterations = req.MaxIterations
		if req.AllowedDirs != nil {
			s.cfg.AllowedDirs = req.AllowedDirs
		}
		if req.SystemPrompt != "" {
			if req.SystemPrompt == "__reset__" {
				s.cfg.SystemPrompt = config.DefaultSystemPrompt
			} else {
				s.cfg.SystemPrompt = req.SystemPrompt
			}
		}
		if err := s.saveConfig(); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, map[string]string{"status": "ok"})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// POST /api/restart — trigger application restart
func (s *Server) handleRestart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, map[string]string{"status": "restarting"})
	// Trigger async so the response can be sent first
	go func() {
		time.Sleep(500 * time.Millisecond)
		if s.restartFn != nil {
			s.restartFn()
		}
	}()
}

// --- Helpers ---

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func readJSON(r *http.Request, v interface{}) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}

// sanitizeConfigForAPI returns a JSON-safe copy with API keys masked.
func sanitizeConfigForAPI(cfg *config.Config) map[string]interface{} {
	return map[string]interface{}{
		"vendor":         cfg.Vendor,
		"endpoint":       cfg.Endpoint,
		"model":          cfg.Model,
		"language":       cfg.Language,
		"default_mode":   cfg.DefaultMode,
		"max_iterations": cfg.MaxIterations,
		"allowed_dirs":   cfg.AllowedDirs,
		"system_prompt":  cfg.SystemPrompt,
		"im":             cfg.IM,
		"mcp_servers":    cfg.MCPServers,
		"vendors":        cfg.Vendors,
		"a2a": map[string]interface{}{
			"disabled": cfg.A2A.Disabled,
			"port":     cfg.A2A.Port,
			"host":     cfg.A2A.Host,
		},
	}
}
