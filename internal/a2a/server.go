package a2a

import (
	"bytes"
	"context"
	"crypto/subtle"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/auth"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/util"
)

const maxPushConfigs = 256

type Server struct {
	handler        *TaskHandler
	card           AgentCard
	cardMu         sync.RWMutex
	extendedCard   json.RawMessage
	apiKeys        []string
	server         *http.Server
	mux            *http.ServeMux
	port           int
	done           chan struct{}
	doneOnce       sync.Once
	pushConfigs    map[string]PushNotificationConfig
	pushMu         sync.RWMutex
	tokenValidator *auth.TokenValidator
	mtlsEnabled    bool
	tlsConfig      *tls.Config

	pushGuard                *pushGuard
	allowWildcardPushConfigs bool
	pushClient               *http.Client

	// #5575 runtime introspection (additive; empty mode = handler absent,
	// legacy behavior byte-identical).
	introspectionMode string
	introspector      RuntimeIntrospector
	domainKey         []byte

	extensions []AgentExtension

	delegation DelegationPolicy
}

type ServerConfig struct {
	Host     string
	Port     int
	APIKey   string
	APIKeys  []string
	Instance string

	PushNotifications *bool

	PushCallbackAllowlist []string

	AllowWildcardPushCallbacks bool

	Extensions []AgentExtension

	Delegation DelegationPolicy

	// #5575: empty mode = introspection off (handler not registered).
	IntrospectionMode string
	Introspector      RuntimeIntrospector
	DomainKey         []byte
}

func NewServer(cfg ServerConfig, handler *TaskHandler) *Server {
	apiKeys := cfg.APIKeys
	if cfg.APIKey != "" {
		apiKeys = append(apiKeys, cfg.APIKey)
	}

	s := &Server{
		handler:                  handler,
		apiKeys:                  apiKeys,
		introspectionMode:        cfg.IntrospectionMode,
		introspector:             cfg.Introspector,
		domainKey:                cfg.DomainKey,
		extensions:               cfg.Extensions,
		delegation:               cfg.Delegation,
		done:                     make(chan struct{}),
		pushConfigs:              make(map[string]PushNotificationConfig),
		pushGuard:                newPushGuard(cfg.PushCallbackAllowlist),
		allowWildcardPushConfigs: cfg.AllowWildcardPushCallbacks,
	}
	s.pushClient = s.pushHTTPClient()

	if handler != nil {
		handler.SetPushNotifier(s.firePushNotifications)
	}

	meta := handler.WorkspaceMetadata()
	s.card = AgentCard{
		Name:              "ggcode",
		Description:       fmt.Sprintf("AI coding agent for %s", meta.ProjName),
		Version:           "1.0.0",
		ProtocolReversion: A2AProtocolVersion,
		Provider: &AgentProvider{
			URL:          "https://github.com/topcheer/ggcode",
			Organization: "topcheer",
		},
		Capabilities: AgentCapabilities{
			Streaming:         true,
			PushNotifications: cfg.PushNotifications == nil || *cfg.PushNotifications,
			Extensions:        cfg.Extensions,
		},
		Extensions:         cfg.Extensions,
		DefaultInputModes:  []string{"text/plain"},
		DefaultOutputModes: []string{"text/plain"},
		Skills:             DefaultSkills(),
		Metadata:           meta,
	}

	s.rebuildSecuritySchemes()

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/agent.json", s.a2aMiddleware(s.handleAgentCard))
	mux.HandleFunc("/.well-known/a2a.json", s.a2aMiddleware(s.handleAgentCard))
	mux.HandleFunc("/.well-known/agent-card.json", s.a2aMiddleware(s.handleAgentCard))
	mux.HandleFunc("/", s.a2aMiddleware(s.handleRPC))
	s.mux = mux

	host := cfg.Host
	if host == "" {
		host = "0.0.0.0"
	}

	s.server = &http.Server{
		Addr:         fmt.Sprintf("%s:%d", host, cfg.Port),
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0,
		BaseContext: func(_ net.Listener) context.Context {
			return context.Background()
		},
	}

	return s
}

