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
	"sort"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/version"
)

const (
	ErrCodeParseError     = -32700
	ErrCodeInvalidRequest = -32600
	ErrCodeMethodNotFound = -32601
	ErrCodeInvalidParams  = -32602
	ErrCodeInternalError  = -32603
)

type Handler struct {
	transport      *Transport
	sessions       map[string]*Session
	sessionsMu     sync.RWMutex
	initialized    bool
	authMu         sync.Mutex
	authenticated  bool
	authErr        error
	authMethodUsed string
	version        int
	clientCaps     ClientCapabilities
	clientInfo     *ImplementationInfo
	cfg            *config.Config
	toolRegistry   *tool.Registry
	prov           provider.Provider
	sessionsDir    string
	workspaceDirs  map[string]string
	agentLoops     map[string]*AgentLoop
	sessionModes   map[string]string
}

func NewHandler(cfg *config.Config, registry *tool.Registry, transport *Transport, prov provider.Provider) *Handler {
	homeDir := config.HomeDir()
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
		sessionModes:  make(map[string]string),
	}
}

func isAuthMethodRequired(methodID string) bool {
	return methodID == "agent" || methodID == "api-key"
}

func (h *Handler) Run(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			h.transport.FailAllPending(ctx.Err())
			h.cleanupEmptySessions()
			return ctx.Err()
		default:
		}

		req, resp, err := h.transport.ReadAnyMessage()
		if err != nil {
			if errors.Is(err, io.EOF) {
				h.transport.FailAllPending(fmt.Errorf("client disconnected"))
				h.cleanupEmptySessions()
				return nil
			}
			debug.Log("acp", "error reading message: %v", err)
			continue
		}

		if resp != nil {
			h.transport.DeliverResponse(resp)
			continue
		}

		if req != nil {
			h.handleRequest(ctx, req)
		}
	}
}

func (h *Handler) handleRequest(_ context.Context, req *JSONRPCRequest) {
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

func (h *Handler) handleAuthenticate(params json.RawMessage) (interface{}, error) {
	var authParams AuthenticateParams
	if err := json.Unmarshal(params, &authParams); err != nil {
		return nil, fmt.Errorf("invalid authenticate params: %w", err)
	}

	switch authParams.AuthMethodID {
	case "agent":
		authHandler := NewAuthHandler(h.transport, "")
		h.authMu.Lock()
		h.authMethodUsed = "agent"
		h.authErr = nil
		h.authMu.Unlock()
		safego.Go("acp.handler.deviceFlow", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
			defer cancel()
			if err := authHandler.HandleAgentAuth(ctx); err != nil {
				debug.Log("acp", "device flow auth error: %v", err)
				h.authMu.Lock()
				h.authErr = fmt.Errorf("agent authentication failed: %w", err)
				h.authenticated = false
				h.authMu.Unlock()
				return
			}
			h.authMu.Lock()
			h.authenticated = true
			h.authErr = nil
			h.authMu.Unlock()
		})
		return AuthenticateResult{}, nil
	case "api-key":
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
		h.authMu.Lock()
		h.authenticated = true
		h.authErr = nil
		h.authMethodUsed = "api-key"
		h.authMu.Unlock()
		return AuthenticateResult{}, nil
	default:
		return nil, fmt.Errorf("unknown auth method: %s", authParams.AuthMethodID)
	}
}

