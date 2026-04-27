package acp

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/topcheer/ggcode/internal/debug"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/version"
)

// JSON-RPC 2.0 error codes
const (
	ErrCodeParseError     = -32700
	ErrCodeInvalidRequest = -32600
	ErrCodeMethodNotFound = -32601
	ErrCodeInvalidParams  = -32602
	ErrCodeInternalError  = -32603
)

// Handler processes ACP JSON-RPC requests and dispatches to appropriate methods.
type Handler struct {
	transport     *Transport
	sessions      map[string]*Session
	sessionsMu    sync.RWMutex
	initialized   bool
	authenticated bool
	version       int
	clientCaps    ClientCapabilities
	clientInfo    *ImplementationInfo
	cfg           *config.Config
	toolRegistry  *tool.Registry
	prov          provider.Provider
	sessionsDir   string                // directory for persistent sessions
	workspaceDirs map[string]string     // sessionID → per-workspace sessionsDir
	agentLoops    map[string]*AgentLoop // sessionID → active agent loop for mode changes
}

// NewHandler creates a new ACP handler.
func NewHandler(cfg *config.Config, registry *tool.Registry, transport *Transport, prov provider.Provider) *Handler {
	// Set up sessions directory
	homeDir, _ := os.UserHomeDir()
	sessionsDir := filepath.Join(homeDir, ".ggcode", "acp-sessions")
	os.MkdirAll(sessionsDir, 0o755)

	return &Handler{
		transport:     transport,
		sessions:      make(map[string]*Session),
		cfg:           cfg,
		toolRegistry:  registry,
		prov:          prov,
		sessionsDir:   sessionsDir,
		workspaceDirs: make(map[string]string),
		agentLoops:    make(map[string]*AgentLoop),
	}
}

// Run starts the main ACP message loop. It reads messages from the transport
// and dispatches them to the appropriate handler methods.
// Supports bi-directional communication: Client requests are dispatched to handlers,
// and Client responses (to our Agent→Client requests) are delivered to pending callers.
func (h *Handler) Run(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		req, resp, err := h.transport.ReadAnyMessage()
		if err != nil {
			// EOF means client disconnected — normal shutdown
			if errors.Is(err, io.EOF) {
				return nil
			}
			debug.Log("acp", "error reading message: %v", err)
			continue
		}

		// Client response to our pending request (e.g. session/request_permission)
		if resp != nil {
			h.transport.DeliverResponse(resp)
			continue
		}

		// Client request
		if req != nil {
			h.handleRequest(ctx, req)
		}
	}
}

// handleRequest dispatches a single JSON-RPC request.
func (h *Handler) handleRequest(_ context.Context, req *JSONRPCRequest) {
	// Route to method handler
	switch req.Method {
	case "initialize":
		h.dispatch(req, h.handleInitialize)
	case "session/authenticate":
		h.dispatch(req, h.handleAuthenticate)
	case "session/new":
		h.dispatch(req, h.handleSessionNew)
	case "session/prompt":
		h.dispatch(req, h.handleSessionPrompt)
	case "session/cancel":
		h.dispatch(req, h.handleSessionCancel)
	case "session/load":
		h.dispatch(req, h.handleSessionLoad)
	case "session/set_mode":
		h.dispatch(req, h.handleSessionSetMode)
	case "session/close":
		h.dispatch(req, h.handleSessionClose)
	case "session/list":
		h.dispatch(req, h.handleSessionList)
	case "session/resume":
		h.dispatch(req, h.handleSessionResume)
	case "session/set_config_option":
		h.dispatch(req, h.handleSetConfigOption)
	default:
		if req.ID != nil {
			_ = h.transport.WriteError(req.ID, ErrCodeMethodNotFound, fmt.Sprintf("method not found: %s", req.Method))
		}
	}
}

// dispatch handles a request with standard error handling.
func (h *Handler) dispatch(req *JSONRPCRequest, handler func(json.RawMessage) (interface{}, error)) {
	result, err := handler(req.Params)
	if err != nil {
		if req.ID != nil {
			_ = h.transport.WriteError(req.ID, ErrCodeInternalError, err.Error())
		}
		return
	}
	if req.ID != nil {
		_ = h.transport.WriteResponse(req.ID, result)
	}
}

