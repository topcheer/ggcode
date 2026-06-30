package webui

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/safego"
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

// KnightStatusFunc returns current Knight agent status and skill data.
type KnightStatusFunc func() KnightStatus

// KnightActionFunc performs an action on a Knight skill. Returns an error message or empty string on success.
type KnightActionFunc func(action, skillName string, params map[string]interface{}) error

// KnightSkillContentFunc reads the raw content of a skill file by name and staging flag.
type KnightSkillContentFunc func(name string, staging bool) (string, error)

// RuntimeStatusFunc returns live runtime state for the /api/status endpoint.
type RuntimeStatusFunc func() RuntimeStatus

// RuntimeStatus is the JSON response for /api/status — external process visibility.
type RuntimeStatus struct {
	PID            int             `json:"pid"`
	Workspace      string          `json:"workspace"`
	AgentBusy      bool            `json:"agent_busy"`
	PermissionMode string          `json:"permission_mode"`
	Vendor         string          `json:"vendor"`
	Endpoint       string          `json:"endpoint"`
	Model          string          `json:"model"`
	Language       string          `json:"language"`
	IMAdapters     []IMAdapterInfo `json:"im_adapters"`
	MobileConn     MobileConnInfo  `json:"mobile"`
}

// IMAdapterInfo describes one IM adapter's status.
type IMAdapterInfo struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Online  bool   `json:"online"`
	Muted   bool   `json:"muted"`
	Channel string `json:"channel,omitempty"`
}

// MobileConnInfo describes the mobile tunnel connection status.
type MobileConnInfo struct {
	Connected   bool   `json:"connected"`
	RelayURL    string `json:"relay_url,omitempty"`
	SessionID   string `json:"session_id,omitempty"`
	ConnectCode string `json:"connect_code,omitempty"`
}

// KnightStatus holds all Knight state exposed via the WebUI API.
type KnightStatus struct {
	Enabled bool              `json:"enabled"`
	Running bool              `json:"running"`
	Status  string            `json:"status"` // human-readable status string
	Budget  KnightBudget      `json:"budget"`
	Active  []KnightSkill     `json:"active"`
	Staging []KnightSkill     `json:"staging"`
	Queue   []KnightCandidate `json:"queue"`
}

type KnightBudget struct {
	Used      int `json:"used"`
	Remaining int `json:"remaining"`
	Limit     int `json:"limit"`
}

type KnightSkill struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Scope       string   `json:"scope"`
	Staging     bool     `json:"staging"`
	CreatedBy   string   `json:"created_by"`
	UsageCount  int      `json:"usage_count"`
	Frozen      bool     `json:"frozen"`
	Platforms   []string `json:"platforms,omitempty"`
	Path        string   `json:"path,omitempty"`
	CreatedAt   string   `json:"created_at,omitempty"`
	UpdatedAt   string   `json:"updated_at,omitempty"`
}

type KnightCandidate struct {
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	Category       string   `json:"category"`
	Score          float64  `json:"score"`
	EvidenceCount  int      `json:"evidence_count"`
	Reason         string   `json:"reason,omitempty"`
	SourceSessions []string `json:"source_sessions,omitempty"`
}

// A2ADiscoveredInstance describes a remote ggcode instance discovered via A2A.
type A2ADiscoveredInstance struct {
	ID        string `json:"id"`
	Workspace string `json:"workspace"`
	Endpoint  string `json:"endpoint"`
	Status    string `json:"status"`
	StartedAt string `json:"started_at"`
}

// ChatBridge is the interface between webui and the agent.
// Implemented by im.DaemonBridge -- webchat sends messages through the
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

// AgentRunner is kept for backward compatibility -- ChatBridge replaces it.
type AgentRunner interface {
	RunStream(ctx context.Context, userMsg string, onEvent func(provider.StreamEvent)) error
	RunStreamWithContent(ctx context.Context, content []provider.ContentBlock, onEvent func(provider.StreamEvent)) error
	Messages() []provider.Message
}

// Server provides a built-in WebUI for configuration and chat.
type Server struct {
	cfg             *config.Config
	authToken       string // random token generated at startup
	mcpStatusFn     MCPStatusFunc
	imStatusFn      IMStatusFunc
	imActionFn      IMActionFunc
	restartFn       func()                 // called when user triggers restart from WebUI
	a2aDiscoverFn   A2ADiscoverFunc        // returns discovered A2A instances
	knightStatusFn  KnightStatusFunc       // returns Knight agent status
	knightActionFn  KnightActionFunc       // performs actions on Knight skills
	knightContentFn KnightSkillContentFunc // reads skill file content
	statusFn        RuntimeStatusFunc      // returns live runtime state for /api/status
	sessionStore    session.Store
	workspace       string // current ggcode working directory
	saveScope       string // "global" or "instance" -- where config saves go
	chatBridge      ChatBridge
	agent           AgentRunner // legacy, for non-bridge setups
	agentMu         sync.Mutex
	agentBusy       atomic.Bool
	mu              sync.RWMutex
	addr            string
	listener        net.Listener
	mux             *http.ServeMux
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

// SetKnightStatusFn sets the callback for Knight agent status.
func (s *Server) SetKnightStatusFn(fn KnightStatusFunc) {
	s.knightStatusFn = fn
}

// SetKnightActionFn sets the callback for Knight skill actions.
func (s *Server) SetKnightActionFn(fn KnightActionFunc) {
	s.knightActionFn = fn
}

// SetKnightSkillContentFn sets the callback for reading skill file content.
func (s *Server) SetKnightSkillContentFn(fn KnightSkillContentFunc) {
	s.knightContentFn = fn
}

// SetStatusFn sets the runtime status provider for /api/status.
func (s *Server) SetStatusFn(fn RuntimeStatusFunc) {
	s.statusFn = fn
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
		authToken: generateAuthToken(),
	}
	s.routes()
	return s
}

