package webui

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
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

// Server provides a built-in WebUI for configuration and chat.
type Server struct {
	cfg         *config.Config
	mcpStatusFn MCPStatusFunc
	imStatusFn  IMStatusFunc
	imActionFn  IMActionFunc
	mu          sync.RWMutex
	addr        string
	listener    net.Listener
	mux         *http.ServeMux
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

// NewServer creates a WebUI server bound to the given config.
func NewServer(cfg *config.Config) *Server {
	s := &Server{
		cfg: cfg,
		mux: http.NewServeMux(),
	}
	s.routes()
	return s
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

// GET /api/config — full config as JSON
func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	sanitized := sanitizeConfigForAPI(s.cfg)
	writeJSON(w, sanitized)
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
		if err := s.cfg.Save(); err != nil {
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
		if err := s.cfg.Save(); err != nil {
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
		if err := s.cfg.Save(); err != nil {
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
		if err := s.cfg.Save(); err != nil {
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
		if err := s.cfg.Save(); err != nil {
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
		if err := s.cfg.Save(); err != nil {
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
		if err := s.cfg.Save(); err != nil {
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
	if err := s.cfg.Save(); err != nil {
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
		if err := s.cfg.Save(); err != nil {
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
	if err := s.cfg.Save(); err != nil {
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
		if err := s.cfg.Save(); err != nil {
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
		if err := s.cfg.Save(); err != nil {
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
		if err := s.cfg.Save(); err != nil {
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
			s.cfg.SystemPrompt = req.SystemPrompt
		}
		if err := s.cfg.Save(); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, map[string]string{"status": "ok"})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
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
		"im":             cfg.IM,
		"mcp_servers":    cfg.MCPServers,
		"vendors":        cfg.Vendors,
	}
}
