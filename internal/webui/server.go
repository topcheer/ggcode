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
)

// Server provides a built-in WebUI for configuration and chat.
type Server struct {
	cfg      *config.Config
	mu       sync.RWMutex
	addr     string
	listener net.Listener
	mux      *http.ServeMux
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
	s.mux.HandleFunc("/api/mcp/", s.handleMCPDetail)
	s.mux.HandleFunc("/api/im", s.handleIM)
	s.mux.HandleFunc("/api/general", s.handleGeneral)
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
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
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
}

// GET /api/vendors/{vendor} — vendor detail
func (s *Server) handleVendorDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	vendor := strings.TrimPrefix(r.URL.Path, "/api/vendors/")
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
}

// GET /api/vendors/{vendor}/endpoints — list endpoints
func (s *Server) handleEndpoints(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	vendor := r.PathValue("vendor")
	s.mu.RLock()
	defer s.mu.RUnlock()

	vc, ok := s.cfg.Vendors[vendor]
	if !ok {
		writeError(w, http.StatusNotFound, "vendor not found")
		return
	}
	writeJSON(w, vc.Endpoints)
}

// GET/PUT /api/vendors/{vendor}/endpoints/{endpoint}
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
		defer s.mu.RUnlock()
		writeJSON(w, s.cfg.MCPServers)

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