func (s *Server) Start() error {
	ln, err := net.Listen("tcp", s.server.Addr)
	if err != nil {
		return fmt.Errorf("a2a listen: %w", err)
	}

	if s.tlsConfig != nil {
		ln = tls.NewListener(ln, s.tlsConfig)
	}

	s.port = ln.Addr().(*net.TCPAddr).Port

	scheme := "http"
	if s.tlsConfig != nil {
		scheme = "https"
	}
	addr := ln.Addr().String()
	host, port, _ := net.SplitHostPort(addr)
	if host == "0.0.0.0" || host == "::" {
		host = PreferredIP()
	}
	s.cardMu.Lock()
	s.card.URL = fmt.Sprintf("%s://%s:%s", scheme, host, port)
	cardURL := s.card.URL
	s.cardMu.Unlock()

	safego.Go("a2a.server.serve", func() {
		if err := s.server.Serve(ln); err != nil && err != http.ErrServerClosed {
			debug.Log("a2a", "server error: %v", err)
		}
		s.doneOnce.Do(func() { close(s.done) })
	})

	debug.Log("a2a", "server listening on %s (card: %s/.well-known/agent.json)",
		ln.Addr().String(), cardURL)
	return nil
}

func (s *Server) Port() int { return s.port }

func (s *Server) Mux() *http.ServeMux { return s.mux }

func (s *Server) APIKey() string {
	if len(s.apiKeys) > 0 {
		return s.apiKeys[0]
	}
	return ""
}

func (s *Server) Endpoint() string {
	s.cardMu.RLock()
	defer s.cardMu.RUnlock()
	return s.card.URL
}

func (s *Server) AgentCard() AgentCard {
	s.cardMu.RLock()
	defer s.cardMu.RUnlock()
	return s.card
}

func (s *Server) Stop() {
	s.doneOnce.Do(func() { close(s.done) })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = s.server.Shutdown(ctx)
	<-s.done
}

const A2AProtocolVersion = "1.0"

type activatedExtensionsKey struct{}

func ActivatedExtensions(ctx context.Context) []string {
	if v, ok := ctx.Value(activatedExtensionsKey{}).([]string); ok {
		return v
	}
	return nil
}

func (s *Server) a2aMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("A2A-Version", A2AProtocolVersion)
		activated := s.activateExtensions(ParseA2AExtensionsHeader(r.Header.Get(A2AExtensionsHeader)))
		if len(activated) > 0 {
			w.Header().Set(A2AExtensionsHeader, FormatA2AExtensionsHeader(activated))
		}
		next(w, r.WithContext(context.WithValue(r.Context(), activatedExtensionsKey{}, activated)))
	}
}

func (s *Server) activateExtensions(requested []string) []string {
	if len(requested) == 0 || len(s.extensions) == 0 {
		return nil
	}
	declared := make(map[string]struct{}, len(s.extensions))
	for _, ext := range s.extensions {
		declared[ext.URI] = struct{}{}
	}
	var activated []string
	for _, uri := range requested {
		if _, ok := declared[uri]; ok {
			activated = append(activated, uri)
		}
	}
	return activated
}

func (s *Server) missingRequired(activated []string) []string {
	if len(s.extensions) == 0 {
		return nil
	}
	have := make(map[string]struct{}, len(activated))
	for _, uri := range activated {
		have[uri] = struct{}{}
	}
	var missing []string
	for _, ext := range s.extensions {
		if ext.Required {
			if _, ok := have[ext.URI]; !ok {
				missing = append(missing, ext.URI)
			}
		}
	}
	return missing
}

func (s *Server) handleAgentCard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", jsonRPCContentType(w))
	s.cardMu.RLock()
	cardCopy := s.card
	s.cardMu.RUnlock()
	json.NewEncoder(w).Encode(cardCopy)
}