// handleInitialize handles the "initialize" method.
func (h *Handler) handleInitialize(params json.RawMessage) (interface{}, error) {
	var initParams InitializeParams
	if err := json.Unmarshal(params, &initParams); err != nil {
		return nil, fmt.Errorf("invalid initialize params: %w", err)
	}

	h.version = initParams.ProtocolVersion
	h.clientCaps = initParams.ClientCapabilities
	h.clientInfo = initParams.ClientInfo
	h.initialized = true

	result := InitializeResult{
		ProtocolVersion: ProtocolVersion,
		AgentCapabilities: AgentCapabilities{
			LoadSession: true,
			PromptCapabilities: &PromptCapabilities{
				Image:           true,
				EmbeddedContext: true,
			},
			MCPCapabilities: &MCPCapabilities{
				HTTP: true,
				SSE:  true,
			},
			SessionCapabilities: &SessionCapabilities{
				Close:  &SessionCloseCapabilities{},
				List:   &SessionListCapabilities{},
				Resume: &SessionResumeCapabilities{},
			},
		},
		AgentInfo: ImplementationInfo{
			Name:    "ggcode",
			Title:   "ggcode AI Coding Agent",
			Version: version.Version,
		},
		AuthMethods: h.getAuthMethods(),
	}

	return result, nil
}

// handleAuthenticate handles the "authenticate" method.
// It processes the Client's authentication request based on the auth method ID.
func (h *Handler) handleAuthenticate(params json.RawMessage) (interface{}, error) {
	var authParams AuthenticateParams
	if err := json.Unmarshal(params, &authParams); err != nil {
		return nil, fmt.Errorf("invalid authenticate params: %w", err)
	}

	switch authParams.AuthMethodID {
	case "agent":
		// GitHub Device Flow — runs in background, sends user_code via notification
		authHandler := NewAuthHandler(h.transport, "")
		safego.Go("acp.handler.deviceFlow", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
			defer cancel()
			if err := authHandler.HandleAgentAuth(ctx); err != nil {
				debug.Log("acp", "device flow auth error: %v", err)
				return
			}
			h.authenticated = true
		})
		return AuthenticateResult{}, nil
	case "api-key":
		// Env Var Auth — validate required env vars
		authHandler := NewAuthHandler(h.transport, "")
		authMethods := h.getAuthMethods()
		for _, m := range authMethods {
			if m.ID == "api-key" {
				if err := authHandler.HandleEnvVarAuth(m.Vars); err != nil {
					return nil, fmt.Errorf("env var auth: %w", err)
				}
				break
			}
		}
		h.authenticated = true
		return AuthenticateResult{}, nil
	default:
		return nil, fmt.Errorf("unknown auth method: %s", authParams.AuthMethodID)
	}
}

// handleSessionNew handles the "session/new" method.
func (h *Handler) handleSessionNew(params json.RawMessage) (interface{}, error) {
	if !h.initialized {
		return nil, fmt.Errorf("not initialized")
	}

	var sessionParams SessionNewParams
	if err := json.Unmarshal(params, &sessionParams); err != nil {
		return nil, fmt.Errorf("invalid session/new params: %w", err)
	}

	session := NewSession(sessionParams.CWD, sessionParams.MCPServers)

	// Ensure per-workspace session directory exists
	sessionDir := workspaceSessionsDir(h.sessionsDir, sessionParams.CWD)
	os.MkdirAll(sessionDir, 0o755)

	h.sessionsMu.Lock()
	h.sessions[session.ID] = session
	h.workspaceDirs[session.ID] = sessionDir
	h.sessionsMu.Unlock()

	// Start MCP servers if provided
	if len(sessionParams.MCPServers) > 0 {
		mgr := NewMCPManager(h.toolRegistry)
		if err := mgr.ConnectServers(context.Background(), sessionParams.MCPServers); err != nil {
			debug.Log("acp", "MCP server connection errors: %v", err)
		}
		session.mcpManager = mgr
	}

	return SessionNewResult{
		SessionID:     session.ID,
		Modes:         getDefaultSessionModeStatePtr(),
		ConfigOptions: getDefaultConfigOptions(),
	}, nil
}