func (h *Handler) handleSessionNew(params json.RawMessage) (interface{}, error) {
	if !h.initialized {
		return nil, fmt.Errorf("not initialized")
	}

	var sessionParams SessionNewParams
	if err := json.Unmarshal(params, &sessionParams); err != nil {
		return nil, fmt.Errorf("invalid session/new params: %w", err)
	}

	if err := validateCWD(sessionParams.CWD); err != nil {
		return nil, err
	}

	session := NewSession(sessionParams.CWD, sessionParams.MCPServers)

	sessionDir := workspaceSessionsDir(h.sessionsDir, sessionParams.CWD)
	os.MkdirAll(sessionDir, 0o755)
	session.SetSaveDir(sessionDir)

	h.sessionsMu.Lock()
	h.sessions[session.ID] = session
	h.workspaceDirs[session.ID] = sessionDir
	h.sessionsMu.Unlock()

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

func (h *Handler) checkPromptAuth() error {
	h.authMu.Lock()
	authenticated := h.authenticated
	authErr := h.authErr
	authMethod := h.authMethodUsed
	h.authMu.Unlock()
	if authMethod == "" || !isAuthMethodRequired(authMethod) || authenticated {
		return nil
	}
	if authErr != nil {
		return fmt.Errorf("authentication required (%s): %w", authMethod, authErr)
	}
	return fmt.Errorf("authentication required (%s): authentication is still in progress", authMethod)
}

func (h *Handler) agentLoopForSession(session *Session) *AgentLoop {
	h.sessionsMu.Lock()
	defer h.sessionsMu.Unlock()
	if loop, ok := h.agentLoops[session.ID]; ok {
		return loop
	}
	loop := NewAgentLoop(h.cfg, h.toolRegistry, h.transport, session, h.clientCaps, h.prov)
	if msgs := session.Messages(); len(msgs) > 0 {
		loop.RestoreConversation(msgs)
	}
	h.agentLoops[session.ID] = loop
	return loop
}

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

	if err := h.checkPromptAuth(); err != nil {
		return nil, err
	}

	loop := h.agentLoopForSession(session)
	if !session.TryBeginRun() {
		return nil, fmt.Errorf("session %s already has a prompt in flight; wait for session/prompt_complete or send session/cancel first", promptParams.SessionID)
	}
	h.sessionsMu.RLock()
	mode, hasMode := h.sessionModes[session.ID]
	h.sessionsMu.RUnlock()
	if hasMode {
		loop.SetMode(mode)
	}

	ctx, cancel := context.WithCancel(context.Background())
	session.SetCancel(cancel)

	safego.Go("acp.handler.agentLoop", func() {
		defer cancel()
		defer session.EndRun()
		defer func() {
			h.sessionsMu.Lock()
			delete(h.agentLoops, promptParams.SessionID)
			h.sessionsMu.Unlock()
		}()
		stopReason := StopReasonEndTurn
		if err := loop.ExecutePrompt(ctx, promptParams.Prompt); err != nil {
			debug.Log("acp", "agent loop error: %v", err)
			if errors.Is(err, context.Canceled) {
				stopReason = StopReasonCancelled
			} else {
				stopReason = StopReasonError
			}
		}
		_ = h.transport.WriteNotification("session/prompt_complete", PromptCompleteNotification{
			SessionID: promptParams.SessionID,
			Response: PromptResponse{
				StopReason: stopReason,
			},
		})
		h.sessionsMu.RLock()
		_, stillActive := h.sessions[promptParams.SessionID]
		sDir := h.workspaceDirs[promptParams.SessionID]
		h.sessionsMu.RUnlock()
		if !stillActive {
			debug.Log("acp", "session %s was closed during prompt, skipping save", promptParams.SessionID)
		} else if sDir == "" {
			debug.Log("acp", "session %s has no workspace dir, skipping save", promptParams.SessionID)
		} else if saveErr := session.Save(sDir); saveErr != nil {
			debug.Log("acp", "failed to save session: %v", saveErr)
		}
	})

	return SessionPromptResult{}, nil
}

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

func (h *Handler) handleSessionLoad(params json.RawMessage) (interface{}, error) {
	if !h.initialized {
		return nil, fmt.Errorf("not initialized")
	}

	var loadParams SessionLoadParams
	if err := json.Unmarshal(params, &loadParams); err != nil {
		return nil, fmt.Errorf("invalid session/load params: %w", err)
	}

	h.sessionsMu.RLock()
	_, exists := h.sessions[loadParams.SessionID]
	h.sessionsMu.RUnlock()
	if exists {
		return nil, fmt.Errorf("session %s already active: cannot load", loadParams.SessionID)
	}

	session, err := LoadSession(h.sessionsDir, loadParams.SessionID)
	if err != nil {
		session, err = h.loadSessionFromWorkspaces(loadParams.SessionID)
		if err != nil {
			return nil, fmt.Errorf("loading session: %w", err)
		}
	} else {
		sub := filepath.Join(workspaceSessionsDir(h.sessionsDir, session.CWD), loadParams.SessionID+".json")
		if sub != filepath.Join(h.sessionsDir, loadParams.SessionID+".json") {
			if subData, subErr := os.ReadFile(sub); subErr == nil {
				var sd SessionData
				if json.Unmarshal(subData, &sd) == nil && sd.ID == session.ID {
					if s2, lErr := LoadSession(workspaceSessionsDir(h.sessionsDir, session.CWD), loadParams.SessionID); lErr == nil {
						session = s2
					}
					_ = os.Remove(filepath.Join(h.sessionsDir, loadParams.SessionID+".json"))
				}
			}
		}
	}

	sessionDir := workspaceSessionsDir(h.sessionsDir, session.CWD)
	os.MkdirAll(sessionDir, 0o755)
	session.SetSaveDir(sessionDir)
	h.sessionsMu.Lock()
	h.sessions[session.ID] = session
	h.workspaceDirs[session.ID] = sessionDir
	h.sessionsMu.Unlock()

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

	return nil, nil
}