// Token returns the server's auth token for display in the TUI/daemon.
func (s *Server) Token() string { return s.authToken }

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

	safego.Go("webui.httpServe", func() {
		_ = http.Serve(ln, s.mux)
	})

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
	// Static SPA (no auth required -- serves static HTML/JS)
	s.mux.HandleFunc("/", s.serveSPA)

	// All API endpoints require auth token via Bearer header or ?token= query param.
	a := s.requireAuth
	s.mux.HandleFunc("/api/config", a(s.handleConfig))
	s.mux.HandleFunc("/api/config/scope", a(s.handleConfigScope))
	s.mux.HandleFunc("/api/config/active", a(s.handleActiveSelection))
	s.mux.HandleFunc("/api/vendors", a(s.handleVendors))
	s.mux.HandleFunc("/api/vendors/", a(s.handleVendorDetail))
	s.mux.HandleFunc("/api/vendors/{vendor}/endpoints", a(s.handleEndpoints))
	s.mux.HandleFunc("/api/vendors/{vendor}/endpoints/{endpoint}", a(s.handleEndpointDetail))
	s.mux.HandleFunc("/api/vendors/{vendor}/endpoints/{endpoint}/apikey", a(s.handleAPIKey))
	s.mux.HandleFunc("/api/mcp", a(s.handleMCP))
	s.mux.HandleFunc("/api/mcp/status", a(s.handleMCPStatus))
	s.mux.HandleFunc("/api/mcp/", a(s.handleMCPDetail))
	s.mux.HandleFunc("/api/im", a(s.handleIM))
	s.mux.HandleFunc("/api/im/status", a(s.handleIMStatus))
	s.mux.HandleFunc("/api/im/action", a(s.handleIMAction))
	s.mux.HandleFunc("/api/im/adapters", a(s.handleIMAdapters))
	s.mux.HandleFunc("/api/im/adapters/", a(s.handleIMAdapterDetail))
	s.mux.HandleFunc("/api/general", a(s.handleGeneral))
	s.mux.HandleFunc("/api/impersonate", a(s.handleImpersonate))
	s.mux.HandleFunc("/api/a2a", a(s.handleA2A))
	s.mux.HandleFunc("/api/a2a/discover", a(s.handleA2ADiscover))
	s.mux.HandleFunc("/api/sessions", a(s.handleSessions))
	s.mux.HandleFunc("/api/sessions/", a(s.handleSessionDetail))
	s.mux.HandleFunc("/api/chat/ws", a(s.handleChatWS))
	s.mux.HandleFunc("/api/chat/history", a(s.handleChatHistory))
	s.mux.HandleFunc("/api/restart", a(s.handleRestart))
	s.mux.HandleFunc("/api/knight", a(s.handleKnight))
	s.mux.HandleFunc("/api/knight/skills", a(s.handleKnightSkills))
	s.mux.HandleFunc("/api/knight/action", a(s.handleKnightAction))
	s.mux.HandleFunc("/api/knight/skill-content", a(s.handleKnightSkillContent))
	s.mux.HandleFunc("/api/status", a(s.handleStatus))
}

// handleStatus returns live runtime state for external monitoring.
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.statusFn != nil {
		writeJSON(w, s.statusFn())
		return
	}
	// Fallback: return what we know from the server itself
	writeJSON(w, RuntimeStatus{
		PID:       0,
		Workspace: s.workspace,
	})
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

// sanitizeConfigForAPI returns a JSON-safe copy with sensitive fields masked.
// API keys, OAuth secrets, MCP env/headers are replaced with has_* booleans.
func sanitizeConfigForAPI(cfg *config.Config) map[string]interface{} {
	// Marshal and unmarshal through JSON to get generic maps for mutation.
	raw, _ := json.Marshal(map[string]interface{}{
		"vendor":         cfg.Vendor,
		"endpoint":       cfg.Endpoint,
		"model":          cfg.Model,
		"language":       cfg.Language,
		"default_mode":   cfg.DefaultMode,
		"max_iterations": cfg.MaxIterations,
		"allowed_dirs":   cfg.AllowedDirs,
		"extra_prompt":   cfg.ExtraPrompt,
		"im":             cfg.IM,
		"mcp_servers":    cfg.MCPServers,
		"vendors":        cfg.Vendors,
		"a2a": map[string]interface{}{
			"disabled": cfg.A2A.Disabled,
			"port":     cfg.A2A.Port,
			"host":     cfg.A2A.Host,
		},
	})

	var result map[string]interface{}
	_ = json.Unmarshal(raw, &result)

	sanitizeMap(result)
	return result
}

// sanitizeMap recursively removes sensitive fields from a config map.
func sanitizeMap(m map[string]interface{}) {
	for key, val := range m {
		switch key {
		case "api_key", "api_secret", "oauth_client_secret":
			if str, ok := val.(string); ok && str != "" {
				m[key] = "***"
			} else {
				delete(m, key)
			}
		case "env", "headers":
			if val != nil {
				m["has_"+key] = true
			}
			delete(m, key)
		default:
			switch v := val.(type) {
			case map[string]interface{}:
				sanitizeMap(v)
			case []interface{}:
				for _, item := range v {
					if sub, ok := item.(map[string]interface{}); ok {
						sanitizeMap(sub)
					}
				}
			}
		}
	}
}