func (s *Server) handleRPC(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if !s.authenticate(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	if prefersA2AJSON(r) {
		w = &rpcWriter{ResponseWriter: w, respCT: "application/a2a+json"}
	}

	if missing := s.missingRequired(ActivatedExtensions(r.Context())); len(missing) > 0 {
		writeRPCError(w, nil, &JSONRPCError{
			Code:    ErrUnsupportedOp.Code,
			Message: ErrUnsupportedOp.Message,
			Data:    fmt.Sprintf("required A2A extension(s) not activated: %s", strings.Join(missing, ", ")),
		})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
	body, err := util.ReadAll(r.Body, util.ReadLimitGeneral)
	if err != nil {
		writeRPCError(w, nil, ErrParseError)
		return
	}

	var req JSONRPCRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeRPCError(w, nil, ErrParseError)
		return
	}

	if req.JSONRPC != "2.0" {
		writeRPCError(w, req.ID, ErrInvalidRequest)
		return
	}

	s.routeRPC(w, r, &req)
}

func (s *Server) authenticate(r *http.Request) bool {
	if len(s.apiKeys) > 0 {
		if provided := r.Header.Get("X-API-Key"); provided != "" {
			for _, key := range s.apiKeys {
				if subtle.ConstantTimeCompare([]byte(provided), []byte(key)) == 1 {
					return true
				}
			}
		}
	}

	if authHeader := r.Header.Get("Authorization"); strings.HasPrefix(authHeader, "Bearer ") {
		token := strings.TrimPrefix(authHeader, "Bearer ")
		if s.tokenValidator != nil {
			_, err := s.tokenValidator.ValidateToken(r.Context(), token)
			if err == nil {
				return true
			}
		}
		if s.tokenValidator == nil {
			return false
		}
	}

	if s.mtlsEnabled {
		if r.TLS != nil && len(r.TLS.PeerCertificates) > 0 {
			return true
		}
		return false
	}

	if len(s.apiKeys) == 0 && s.tokenValidator == nil && !s.mtlsEnabled {
		return true
	}

	return false
}

func (s *Server) routeRPC(w http.ResponseWriter, r *http.Request, req *JSONRPCRequest) {
	// #5575: runtime/* is a gated additive namespace. With introspection
	// off (or no injected source) the request falls through to the default
	// arm below, so the method-not-found error is byte-identical to today.
	if strings.HasPrefix(req.Method, "runtime/") && s.introspectionMode != IntrospectionOff && s.introspector != nil {
		s.handleRuntimeRPC(w, r, req)
		return
	}
	switch req.Method {
	case "message/send":
		s.handleMessageSend(w, r, req)
	case "message/stream":
		s.handleMessageStream(w, r, req)
	case "tasks/get":
		s.handleTaskGet(w, req)
	case "tasks/list":
		s.handleTaskList(w, req)
	case "tasks/cancel":
		s.handleTaskCancel(w, req)
	case "tasks/resubscribe":
		s.handleTaskResubscribe(w, r, req)
	case "tasks/pushNotificationConfig/set":
		s.handlePushConfigSet(w, req)
	case "tasks/pushNotificationConfig/get":
		s.handlePushConfigGet(w, req)
	case "tasks/pushNotificationConfig/list":
		s.handlePushConfigList(w, req)
	case "tasks/pushNotificationConfig/delete":
		s.handlePushConfigDelete(w, req)
	case "agent/getExtendedCard":
		s.handleGetExtendedCard(w, req)
	default:
		writeRPCError(w, req.ID, ErrMethodNotFound)
	}
}

// handleRuntimeRPC serves the runtime/* namespace. The legacy auth
// middleware has already run (first gate, unchanged); the workspace domain
// HMAC proof is the second factor stacked only here (#5575).
func (s *Server) handleRuntimeRPC(w http.ResponseWriter, r *http.Request, req *JSONRPCRequest) {
	nonce := r.Header.Get("X-A2A-Introspection-Nonce")
	sig := r.Header.Get("X-A2A-Introspection-Signature")
	if !VerifyIntrospectionProof(s.domainKey, req.Method, nonce, sig) {
		writeRPCError(w, req.ID, ErrIntrospectionDenied)
		return
	}
	handle := NewRuntimeSnapshotHandler(s.introspector, s.introspectionMode, time.Now)
	result, rpcErr := handle(req.Method, nonce, sig)
	if rpcErr != nil {
		writeRPCError(w, req.ID, rpcErr)
		return
	}
	writeRPCResult(w, req.ID, result)
}

func (s *Server) handleMessageSend(w http.ResponseWriter, r *http.Request, req *JSONRPCRequest) {
	var params SendMessageParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeRPCError(w, req.ID, ErrInvalidParams)
		return
	}
	if !s.checkDelegation(w, req.ID, &params.Message) {
		return
	}

	if params.Configuration != nil && len(params.Configuration.AcceptedOutputModes) > 0 {
		supported := map[string]bool{"text/plain": true, "text/markdown": true, "application/json": true}
		found := false
		for _, mode := range params.Configuration.AcceptedOutputModes {
			if supported[mode] {
				found = true
				break
			}
		}
		if !found {
			writeRPCError(w, req.ID, ErrUnsupportedMode)
			return
		}
	}

	task, err := s.handler.Handle(r.Context(), params.Skill, params.Message, params.TaskID)
	if err != nil {
		writeRPCError(w, req.ID, &JSONRPCError{
			Code:    -32000,
			Message: err.Error(),
		})
		return
	}

	done := s.handler.GetTaskDone(task.ID)
	if done == nil {
		writeTaskResultOrNotFound(w, req.ID, s.handler, task.ID)
		return
	}

	timeout := s.handler.Timeout()
	if timeout == 0 {
		timeout = 5 * time.Minute
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-done:
		writeTaskResultOrNotFound(w, req.ID, s.handler, task.ID)
	case <-timer.C:
		writeRPCError(w, req.ID, &JSONRPCError{
			Code:    -32060,
			Message: "task timed out",
			Data:    fmt.Sprintf("task %s: use tasks/get to check status", task.ID),
		})
	case <-r.Context().Done():
		debug.Log("a2a", "message/send: client disconnected for task %s; task continues in background", task.ID)
	}
}

