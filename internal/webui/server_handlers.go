package webui

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/auth"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/safego"
)

// --- Config API ---

// GET /api/config -- full config as JSON (includes scope info)
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

// GET/PUT /api/config/scope -- read/switch config save scope
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

// GET/PUT /api/config/active -- read/switch active vendor+endpoint+model
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

// GET /api/vendors -- list all vendor names
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
		defer s.mu.RUnlock()
		cfg := s.cfg

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

// GET /api/mcp/status -- runtime MCP status only
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
	if err := s.cfg.SaveMCPServersScoped(s.saveScope); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]string{"status": "deleted"})
}

// GET/PUT /api/im -- IM config
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

// GET /api/im/status -- runtime IM adapter status with bindings
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

// POST /api/im/action -- perform IM action (enable/disable/mute/unmute)
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

// GET /api/im/adapters -- list all IM adapters
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

// GET/PUT /api/impersonate -- read/update impersonation settings
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

// GET/PUT /api/a2a -- A2A protocol configuration
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
				"has_api_key":   strings.TrimSpace(a2a.Auth.APIKey) != "",
				"has_api_keys":  len(a2a.Auth.APIKeys) > 0,
				"api_key_count": len(a2a.Auth.APIKeys),
				"oauth2":        sanitizeOAuth2(a2a.Auth.OAuth2),
				"oidc":          sanitizeOIDC(a2a.Auth.OIDC),
				"mtls":          sanitizeMTLS(a2a.Auth.MTLS),
			},
			"presets": a2aAuthPresets(),
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

// GET /api/a2a/discover -- list discovered A2A instances
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

// GET /api/sessions -- list sessions grouped by workspace
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

// GET /api/sessions/{id} -- get session detail with messages
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
		// Skip system messages -- they contain internal prompts and should
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

// GET/PUT /api/general -- general settings (language, mode, max_iterations, allowed_dirs)
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
			"extra_prompt":   s.cfg.ExtraPrompt,
		})
	case http.MethodPut:
		var req struct {
			Language      string   `json:"language"`
			DefaultMode   string   `json:"default_mode"`
			MaxIterations int      `json:"max_iterations"`
			AllowedDirs   []string `json:"allowed_dirs"`
			ExtraPrompt   string   `json:"extra_prompt"`
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
		if req.ExtraPrompt != "" {
			if req.ExtraPrompt == "__reset__" {
				s.cfg.ExtraPrompt = ""
			} else {
				s.cfg.ExtraPrompt = req.ExtraPrompt
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

// POST /api/restart -- trigger application restart
func (s *Server) handleRestart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, map[string]string{"status": "restarting"})
	// Trigger async so the response can be sent first
	safego.Go("webui.restart", func() {
		time.Sleep(500 * time.Millisecond)
		if s.restartFn != nil {
			s.restartFn()
		}
	})
}

// handleKnight returns Knight agent status and all skill data.
func (s *Server) handleKnight(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.knightStatusFn == nil {
		writeJSON(w, KnightStatus{Enabled: false, Status: "unavailable"})
		return
	}
	writeJSON(w, s.knightStatusFn())
}

// handleKnightSkills returns only the active and staging skills (lighter endpoint).
func (s *Server) handleKnightSkills(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.knightStatusFn == nil {
		writeJSON(w, map[string]interface{}{"active": []interface{}{}, "staging": []interface{}{}})
		return
	}
	status := s.knightStatusFn()
	writeJSON(w, map[string]interface{}{
		"active":  status.Active,
		"staging": status.Staging,
	})
}

// handleKnightAction performs skill lifecycle actions.
// POST with JSON body: {"action": "...", "name": "skill-name", ...params}
//
// Supported actions:
//   - promote: promote a staging skill to active
//   - reject: reject (delete) a staging skill
//   - freeze: freeze an active skill (disable auto-updates)
//   - unfreeze: unfreeze an active skill
//   - rollback: rollback a skill to its previous version
//   - record_effectiveness: record effectiveness score (params: {"score": 1-5})
//   - analyze: trigger immediate session analysis
//   - validate: trigger immediate skill validation
//   - delete_queue: remove a candidate from the queue (params: {"name": "..."})
func (s *Server) handleKnightAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.knightActionFn == nil {
		writeError(w, http.StatusServiceUnavailable, "Knight not available")
		return
	}
	var req struct {
		Action string                 `json:"action"`
		Name   string                 `json:"name"`
		Params map[string]interface{} `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Action == "" {
		writeError(w, http.StatusBadRequest, "missing 'action' field")
		return
	}

	err := s.knightActionFn(req.Action, req.Name, req.Params)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

// handleKnightSkillContent returns the raw markdown content of a skill file.
// GET with query params: ?name=skill-name&staging=true
func (s *Server) handleKnightSkillContent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.knightContentFn == nil {
		writeError(w, http.StatusServiceUnavailable, "Knight not available")
		return
	}
	name := r.URL.Query().Get("name")
	staging := r.URL.Query().Get("staging") == "true"
	if name == "" {
		writeError(w, http.StatusBadRequest, "missing 'name' query parameter")
		return
	}
	content, err := s.knightContentFn(name, staging)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, map[string]string{"content": content})
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