func (h *Handler) handleSessionSetMode(params json.RawMessage) (interface{}, error) {
	if !h.initialized {
		return nil, fmt.Errorf("not initialized")
	}

	var modeParams SessionSetModeParams
	if err := json.Unmarshal(params, &modeParams); err != nil {
		return nil, fmt.Errorf("invalid session/set_mode params: %w", err)
	}

	h.sessionsMu.Lock()
	session, ok := h.sessions[modeParams.SessionID]
	loop := h.agentLoops[modeParams.SessionID]
	if ok {
		h.sessionModes[modeParams.SessionID] = modeParams.Mode
	}
	h.sessionsMu.Unlock()
	if !ok {
		return nil, fmt.Errorf("session not found: %s", modeParams.SessionID)
	}

	if loop != nil {
		loop.SetMode(modeParams.Mode)
	}

	debug.Log("acp", "session %s mode changed to %s", session.ID, modeParams.Mode)

	return SessionSetModeResult{}, nil
}

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

func validateCWD(cwd string) error {
	if cwd == "" || cwd == "/" || !filepath.IsAbs(cwd) {
		return fmt.Errorf("invalid cwd %q: must be an absolute project directory path", cwd)
	}
	info, err := os.Stat(cwd)
	if err != nil {
		return fmt.Errorf("cwd %q does not exist: %w", cwd, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("cwd %q is not a directory", cwd)
	}
	return nil
}

func workspaceSessionsDir(baseDir, cwd string) string {
	if cwd == "" {
		return baseDir
	}
	absCWD := cwd
	h := sha256.Sum256([]byte(absCWD))
	short := fmt.Sprintf("%x", h[:8])
	return filepath.Join(baseDir, short)
}

func (h *Handler) loadSessionFromWorkspaces(sessionID string) (*Session, error) {
	entries, err := os.ReadDir(h.sessionsDir)
	if err != nil {
		return nil, fmt.Errorf("reading sessions directory: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		wsDir := filepath.Join(h.sessionsDir, entry.Name())
		s, err := LoadSession(wsDir, sessionID)
		if err == nil {
			s.SetSaveDir(wsDir)
			return s, nil
		}
	}
	return nil, fmt.Errorf("session %s not found in any workspace", sessionID)
}

func (h *Handler) cleanupEmptySessions() {
	h.sessionsMu.RLock()
	type sessionInfo struct {
		id      string
		saveDir string
	}
	var toCheck []sessionInfo
	for id, s := range h.sessions {
		if !s.HasMessages() {
			toCheck = append(toCheck, sessionInfo{id: id, saveDir: h.workspaceDirs[id]})
		}
	}
	h.sessionsMu.RUnlock()

	for _, si := range toCheck {
		if si.saveDir == "" {
			continue
		}
		path := filepath.Join(si.saveDir, si.id+".json")
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			debug.Log("acp", "failed to remove empty session file %s: %v", path, err)
		}
	}
}

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

	session.DoCancel()

	if session.mcpManager != nil {
		if err := session.mcpManager.Close(); err != nil {
			debug.Log("acp", "MCP cleanup error on session close: %v", err)
		}
	}

	h.sessionsMu.Lock()
	delete(h.sessions, req.SessionID)
	delete(h.agentLoops, req.SessionID)
	delete(h.sessionModes, req.SessionID)
	delete(h.workspaceDirs, req.SessionID)
	h.sessionsMu.Unlock()

	debug.Log("acp", "session %s closed", req.SessionID)
	return CloseSessionResponse{}, nil
}

func (h *Handler) handleSessionList(params json.RawMessage) (interface{}, error) {
	var req ListSessionsRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("parsing session/list params: %w", err)
	}

	var sessions []SessionInfo
	for _, dir := range h.sessionSearchDirs(req.CWD) {
		sessions = append(sessions, readSessionInfos(dir)...)
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt > sessions[j].UpdatedAt
	})
	if sessions == nil {
		sessions = []SessionInfo{}
	}
	return ListSessionsResponse{Sessions: sessions}, nil
}

func (h *Handler) sessionSearchDirs(cwd string) []string {
	if cwd != "" {
		return []string{workspaceSessionsDir(h.sessionsDir, cwd)}
	}
	entries, err := os.ReadDir(h.sessionsDir)
	if err != nil {
		return nil
	}
	var dirs []string
	for _, entry := range entries {
		if entry.IsDir() {
			dirs = append(dirs, filepath.Join(h.sessionsDir, entry.Name()))
		}
	}
	return dirs
}