func (s *Server) handleMessageStream(w http.ResponseWriter, r *http.Request, req *JSONRPCRequest) {
	var params SendMessageParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeRPCError(w, req.ID, ErrInvalidParams)
		return
	}
	if !s.checkDelegation(w, req.ID, &params.Message) {
		return
	}

	if params.Configuration != nil && len(params.Configuration.AcceptedOutputModes) > 0 {
		supported := map[string]bool{"text/plain": true, "text/markdown": true, "application/json": true}
		found := false
		for _, mode := range params.Configuration.AcceptedOutputModes {
			if supported[mode] {
				found = true
				break
			}
		}
		if !found {
			writeRPCError(w, req.ID, ErrUnsupportedMode)
			return
		}
	}

	task, err := s.handler.Handle(r.Context(), params.Skill, params.Message, params.TaskID)
	if err != nil {
		writeRPCError(w, req.ID, &JSONRPCError{
			Code:    -32000,
			Message: err.Error(),
		})
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeRPCResult(w, req.ID, task)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	t, ok := s.getTaskOrSSEError(w, flusher, req.ID, task.ID)
	if !ok {
		return
	}
	if t.Status.State.IsTerminal() {
		for _, art := range t.Artifacts {
			s.sendSSE(w, flusher, req.ID, TaskArtifactUpdateEvent{
				TaskID:    t.ID,
				Artifact:  art,
				LastChunk: true,
			})
		}
		s.sendSSE(w, flusher, req.ID, TaskStatusUpdateEvent{TaskID: t.ID, Status: t.Status, Final: true})
		return
	}
	s.sendSSE(w, flusher, req.ID, TaskStatusUpdateEvent{
		TaskID: task.ID,
		Status: t.Status,
		Final:  false,
	})

	timeout := s.handler.Timeout()
	if timeout == 0 {
		timeout = 5 * time.Minute
	}
	done := s.handler.GetTaskDone(task.ID)
	if done == nil {
		t, ok := s.getTaskOrSSEError(w, flusher, req.ID, task.ID)
		if !ok {
			return
		}
		for _, art := range t.Artifacts {
			s.sendSSE(w, flusher, req.ID, TaskArtifactUpdateEvent{
				TaskID:    t.ID,
				Artifact:  art,
				LastChunk: true,
			})
		}
		s.sendSSE(w, flusher, req.ID, TaskStatusUpdateEvent{TaskID: t.ID, Status: t.Status, Final: t.Status.State.IsTerminal()})
		return
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-done:
		t, ok := s.getTaskOrSSEError(w, flusher, req.ID, task.ID)
		if !ok {
			return
		}
		for _, art := range t.Artifacts {
			s.sendSSE(w, flusher, req.ID, TaskArtifactUpdateEvent{
				TaskID:    t.ID,
				Artifact:  art,
				LastChunk: true,
			})
		}
		s.sendSSE(w, flusher, req.ID, TaskStatusUpdateEvent{TaskID: t.ID, Status: t.Status, Final: t.Status.State.IsTerminal()})
	case <-timer.C:
		s.sendSSEError(w, flusher, req.ID, -32060, "task timed out")
	case <-r.Context().Done():
		debug.Log("a2a", "message/stream: client disconnected for task %s; stream events during disconnect are not replayed", task.ID)
	}
}

func (s *Server) handleTaskGet(w http.ResponseWriter, req *JSONRPCRequest) {
	var params GetTaskParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeRPCError(w, req.ID, ErrInvalidParams)
		return
	}

	task, ok := s.handler.GetTask(params.ID)
	if !ok {
		writeRPCError(w, req.ID, ErrTaskNotFound)
		return
	}

	if params.HistoryLength != nil && *params.HistoryLength >= 0 && len(task.History) > *params.HistoryLength {
		snapshot := task.Snapshot()
		start := len(snapshot.History) - *params.HistoryLength
		snapshot.History = snapshot.History[start:]
		writeRPCResult(w, req.ID, snapshot)
		return
	}

	writeRPCResult(w, req.ID, task)
}

