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
	"github.com/topcheer/ggcode/internal/secretfield"
)

func sanitizeEndpoints(endpoints map[string]config.EndpointConfig) map[string]interface{} {
	out := make(map[string]interface{}, len(endpoints))
	for name, ep := range endpoints {
		entry := map[string]interface{}{
			"base_url":    ep.BaseURL,
			"protocol":    ep.Protocol,
			"has_api_key": strings.TrimSpace(ep.APIKey) != "",
		}
		if ep.Models != nil {
			entry["models"] = ep.Models
		}
		out[name] = entry
	}
	return out
}

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
			"endpoints":    sanitizeEndpoints(vc.Endpoints),
		})

	case http.MethodPut:
		// #5294: pointer fields so an ABSENT key means "keep existing" -
		// with a plain string, an omitted api_key decoded as "" and any
		// non-"__unchanged__" value (including "") destroyed the stored
		// key. Now: nil = keep; non-nil = explicit set (empty string =
		// intentional clear); "__unchanged__" kept for old clients.
		var req struct {
			DisplayName *string `json:"display_name"`
			APIKey      *string `json:"api_key"`
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
		if req.DisplayName != nil && *req.DisplayName != "" {
			vc.DisplayName = *req.DisplayName
		}

		if req.APIKey != nil && *req.APIKey != "__unchanged__" {
			if err := s.cfg.SetVendorAPIKey(vendor, *req.APIKey); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}

			vc = s.cfg.Vendors[vendor]
			if req.DisplayName != nil && *req.DisplayName != "" {
				vc.DisplayName = *req.DisplayName
			}
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
		writeJSON(w, sanitizeEndpoints(vc.Endpoints))

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

		if raw, err := json.Marshal(ep); err == nil {
			var m map[string]interface{}
			if json.Unmarshal(raw, &m) == nil {
				sanitizeMap(m)
				writeJSON(w, m)
				return
			}
		}
		writeJSON(w, ep)

	case http.MethodPut:
		// #5293: full-struct replace destroyed fields the client did not
		// project (api_key and ~20 others were zeroed on every save from
		// a partial GET projection). Decode the body as a presence map
		// and patch only keys the client actually sent onto the existing
		// endpoint; absent keys keep their stored values.
		var raw map[string]json.RawMessage
		if err := readJSON(r, &raw); err != nil {
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
		ep := vc.Endpoints[endpoint]
		patchStr := func(dst *string, key string) {
			if v, ok := raw[key]; ok {
				var sv string
				_ = json.Unmarshal(v, &sv)
				*dst = sv
			}
		}
		patchInt := func(dst *int, key string) {
			if v, ok := raw[key]; ok {
				var n int
				_ = json.Unmarshal(v, &n)
				*dst = n
			}
		}
		patchBool := func(dst *bool, key string) {
			if v, ok := raw[key]; ok {
				var b bool
				_ = json.Unmarshal(v, &b)
				*dst = b
			}
		}
		patchStr(&ep.DisplayName, "display_name")
		patchStr(&ep.Protocol, "protocol")
		patchStr(&ep.BaseURL, "base_url")
		patchStr(&ep.AuthType, "auth_type")
		patchStr(&ep.APIKey, "api_key")
		patchStr(&ep.ReasoningEffort, "reasoning_effort")
		patchStr(&ep.TextVerbosity, "text_verbosity")
		patchStr(&ep.ToolChoice, "tool_choice")
		patchStr(&ep.ThinkingMode, "thinking_mode")
		patchStr(&ep.DefaultModel, "default_model")
		patchStr(&ep.SelectedModel, "selected_model")
		patchStr(&ep.ContextEditing, "context_editing")
		patchStr(&ep.ServiceTier, "service_tier")
		patchInt(&ep.ContextWindow, "context_window")
		patchInt(&ep.MaxTokens, "max_tokens")
		patchInt(&ep.MaxRetries, "max_retries")
		patchBool(&ep.Logprobs, "logprobs")
		patchBool(&ep.MemoryTool, "memory_tool")
		patchBool(&ep.ResponsesBackground, "responses_background")
		if v, ok := raw["request_timeout"]; ok {
			var d int64
			_ = json.Unmarshal(v, &d)
			ep.RequestTimeout = time.Duration(d)
		}
		if v, ok := raw["supports_vision"]; ok {
			var b *bool
			_ = json.Unmarshal(v, &b)
			ep.SupportsVision = b
		}
		if v, ok := raw["strict_tools"]; ok {
			var b *bool
			_ = json.Unmarshal(v, &b)
			ep.StrictTools = b
		}
		patchStrings := func(dst *[]string, key string) {
			if v, ok := raw[key]; ok {
				var ss []string
				_ = json.Unmarshal(v, &ss)
				*dst = ss
			}
		}
		patchStrings(&ep.Models, "models")
		patchStrings(&ep.Tags, "tags")
		patchStrings(&ep.StrictToolsAllow, "strict_tools_allow")
		if v, ok := raw["server_tools"]; ok {
			var st []config.ServerToolConfig
			_ = json.Unmarshal(v, &st)
			ep.ServerTools = st
		}
		if v, ok := raw["model_limits"]; ok {
			var ml map[string]config.ModelLimitConfig
			_ = json.Unmarshal(v, &ml)
			ep.ModelLimits = ml
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

func (s *Server) handleMCP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.mu.RLock()
		defer s.mu.RUnlock()
		cfg := s.cfg

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

		if raw, err := json.Marshal(entries); err == nil {
			var arr []interface{}
			if json.Unmarshal(raw, &arr) == nil {
				for _, item := range arr {
					if sub, ok := item.(map[string]interface{}); ok {
						sanitizeMap(sub)
					}
				}
				writeJSON(w, arr)
				return
			}
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

func (s *Server) handleMCPStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, s.getRuntimeMCPStatus())
}

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

func maskIMConfig(cfg config.IMConfig) config.IMConfig {
	out := cfg
	out.Adapters = make(map[string]config.IMAdapterConfig, len(cfg.Adapters))
	for name, a := range cfg.Adapters {
		if len(a.Env) > 0 {
			a.Env = map[string]string{"__masked__": ""}
		}
		if len(a.Extra) > 0 {
			masked := make(map[string]interface{}, len(a.Extra))
			for k, v := range a.Extra {
				masked[k] = maskExtraValue(k, v)
			}
			a.Extra = masked
		}
		out.Adapters[name] = a
	}
	return out
}

func maskIMAdapter(a config.IMAdapterConfig) config.IMAdapterConfig {
	return maskIMConfig(config.IMConfig{Adapters: map[string]config.IMAdapterConfig{"": a}}).Adapters[""]
}

func sensitiveExtraKey(key string) bool {
	return secretfield.LooksLikeSecretField(key)
}

func maskExtraValue(key string, v interface{}) interface{} {
	switch val := v.(type) {
	case string:
		if val != "" && sensitiveExtraKey(key) {
			return "__unchanged__"
		}
		return val
	case map[string]interface{}:
		out := make(map[string]interface{}, len(val))
		for kk, vv := range val {
			out[kk] = maskExtraValue(kk, vv)
		}
		return out
	default:
		return v
	}
}

func restoreMaskedIMCredentials(prev, next config.IMConfig) config.IMConfig {
	for name, a := range next.Adapters {
		old, ok := prev.Adapters[name]
		if !ok {
			continue
		}
		if _, masked := a.Env["__masked__"]; masked && len(old.Env) > 0 {
			a.Env = old.Env
		}
		for k, v := range a.Extra {
			a.Extra[k] = restoreExtraValue(old.Extra[k], v)
		}
		next.Adapters[name] = a
	}
	return next
}

func restoreExtraValue(oldV, newV interface{}) interface{} {
	if s, ok := newV.(string); ok && s == "__unchanged__" {
		return oldV
	}
	newMap, ok := newV.(map[string]interface{})
	if !ok {
		return newV
	}
	oldMap, _ := oldV.(map[string]interface{})
	out := make(map[string]interface{}, len(newMap))
	for k, v := range newMap {
		var ov interface{}
		if oldMap != nil {
			ov = oldMap[k]
		}
		out[k] = restoreExtraValue(ov, v)
	}
	return out
}

func (s *Server) handleIM(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.mu.RLock()
		defer s.mu.RUnlock()
		writeJSON(w, maskIMConfig(s.cfg.IM))
	case http.MethodPut:
		var im config.IMConfig
		if err := readJSON(r, &im); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		s.cfg.IM = restoreMaskedIMCredentials(s.cfg.IM, im)
		if err := s.saveConfig(); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, im)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

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

func (s *Server) handleIMAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Adapter string `json:"adapter"`
		Action  string `json:"action"`
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

func (s *Server) handleIMAdapters(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	writeJSON(w, maskIMConfig(s.cfg.IM).Adapters)
}

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
		writeJSON(w, maskIMAdapter(adapter))

	case http.MethodPost, http.MethodPut:
		var adapter config.IMAdapterConfig
		if err := readJSON(r, &adapter); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if prev, ok := s.cfg.IM.Adapters[name]; ok {

			single := config.IMConfig{Adapters: map[string]config.IMAdapterConfig{name: prev}}
			wrapped := config.IMConfig{Adapters: map[string]config.IMAdapterConfig{name: adapter}}
			adapter = restoreMaskedIMCredentials(single, wrapped).Adapters[name]
		}
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

		var presetPtr *provider.ImpersonationPreset
		if req.Preset != "" && req.Preset != "none" {
			presetPtr = provider.FindPresetByID(req.Preset)
			if presetPtr == nil {
				writeError(w, http.StatusBadRequest, "unknown preset: "+req.Preset)
				return
			}
		}
		provider.SetActiveImpersonation(presetPtr, req.CustomVersion, req.CustomHeaders)

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
			MaxIterations *int     `json:"max_iterations"`
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

		if req.MaxIterations != nil {
			s.cfg.MaxIterations = *req.MaxIterations
		}
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

func (s *Server) handleRestart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, map[string]string{"status": "restarting"})

	safego.Go("webui.restart", func() {
		time.Sleep(500 * time.Millisecond)
		if s.restartFn != nil {
			s.restartFn()
		}
	})
}

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
