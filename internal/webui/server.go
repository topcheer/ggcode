package webui

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/session"
)

type MCPStatusFunc func() map[string]MCPRuntimeStatus

type MCPRuntimeStatus struct {
	Connected bool     `json:"connected"`
	Pending   bool     `json:"pending"`
	Disabled  bool     `json:"disabled"`
	Error     string   `json:"error,omitempty"`
	Tools     []string `json:"tools,omitempty"`
}

type IMStatusFunc func() []IMRuntimeStatus

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
	AllDirs   []string `json:"all_dirs,omitempty"`
}

type IMActionFunc func(adapter string, action string) error

type A2ADiscoverFunc func() []A2ADiscoveredInstance

type KnightStatusFunc func() KnightStatus

type KnightActionFunc func(action, skillName string, params map[string]interface{}) error

type KnightSkillContentFunc func(name string, staging bool) (string, error)

type RuntimeStatusFunc func() RuntimeStatus

type RuntimeStatus struct {
	PID            int             `json:"pid"`
	Workspace      string          `json:"workspace"`
	AgentBusy      bool            `json:"agent_busy"`
	PermissionMode string          `json:"permission_mode"`
	Vendor         string          `json:"vendor"`
	Endpoint       string          `json:"endpoint"`
	Model          string          `json:"model"`
	Language       string          `json:"language"`
	ContextWindow  int             `json:"context_window"`
	MaxTokens      int             `json:"max_tokens"`
	IMAdapters     []IMAdapterInfo `json:"im_adapters"`
	MobileConn     MobileConnInfo  `json:"mobile"`
}

type IMAdapterInfo struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Online  bool   `json:"online"`
	Muted   bool   `json:"muted"`
	Channel string `json:"channel,omitempty"`
}

type MobileConnInfo struct {
	Connected   bool   `json:"connected"`
	RelayURL    string `json:"relay_url,omitempty"`
	SessionID   string `json:"session_id,omitempty"`
	ConnectCode string `json:"connect_code,omitempty"`
}

type KnightStatus struct {
	Enabled bool              `json:"enabled"`
	Running bool              `json:"running"`
	Status  string            `json:"status"`
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

type A2ADiscoveredInstance struct {
	ID        string `json:"id"`
	Workspace string `json:"workspace"`
	Endpoint  string `json:"endpoint"`
	Status    string `json:"status"`
	StartedAt string `json:"started_at"`
}

type ChatBridge interface {
	Messages() []provider.Message

	SendUserMessage(content []provider.ContentBlock)

	Subscribe(fn func(provider.StreamEvent)) func()
}

type AgentRunner interface {
	RunStream(ctx context.Context, userMsg string, onEvent func(provider.StreamEvent)) error

	RunStreamWithContent(ctx context.Context, content []provider.ContentBlock, onEvent func(provider.StreamEvent), origin ...string) error
	Messages() []provider.Message
}

type Server struct {
	cfg             *config.Config
	authToken       string
	mcpStatusFn     MCPStatusFunc
	imStatusFn      IMStatusFunc
	imActionFn      IMActionFunc
	restartFn       func()
	sessionSwitchFn func(sessionID string) error
	cronListFn      func() []CronJobView
	tunnelStatusFn  func() map[string]any
	chatCommandFn   func(text string) error
	chatControlsFn  func() map[string]string
	queueSnapshotFn func() []string
	wsMu            sync.Mutex
	queueSinks      map[chan interface{}]struct{}
	a2aDiscoverFn   A2ADiscoverFunc
	knightStatusFn  KnightStatusFunc
	knightActionFn  KnightActionFunc
	knightContentFn KnightSkillContentFunc
	statusFn        RuntimeStatusFunc
	sessionStore    session.Store
	workspace       string
	saveScope       string
	chatBridge      ChatBridge
	agent           AgentRunner
	agentMu         sync.Mutex
	agentBusy       atomic.Bool
	mu              sync.RWMutex
	addr            string
	listener        net.Listener
	mux             *http.ServeMux
}

func (s *Server) SetMCPStatusFn(fn MCPStatusFunc) {
	s.mcpStatusFn = fn
}

func (s *Server) SetIMStatusFn(fn IMStatusFunc) {
	s.imStatusFn = fn
}

func (s *Server) SetIMActionFn(fn IMActionFunc) {
	s.imActionFn = fn
}

func (s *Server) SetRestartFn(fn func()) {
	s.restartFn = fn
}

func (s *Server) SetA2ADiscoverFn(fn A2ADiscoverFunc) {
	s.a2aDiscoverFn = fn
}

func (s *Server) SetKnightStatusFn(fn KnightStatusFunc) {
	s.knightStatusFn = fn
}

func (s *Server) SetKnightActionFn(fn KnightActionFunc) {
	s.knightActionFn = fn
}

func (s *Server) SetKnightSkillContentFn(fn KnightSkillContentFunc) {
	s.knightContentFn = fn
}

func (s *Server) SetStatusFn(fn RuntimeStatusFunc) {
	s.statusFn = fn
}

func (s *Server) SetSessionStore(store session.Store, workspace string) {
	s.sessionStore = store
	s.workspace = workspace
}

func (s *Server) SetAgent(a AgentRunner) {
	s.agent = a
}

func (s *Server) SetChatBridge(b ChatBridge) {
	s.chatBridge = b
}

func NewServer(cfg *config.Config) *Server {
	s := &Server{
		cfg:        cfg,
		mux:        http.NewServeMux(),
		saveScope:  "global",
		authToken:  generateAuthToken(),
		queueSinks: make(map[chan interface{}]struct{}),
	}
	s.routes()
	return s
}

func (s *Server) Token() string { return s.authToken }

func (s *Server) saveConfig() error {
	return s.cfg.SaveScoped(s.saveScope)
}

func (s *Server) Start(addr string) (string, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return "", fmt.Errorf("webui listen: %w", err)
	}

	s.mu.Lock()
	if s.listener != nil {
		s.mu.Unlock()
		ln.Close()
		return "", fmt.Errorf("webui server already started")
	}
	s.listener = ln
	s.addr = ln.Addr().String()
	s.mu.Unlock()

	safego.Go("webui.httpServe", func() {

		handler := http.MaxBytesHandler(s.mux, 1<<20)
		_ = http.Serve(ln, handler)
	})

	return s.addr, nil
}