func (s *Server) handleTaskList(w http.ResponseWriter, req *JSONRPCRequest) {
	var params struct {
		PageToken string `json:"pageToken,omitempty"`
		PageSize  int    `json:"pageSize,omitempty"`
	}
	if req.Params != nil {
		_ = json.Unmarshal(req.Params, &params)
	}
	if params.PageSize <= 0 {
		params.PageSize = 50
	}
	if params.PageSize > 100 {
		params.PageSize = 100
	}

	tasks, nextToken, err := s.handler.ListTasks(params.PageToken, params.PageSize)
	if err != nil {
		writeRPCError(w, req.ID, ErrInvalidParams)
		return
	}
	writeRPCResult(w, req.ID, map[string]interface{}{
		"tasks":     tasks,
		"nextToken": nextToken,
	})
}

func (s *Server) handleTaskCancel(w http.ResponseWriter, req *JSONRPCRequest) {
	var params CancelTaskParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeRPCError(w, req.ID, ErrInvalidParams)
		return
	}

	if _, ok := s.handler.GetTask(params.ID); !ok {
		writeRPCError(w, req.ID, ErrTaskNotFound)
		return
	}

	if err := s.handler.CancelTask(params.ID); err != nil {
		writeRPCError(w, req.ID, &JSONRPCError{
			Code:    -32002,
			Message: err.Error(),
		})
		return
	}

	result, ok := s.handler.GetTask(params.ID)
	if !ok {
		writeRPCError(w, req.ID, ErrTaskNotFound)
		return
	}
	writeRPCResult(w, req.ID, result)
}

func (s *Server) handleTaskResubscribe(w http.ResponseWriter, r *http.Request, req *JSONRPCRequest) {
	var params TaskSubscriptionParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeRPCError(w, req.ID, ErrInvalidParams)
		return
	}

	task, ok := s.handler.GetTask(params.ID)
	if !ok {
		writeRPCError(w, req.ID, ErrTaskNotFound)
		return
	}

	if task.Status.IsTerminal() {
		writeRPCResult(w, req.ID, task)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeRPCResult(w, req.ID, task)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	s.sendSSE(w, flusher, req.ID, TaskStatusUpdateEvent{TaskID: task.ID, Status: task.Status, Final: task.Status.State.IsTerminal()})

	done := s.handler.GetTaskDone(params.ID)
	if done == nil {
		t, ok := s.handler.GetTask(params.ID)
		if !ok {
			return
		}
		for _, art := range t.Artifacts {
			s.sendSSE(w, flusher, req.ID, TaskArtifactUpdateEvent{
				TaskID:    t.ID,
				Artifact:  art,
				LastChunk: true,
			})
		}
		s.sendSSE(w, flusher, req.ID, TaskStatusUpdateEvent{TaskID: t.ID, Status: t.Status, Final: t.Status.State.IsTerminal()})
		return
	}

	timeout := s.handler.Timeout()
	if timeout == 0 {
		timeout = 5 * time.Minute
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-done:
		t, ok := s.handler.GetTask(params.ID)
		if !ok {
			return
		}
		for _, art := range t.Artifacts {
			s.sendSSE(w, flusher, req.ID, TaskArtifactUpdateEvent{
				TaskID:    t.ID,
				Artifact:  art,
				LastChunk: true,
			})
		}
		s.sendSSE(w, flusher, req.ID, TaskStatusUpdateEvent{TaskID: t.ID, Status: t.Status, Final: t.Status.State.IsTerminal()})
	case <-timer.C:
		s.sendSSEError(w, flusher, req.ID, -32060, "task timed out")
	case <-r.Context().Done():
		debug.Log("a2a", "tasks/resubscribe: client disconnected for task %s; events during disconnect are not replayed", params.ID)
	}
}

func (s *Server) sendSSE(w io.Writer, flusher http.Flusher, id json.RawMessage, data interface{}) {
	payload, _ := json.Marshal(JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  data,
	})
	fmt.Fprintf(w, "data: %s\n\n", payload)
	flusher.Flush()
}