type sessionFileMeta struct {
	ID        string    `json:"id"`
	CWD       string    `json:"cwd"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func readSessionInfos(dir string) []SessionInfo {
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var infos []SessionInfo
	for _, f := range files {
		if f.IsDir() || filepath.Ext(f.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, f.Name()))
		if err != nil {
			continue
		}
		var sd sessionFileMeta
		if err := json.Unmarshal(data, &sd); err != nil || sd.ID == "" {
			continue
		}
		infos = append(infos, SessionInfo{
			SessionID: sd.ID,
			CWD:       sd.CWD,
			CreatedAt: sd.CreatedAt.Format(time.RFC3339),
			UpdatedAt: sd.UpdatedAt.Format(time.RFC3339),
		})
	}
	return infos
}

func (h *Handler) handleSessionResume(params json.RawMessage) (interface{}, error) {
	var req ResumeSessionRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("parsing session/resume params: %w", err)
	}

	session, err := h.loadSessionFromWorkspaces(req.SessionID)
	if err != nil {
		return nil, fmt.Errorf("loading session: %w", err)
	}

	h.sessionsMu.Lock()
	if _, exists := h.sessions[req.SessionID]; exists {
		h.sessionsMu.Unlock()
		return nil, fmt.Errorf("session %s already active: cannot resume", req.SessionID)
	}
	h.sessionsMu.Unlock()

	if req.CWD != "" {
		if err := validateCWD(req.CWD); err != nil {
			return nil, err
		}
		session.CWD = req.CWD
		sessionDir := workspaceSessionsDir(h.sessionsDir, req.CWD)
		os.MkdirAll(sessionDir, 0o755)
		session.SetSaveDir(sessionDir)
	} else if err := validateCWD(session.CWD); err != nil {
		return nil, fmt.Errorf("session has invalid stored cwd: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	session.SetCancel(cancel)

	if len(req.MCPServers) > 0 {
		if err := h.connectMCPServers(ctx, session, req.MCPServers); err != nil {
			debug.Log("acp", "MCP server connection errors: %v", err)
		}
	}

	h.sessionsMu.Lock()
	h.sessions[req.SessionID] = session
	h.workspaceDirs[req.SessionID] = session.SaveDir()
	h.sessionsMu.Unlock()

	modes := getDefaultSessionModeState()
	configOpts := getDefaultConfigOptions()

	debug.Log("acp", "session %s resumed", req.SessionID)
	return ResumeSessionResponse{
		Modes:         &modes,
		ConfigOptions: configOpts,
	}, nil
}

func (h *Handler) handleSetConfigOption(params json.RawMessage) (interface{}, error) {
	var req SetSessionConfigOptionRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("parsing session/set_config_option params: %w", err)
	}

	h.sessionsMu.Lock()
	session, ok := h.sessions[req.SessionID]
	loop := h.agentLoops[req.SessionID]
	h.sessionsMu.Unlock()
	if !ok {
		return nil, fmt.Errorf("session not found: %s", req.SessionID)
	}

	if req.ConfigID == "mode" {
		valid := false
		for _, m := range getDefaultSessionModeState().Modes {
			if SessionConfigValueId(m.ID) == req.Value {
				valid = true
				break
			}
		}
		if !valid {
			return nil, fmt.Errorf("invalid value %q for config option \"mode\"", string(req.Value))
		}
		h.sessionsMu.Lock()
		h.sessionModes[req.SessionID] = string(req.Value)
		h.sessionsMu.Unlock()
		if loop != nil {
			loop.SetMode(string(req.Value))
		}
		debug.Log("acp", "session %s mode changed to %s via set_config_option", session.ID, string(req.Value))
	}

	configOpts := getDefaultConfigOptions()
	for i := range configOpts {
		if configOpts[i].ID == req.ConfigID {
			configOpts[i].CurrentValue = req.Value
		}
	}

	return SetSessionConfigOptionResponse{
		ConfigOptions: configOpts,
	}, nil
}

func getDefaultSessionModeState() SessionModeState {
	return SessionModeState{
		Modes: []SessionMode{
			{ID: "supervised", Name: "Supervised", Description: "Asks for confirmation on tool use"},
			{ID: "auto", Name: "Auto", Description: "Automatically allows safe operations"},
			{ID: "bypass", Name: "Bypass", Description: "Allows almost everything"},
			{ID: "autopilot", Name: "Autopilot", Description: "Full autonomy with escalation"},
		},
		Current: "auto",
	}
}

func getDefaultSessionModeStatePtr() *SessionModeState {
	s := getDefaultSessionModeState()
	return &s
}

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

func (h *Handler) connectMCPServers(ctx context.Context, session *Session, servers []MCPServer) error {
	if len(servers) == 0 {
		return nil
	}
	if session.mcpManager != nil {
		if err := session.mcpManager.Close(); err != nil {
			debug.Log("acp", "MCP close on manager replacement: %v", err)
		}
	}
	mgr := NewMCPManager(h.toolRegistry)
	if err := mgr.ConnectServers(ctx, servers); err != nil {
		debug.Log("acp", "MCP server connection errors: %v", err)
	}
	session.mcpManager = mgr
	return nil
}