// handleSessionPrompt handles the "session/prompt" method.
func (h *Handler) handleSessionPrompt(params json.RawMessage) (interface{}, error) {
	if !h.initialized {
		return nil, fmt.Errorf("not initialized")
	}

	var promptParams SessionPromptParams
	if err := json.Unmarshal(params, &promptParams); err != nil {
		return nil, fmt.Errorf("invalid session/prompt params: %w", err)
	}

	h.sessionsMu.RLock()
	session, ok := h.sessions[promptParams.SessionID]
	h.sessionsMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("session not found: %s", promptParams.SessionID)
	}

	// Create agent loop and execute prompt in background goroutine
	loop := NewAgentLoop(h.cfg, h.toolRegistry, h.transport, session, h.clientCaps, h.prov)

	// Store agent loop so set_mode can update it
	h.sessionsMu.Lock()
	h.agentLoops[promptParams.SessionID] = loop
	h.sessionsMu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	session.SetCancel(cancel)

	safego.Go("acp.handler.agentLoop", func() {
		defer cancel()
		defer func() {
			// Clean up agent loop reference when done
			h.sessionsMu.Lock()
			delete(h.agentLoops, promptParams.SessionID)
			h.sessionsMu.Unlock()
		}()
		if err := loop.ExecutePrompt(ctx, promptParams.Prompt); err != nil {
			debug.Log("acp", "agent loop error: %v", err)
		}
		// Persist session after prompt execution
		h.sessionsMu.RLock()
		sDir := h.workspaceDirs[promptParams.SessionID]
		h.sessionsMu.RUnlock()
		if sDir == "" {
			sDir = h.sessionsDir
		}
		if saveErr := session.Save(sDir); saveErr != nil {
			debug.Log("acp", "failed to save session: %v", saveErr)
		}
		// Clean up MCP connections
		if session.mcpManager != nil {
			if err := session.mcpManager.Close(); err != nil {
				debug.Log("acp", "MCP cleanup error: %v", err)
			}
		}
	})

	// Return immediately; updates come via session/update notifications
	return SessionPromptResult{}, nil
}

// handleSessionCancel handles the "session/cancel" method.
func (h *Handler) handleSessionCancel(params json.RawMessage) (interface{}, error) {
	var cancelParams SessionCancelParams
	if err := json.Unmarshal(params, &cancelParams); err != nil {
		return nil, fmt.Errorf("invalid session/cancel params: %w", err)
	}

	h.sessionsMu.RLock()
	session, ok := h.sessions[cancelParams.SessionID]
	h.sessionsMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("session not found: %s", cancelParams.SessionID)
	}

	session.DoCancel()
	return struct{}{}, nil
}

// handleSessionLoad handles the "session/load" method.
// It loads a previously persisted session and replays its messages.
func (h *Handler) handleSessionLoad(params json.RawMessage) (interface{}, error) {
	if !h.initialized {
		return nil, fmt.Errorf("not initialized")
	}

	var loadParams SessionLoadParams
	if err := json.Unmarshal(params, &loadParams); err != nil {
		return nil, fmt.Errorf("invalid session/load params: %w", err)
	}

	// Load session from disk
	session, err := LoadSession(h.sessionsDir, loadParams.SessionID)
	if err != nil {
		// Try to find in workspace subdirectories
		session, err = h.loadSessionFromWorkspaces(loadParams.SessionID)
		if err != nil {
			return nil, fmt.Errorf("loading session: %w", err)
		}
	}

	// Register the loaded session
	sessionDir := workspaceSessionsDir(h.sessionsDir, session.CWD)
	os.MkdirAll(sessionDir, 0o755)
	h.sessionsMu.Lock()
	h.sessions[session.ID] = session
	h.workspaceDirs[session.ID] = sessionDir
	h.sessionsMu.Unlock()

	// Replay messages as session/update notifications
	for _, msg := range session.Messages() {
		for _, block := range msg.Content {
			updateType := "agent_message_chunk"
			if msg.Role == "user" {
				updateType = "user_message_chunk"
			}
			_ = h.transport.WriteNotification("session/update", SessionUpdateParams{
				SessionID: session.ID,
				Update: SessionUpdate{
					Type:    updateType,
					Content: &block,
				},
			})
		}
	}

	// Per ACP spec: respond with null after replaying all messages
	return nil, nil
}