func (s *Server) sendSSEError(w io.Writer, flusher http.Flusher, id json.RawMessage, code int, message string) {
	payload, _ := json.Marshal(JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &JSONRPCError{Code: code, Message: message},
	})
	fmt.Fprintf(w, "data: %s\n\n", payload)
	flusher.Flush()
}

func (s *Server) handlePushConfigSet(w http.ResponseWriter, req *JSONRPCRequest) {
	if reason := s.pushRegistrationDisabled(); reason != "" {
		writeRPCError(w, req.ID, &JSONRPCError{
			Code:    ErrPushAuthNotConfigured.Code,
			Message: ErrPushAuthNotConfigured.Message,
			Data:    "push notifications disabled: " + reason + "; set a2a.auth.api_key to a real secret (or configure oauth2/mTLS) to enable",
		})
		return
	}
	var cfg PushNotificationConfig
	if err := json.Unmarshal(req.Params, &cfg); err != nil {
		writeRPCError(w, req.ID, ErrInvalidParams)
		return
	}
	if cfg.ID == "" {
		cfg.ID = fmt.Sprintf("push-%d", time.Now().UnixNano())
	}
	if cfg.TaskID == "" && !s.allowWildcardPushConfigs {
		writeRPCError(w, req.ID, &JSONRPCError{
			Code:    ErrInvalidParams.Code,
			Message: ErrInvalidParams.Message,
			Data:    "push config with empty taskId matches ALL tasks and requires explicit operator opt-in (AllowWildcardPushCallbacks)",
		})
		return
	}
	if err := s.validatePushCallbackURL(cfg.URL); err != nil {
		debug.Log("a2a.push", "rejected callback URL %q: %v", cfg.URL, err)
		writeRPCError(w, req.ID, &JSONRPCError{
			Code:    ErrInvalidParams.Code,
			Message: ErrInvalidParams.Message,
			Data:    "invalid push callback url: " + err.Error(),
		})
		return
	}
	s.pushMu.Lock()
	if _, exists := s.pushConfigs[cfg.ID]; !exists && len(s.pushConfigs) >= maxPushConfigs {
		s.pushMu.Unlock()
		debug.Log("a2a.push", "push config registry full (%d entries), rejecting id=%s", maxPushConfigs, cfg.ID)
		writeRPCError(w, req.ID, &JSONRPCError{
			Code:    ErrInvalidParams.Code,
			Message: ErrInvalidParams.Message,
			Data:    fmt.Sprintf("push config registry full (%d entries); delete stale configs before registering new ones", maxPushConfigs),
		})
		return
	}
	s.pushConfigs[cfg.ID] = cfg
	s.pushMu.Unlock()
	writeRPCResult(w, req.ID, cfg)
}

func (s *Server) handlePushConfigGet(w http.ResponseWriter, req *JSONRPCRequest) {
	var params struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeRPCError(w, req.ID, ErrInvalidParams)
		return
	}
	s.pushMu.RLock()
	cfg, ok := s.pushConfigs[params.ID]
	s.pushMu.RUnlock()
	if !ok {
		writeRPCError(w, req.ID, &JSONRPCError{
			Code:    -32602,
			Message: "Invalid params",
			Data:    fmt.Sprintf("push config not found: %s", params.ID),
		})
		return
	}
	writeRPCResult(w, req.ID, cfg)
}

func (s *Server) handlePushConfigList(w http.ResponseWriter, req *JSONRPCRequest) {
	s.pushMu.RLock()
	configs := make([]PushNotificationConfig, 0, len(s.pushConfigs))
	for _, cfg := range s.pushConfigs {
		configs = append(configs, cfg)
	}
	s.pushMu.RUnlock()
	writeRPCResult(w, req.ID, configs)
}

func (s *Server) handlePushConfigDelete(w http.ResponseWriter, req *JSONRPCRequest) {
	var params struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeRPCError(w, req.ID, ErrInvalidParams)
		return
	}
	s.pushMu.Lock()
	delete(s.pushConfigs, params.ID)
	s.pushMu.Unlock()
	writeRPCResult(w, req.ID, map[string]string{"status": "ok"})
}

func (s *Server) handleGetExtendedCard(w http.ResponseWriter, req *JSONRPCRequest) {
	s.cardMu.RLock()
	card := s.extendedCard
	s.cardMu.RUnlock()
	if len(card) == 0 {
		writeRPCError(w, req.ID, ErrExtendedCardNotConfigured)
		return
	}
	var result interface{}
	json.Unmarshal(card, &result)
	writeRPCResult(w, req.ID, result)
}