func (s *Server) Addr() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.addr
}

func (s *Server) Close() error {
	s.mu.Lock()
	ln := s.listener
	s.listener = nil
	s.mu.Unlock()
	if ln != nil {
		return ln.Close()
	}
	return nil
}

func (s *Server) routes() {

	s.mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, "{\"error\":\"unknown api endpoint: %s\"}", r.URL.Path)
	})

	// SPA 静态资源:Vite 产物 /assets/* 必须由文件服务应答(带正确 MIME),
	// 否则被下方 "/" catch-all 的 serveSPA 以 text/html 兜底,浏览器按
	// Strict MIME 拒绝加载 module script(白屏)。fs.Sub 限定子树防穿越。
	if assetsFS, err := fs.Sub(spafs, "assets"); err == nil {
		assetsHandler := http.StripPrefix("/assets/", http.FileServer(http.FS(assetsFS)))
		s.mux.Handle("/assets/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			assetsHandler.ServeHTTP(w, r)
		}))
	}

	s.mux.HandleFunc("/", s.serveSPA)

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
	s.mux.HandleFunc("/api/sessions/switch", a(s.handleSessionSwitch))
	s.mux.HandleFunc("/api/chat/controls", a(s.handleChatControls))
	s.mux.HandleFunc("/api/chat/commands", a(s.handleChatCommand))
	s.mux.HandleFunc("/api/chat/queue", a(s.handleChatQueue))
	s.mux.HandleFunc("/api/sessions/", a(s.handleSessionDetail))
	s.mux.HandleFunc("/api/workspace/files", a(s.handleWorkspaceFiles))
	s.mux.HandleFunc("/api/cron/jobs", a(s.handleCronJobs))
	s.mux.HandleFunc("/api/tunnel/status", a(s.handleTunnelStatus))
	s.mux.HandleFunc("/api/checkpoints", a(s.handleCheckpoints))
	s.mux.HandleFunc("/api/chat/ws", a(s.handleChatWS))
	s.mux.HandleFunc("/api/chat/history", a(s.handleChatHistory))
	s.mux.HandleFunc("/api/restart", a(s.handleRestart))
	s.mux.HandleFunc("/api/knight", a(s.handleKnight))
	s.mux.HandleFunc("/api/knight/skills", a(s.handleKnightSkills))
	s.mux.HandleFunc("/api/knight/action", a(s.handleKnightAction))
	s.mux.HandleFunc("/api/knight/skill-content", a(s.handleKnightSkillContent))
	s.mux.HandleFunc("/api/status", a(s.handleStatus))
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.statusFn != nil {
		writeJSON(w, s.statusFn())
		return
	}

	writeJSON(w, RuntimeStatus{
		PID:       0,
		Workspace: s.workspace,
	})
}

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

func sanitizeConfigForAPI(cfg *config.Config) map[string]interface{} {

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

			if sensitiveExtraKey(key) {
				if str, ok := val.(string); ok && str != "" {
					m[key] = "***"
				} else {
					delete(m, key)
				}
				continue
			}
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