// handleSessionSetMode handles the "session/set_mode" method.
// It allows the Client to change the session's permission mode.
func (h *Handler) handleSessionSetMode(params json.RawMessage) (interface{}, error) {
	if !h.initialized {
		return nil, fmt.Errorf("not initialized")
	}

	var modeParams SessionSetModeParams
	if err := json.Unmarshal(params, &modeParams); err != nil {
		return nil, fmt.Errorf("invalid session/set_mode params: %w", err)
	}

	h.sessionsMu.RLock()
	session, ok := h.sessions[modeParams.SessionID]
	loop := h.agentLoops[modeParams.SessionID]
	h.sessionsMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("session not found: %s", modeParams.SessionID)
	}

	// Update the active agent loop's permission mode
	if loop != nil {
		loop.SetMode(modeParams.Mode)
	}

	debug.Log("acp", "session %s mode changed to %s", session.ID, modeParams.Mode)

	return SessionSetModeResult{}, nil
}

// getAuthMethods returns the supported authentication methods.
func (h *Handler) getAuthMethods() []AuthMethod {
	secret := true
	optional := false
	return []AuthMethod{
		{
			ID:          "agent",
			Name:        "ggcode Agent Auth",
			Description: "Authenticate through ggcode (GitHub Device Flow)",
		},
		{
			ID:   "api-key",
			Name: "API Key",
			Type: "env_var",
			Vars: []AuthEnvVar{
				{
					Name:     "GGCODE_API_KEY",
					Label:    "API Key",
					Secret:   &secret,
					Optional: &optional,
				},
			},
		},
	}
}

// workspaceSessionsDir returns a per-workspace session directory.
// This allows multiple ggcode ACP instances to maintain separate session stores
// for different workspaces without conflicts.
func workspaceSessionsDir(baseDir, cwd string) string {
	if cwd == "" {
		return baseDir
	}
	absCWD := cwd
	h := sha256.Sum256([]byte(absCWD))
	short := fmt.Sprintf("%x", h[:8]) // first 8 bytes = 16 hex chars
	return filepath.Join(baseDir, short)
}

// loadSessionFromWorkspaces searches workspace subdirectories for a session.
func (h *Handler) loadSessionFromWorkspaces(sessionID string) (*Session, error) {
	entries, err := os.ReadDir(h.sessionsDir)
	if err != nil {
		return nil, fmt.Errorf("reading sessions directory: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		s, err := LoadSession(filepath.Join(h.sessionsDir, entry.Name()), sessionID)
		if err == nil {
			return s, nil
		}
	}
	return nil, fmt.Errorf("session %s not found in any workspace", sessionID)
}

// handleSessionClose closes an active session and cleans up resources.
func (h *Handler) handleSessionClose(params json.RawMessage) (interface{}, error) {
	var req CloseSessionRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("parsing session/close params: %w", err)
	}

	h.sessionsMu.Lock()
	session, ok := h.sessions[req.SessionID]
	h.sessionsMu.Unlock()

	if !ok {
		return nil, fmt.Errorf("session %s not found", req.SessionID)
	}

	// Cancel any ongoing work
	if session.Cancel != nil {
		session.Cancel()
	}

	// Remove from active sessions
	h.sessionsMu.Lock()
	delete(h.sessions, req.SessionID)
	h.sessionsMu.Unlock()

	debug.Log("acp", "session %s closed", req.SessionID)
	return CloseSessionResponse{}, nil
}

// handleSessionList lists existing sessions for the given cwd.
func (h *Handler) handleSessionList(params json.RawMessage) (interface{}, error) {
	var req ListSessionsRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("parsing session/list params: %w", err)
	}

	var sessions []SessionInfo
	searchDir := h.sessionsDir
	if req.CWD != "" {
		searchDir = workspaceSessionsDir(h.sessionsDir, req.CWD)
	}

	entries, err := os.ReadDir(searchDir)
	if err != nil {
		// No sessions yet is not an error
		return ListSessionsResponse{Sessions: []SessionInfo{}}, nil
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		// Try to load session metadata
		metaPath := filepath.Join(searchDir, entry.Name(), "session.json")
		data, err := os.ReadFile(metaPath)
		if err != nil {
			continue
		}
		var sd struct {
			ID        string `json:"id"`
			CWD       string `json:"cwd"`
			CreatedAt string `json:"created_at"`
			UpdatedAt string `json:"updated_at"`
		}
		if err := json.Unmarshal(data, &sd); err != nil {
			continue
		}
		sessions = append(sessions, SessionInfo{
			SessionID: sd.ID,
			CWD:       sd.CWD,
			CreatedAt: sd.CreatedAt,
			UpdatedAt: sd.UpdatedAt,
		})
	}

	return ListSessionsResponse{Sessions: sessions}, nil
}