func (s *Server) SetExtendedCard(card json.RawMessage) {
	s.cardMu.Lock()
	s.extendedCard = card
	if len(card) > 0 {
		s.card.Capabilities.ExtendedAgentCard = true
	}
	s.cardMu.Unlock()
}

func (s *Server) SetSkills(skills []Skill) {
	if len(skills) == 0 {
		return
	}
	s.cardMu.Lock()
	s.card.Skills = skills
	s.cardMu.Unlock()
}

func (s *Server) SetTokenValidator(v *auth.TokenValidator) {
	s.tokenValidator = v
	s.rebuildSecuritySchemes()
}

func (s *Server) SetMTLSEnabled(enabled bool) {
	s.mtlsEnabled = enabled
	s.rebuildSecuritySchemes()
}

func (s *Server) SetTLSConfig(tlsCfg *tls.Config) {
	s.tlsConfig = tlsCfg
	s.mtlsEnabled = true
	s.rebuildSecuritySchemes()
}

func (s *Server) rebuildSecuritySchemes() {
	schemes := map[string]Security{}
	var security []map[string][]string

	if len(s.apiKeys) > 0 {
		schemes["apiKey"] = Security{
			Type:        "apiKey",
			Location:    "header",
			Name:        "X-API-Key",
			Description: "API key authentication",
		}
		security = append(security, map[string][]string{"apiKey": {}})
	}

	if s.tokenValidator != nil {
		schemes["bearer"] = Security{
			Type:        "http",
			Scheme:      "bearer",
			Description: "Bearer token authentication (OAuth2 / OIDC)",
		}
		security = append(security, map[string][]string{"bearer": {}})
	}

	if s.mtlsEnabled {
		schemes["mutualTLS"] = Security{
			Type:        "mutualTLS",
			Description: "Mutual TLS certificate authentication",
		}
		security = append(security, map[string][]string{"mutualTLS": {}})
	}

	s.cardMu.Lock()
	s.card.SecuritySchemes = schemes
	s.card.Security = security
	s.cardMu.Unlock()
}

func (s *Server) SetHandler(h *TaskHandler) {
	s.handler = h
	if h != nil {
		h.SetPushNotifier(s.firePushNotifications)
	}
}

func (s *Server) firePushNotifications(taskID string, payload StreamResponse) {
	s.pushMu.RLock()
	configs := make([]PushNotificationConfig, 0)
	for _, cfg := range s.pushConfigs {
		if cfg.TaskID == "" || cfg.TaskID == taskID {
			configs = append(configs, cfg)
		}
	}
	s.pushMu.RUnlock()

	deliverable := configs[:0:0]
	for _, cfg := range configs {
		if err := s.validatePushCallbackURL(cfg.URL); err != nil {
			debug.Log("a2a.push", "skipping delivery to unverified callback %q: %v", cfg.URL, err)
			continue
		}
		deliverable = append(deliverable, cfg)
	}
	configs = deliverable

	body, err := json.Marshal(payload)
	if err != nil {
		debug.Log("a2a", "push marshal error: %v", err)
		return
	}

	for _, cfg := range configs {
		s.pushMu.RLock()
		live, ok := s.pushConfigs[cfg.ID]
		configCopy := live
		s.pushMu.RUnlock()
		if !ok {
			continue
		}
		if configCopy.URL != cfg.URL {
			if err := s.validatePushCallbackURL(configCopy.URL); err != nil {
				debug.Log("a2a.push", "live callback for %s changed to unverified url, skipping: %v", cfg.ID, err)
				continue
			}
		}

		if configCopy.Disabled {
			debug.Log("a2a.push", "skipping disabled config %s", configCopy.ID)
			continue
		}

		now := time.Now()
		if now.Before(configCopy.NextDeliveryAfter) {
			debug.Log("a2a.push", "skipping config %s (backoff until %v)", configCopy.ID, configCopy.NextDeliveryAfter)
			continue
		}

		url := configCopy.URL
		token := configCopy.Token
		configID := configCopy.ID

		safego.Go("a2a.pushNotify", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
			if err != nil {
				s.recordPushFailure(configID, err.Error())
				debug.Log("a2a", "push request error: %v", err)
				return
			}
			req.Header.Set("Content-Type", "application/json")
			if token != "" {
				req.Header.Set("Authorization", "Bearer "+token)
			}
			resp, err := s.pushClient.Do(req)
			if err != nil {
				s.recordPushFailure(configID, err.Error())
				debug.Log("a2a", "push delivery error: %v", err)
				return
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode >= 400 {
				s.recordPushFailure(configID, fmt.Sprintf("HTTP %d", resp.StatusCode))
				debug.Log("a2a", "push to %s failed: HTTP %d", url, resp.StatusCode)
			} else {
				s.recordPushSuccess(configID)
				debug.Log("a2a", "push delivered to %s: %d", url, resp.StatusCode)
			}
		})
	}
}