// handleSessionResume resumes an existing session.
func (h *Handler) handleSessionResume(params json.RawMessage) (interface{}, error) {
	var req ResumeSessionRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("parsing session/resume params: %w", err)
	}

	session, err := h.loadSessionFromWorkspaces(req.SessionID)
	if err != nil {
		return nil, fmt.Errorf("loading session: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	_ = ctx
	session.SetCancel(cancel)

	// Connect MCP servers if provided
	if len(req.MCPServers) > 0 {
		if err := h.connectMCPServers(ctx, session, req.MCPServers); err != nil {
			debug.Log("acp", "MCP server connection errors: %v", err)
		}
	}

	h.sessionsMu.Lock()
	h.sessions[req.SessionID] = session
	h.sessionsMu.Unlock()

	modes := getDefaultSessionModeState()
	configOpts := getDefaultConfigOptions()

	debug.Log("acp", "session %s resumed", req.SessionID)
	return ResumeSessionResponse{
		Modes:         &modes,
		ConfigOptions: configOpts,
	}, nil
}

// handleSetConfigOption sets a configuration option for a session.
func (h *Handler) handleSetConfigOption(params json.RawMessage) (interface{}, error) {
	var req SetSessionConfigOptionRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("parsing session/set_config_option params: %w", err)
	}

	h.sessionsMu.Lock()
	_, ok := h.sessions[req.SessionID]
	// Mode change is handled by the agent loop config
	h.sessionsMu.Unlock()

	configOpts := getDefaultConfigOptions()
	if ok {
		// Update the current value for mode
		for i := range configOpts {
			if configOpts[i].ID == req.ConfigID {
				configOpts[i].CurrentValue = req.Value
			}
		}
	}

	return SetSessionConfigOptionResponse{
		ConfigOptions: configOpts,
	}, nil
}

// getDefaultSessionModeState returns the default modes for ACP sessions.
func getDefaultSessionModeState() SessionModeState {
	return SessionModeState{
		Modes: []SessionMode{
			{ID: "supervised", Name: "Supervised", Description: "Asks for confirmation on tool use"},
			{ID: "auto", Name: "Auto", Description: "Automatically allows safe operations"},
			{ID: "bypass", Name: "Bypass", Description: "Allows almost everything"},
			{ID: "autopilot", Name: "Autopilot", Description: "Full autonomy with escalation"},
		},
		Current: "bypass",
	}
}

func getDefaultSessionModeStatePtr() *SessionModeState {
	s := getDefaultSessionModeState()
	return &s
}

// getDefaultConfigOptions returns config options for ACP sessions.
func getDefaultConfigOptions() []SessionConfigOption {
	modes := getDefaultSessionModeState()
	var modeOptions []interface{}
	for _, m := range modes.Modes {
		modeOptions = append(modeOptions, SessionConfigSelectOption{
			ID:   SessionConfigValueId(m.ID),
			Name: m.Name,
		})
	}
	return []SessionConfigOption{
		{
			Type:         "select",
			ID:           "mode",
			Name:         "Mode",
			Description:  "Permission mode for the session",
			Category:     "mode",
			CurrentValue: SessionConfigValueId(modes.Current),
			Options:      modeOptions,
		},
	}
}

// connectMCPServers connects MCP servers for a session.
func (h *Handler) connectMCPServers(ctx context.Context, session *Session, servers []MCPServer) error {
	if len(servers) == 0 {
		return nil
	}
	mgr := NewMCPManager(h.toolRegistry)
	if err := mgr.ConnectServers(ctx, servers); err != nil {
		return err
	}
	session.mcpManager = mgr
	return nil
}