func (s *Server) getTaskOrSSEError(w http.ResponseWriter, flusher http.Flusher, reqID json.RawMessage, taskID string) (*Task, bool) {
	t, ok := s.handler.GetTask(taskID)
	if !ok {
		s.sendSSEError(w, flusher, reqID, ErrTaskNotFound.Code, ErrTaskNotFound.Message)
		return nil, false
	}
	return t, true
}

func writeTaskResultOrNotFound(w http.ResponseWriter, id json.RawMessage, h *TaskHandler, taskID string) {
	if t, ok := h.GetTask(taskID); ok {
		writeRPCResult(w, id, t)
		return
	}
	writeRPCError(w, id, ErrTaskNotFound)
}

type rpcWriter struct {
	http.ResponseWriter
	respCT string
}

func (rw *rpcWriter) Flush() {
	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func jsonRPCContentType(w http.ResponseWriter) string {
	if rw, ok := w.(*rpcWriter); ok && rw.respCT != "" {
		return rw.respCT
	}
	return "application/json"
}

func prefersA2AJSON(r *http.Request) bool {
	for _, h := range []string{"Content-Type", "Accept"} {
		if v := strings.ToLower(r.Header.Get(h)); strings.Contains(v, "a2a+json") {
			return true
		}
	}
	return false
}

func writeRPCResult(w http.ResponseWriter, id json.RawMessage, result interface{}) {
	w.Header().Set("Content-Type", jsonRPCContentType(w))
	json.NewEncoder(w).Encode(JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      normalizeResponseID(id),
		Result:  result,
	})
}

func writeRPCError(w http.ResponseWriter, id json.RawMessage, rpcErr *JSONRPCError) {
	w.Header().Set("Content-Type", jsonRPCContentType(w))
	json.NewEncoder(w).Encode(JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      normalizeResponseID(id),
		Error:   rpcErr,
	})
}

const (
	maxPushFailuresBeforeDisable = 5
	initialPushBackoff           = 10 * time.Second
	maxPushBackoff               = 10 * time.Minute
)

func (s *Server) recordPushFailure(configID string, reason string) {
	s.pushMu.Lock()
	defer s.pushMu.Unlock()
	cfg, ok := s.pushConfigs[configID]
	if !ok {
		return
	}
	cfg.ConsecutiveFailures++
	if cfg.ConsecutiveFailures >= maxPushFailuresBeforeDisable {
		cfg.Disabled = true
		debug.Log("a2a.push", "disabled push config %s after %d failures (last: %s)",
			configID, cfg.ConsecutiveFailures, reason)
	} else {
		backoff := time.Duration(int(initialPushBackoff.Seconds())<<cfg.ConsecutiveFailures) * time.Second
		if backoff > maxPushBackoff {
			backoff = maxPushBackoff
		}
		cfg.NextDeliveryAfter = time.Now().Add(backoff)
		debug.Log("a2a.push", "push config %s failure %d/%d, backoff %v (reason: %s)",
			configID, cfg.ConsecutiveFailures, maxPushFailuresBeforeDisable, backoff, reason)
	}
	s.pushConfigs[configID] = cfg
}

func (s *Server) recordPushSuccess(configID string) {
	s.pushMu.Lock()
	defer s.pushMu.Unlock()
	cfg, ok := s.pushConfigs[configID]
	if !ok {
		return
	}
	wasDisabled := cfg.Disabled
	cfg.ConsecutiveFailures = 0
	cfg.NextDeliveryAfter = time.Time{}
	cfg.Disabled = false
	s.pushConfigs[configID] = cfg
	if wasDisabled {
		debug.Log("a2a.push", "re-enabled push config %s on successful delivery", configID)
	}
}

func normalizeResponseID(id json.RawMessage) json.RawMessage {
	if len(id) == 0 {
		return json.RawMessage(`null`)
	}
	return id
}

// SetDomainKey installs the workspace domain key post-construction (the
// assembly layer resolves it from the working dir after server creation).
func (s *Server) SetDomainKey(key []byte) { s.domainKey = key }
