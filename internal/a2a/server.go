package a2a

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
)

// Server is an A2A protocol server that handles JSON-RPC requests over HTTP.
type Server struct {
	handler      *TaskHandler
	card         AgentCard
	extendedCard json.RawMessage // optional extended agent card
	apiKey       string
	server       *http.Server
	port         int
	done         chan struct{}
	pushConfigs  map[string]PushNotificationConfig // by ID
	pushMu       sync.RWMutex
}

// ServerConfig holds A2A server configuration.
type ServerConfig struct {
	Host     string // bind address (default "127.0.0.1")
	Port     int    // 0 = auto-assign
	APIKey   string // empty = no auth
	Instance string // instance identifier
}

// NewServer creates a new A2A server.
func NewServer(cfg ServerConfig, handler *TaskHandler) *Server {
	s := &Server{
		handler:     handler,
		apiKey:      cfg.APIKey,
		done:        make(chan struct{}),
		pushConfigs: make(map[string]PushNotificationConfig),
	}

	// Wire push notification callbacks: handler → server.firePushNotifications.
	if handler != nil {
		handler.SetPushNotifier(s.firePushNotifications)
	}

	// Build Agent Card.
	meta := handler.WorkspaceMetadata()
	s.card = AgentCard{
		Name:        "ggcode",
		Description: fmt.Sprintf("AI coding agent for %s", meta.ProjName),
		Version:     "1.0.0",
		Provider: &AgentProvider{
			URL:          "https://github.com/topcheer/ggcode",
			Organization: "topcheer",
		},
		Capabilities: AgentCapabilities{
			Streaming:         true,
			PushNotifications: false,
		},
		DefaultInputModes:  []string{"text/plain"},
		DefaultOutputModes: []string{"text/plain"},
		Skills:             DefaultSkills(),
		Metadata:           meta,
	}
	if cfg.APIKey != "" {
		s.card.SecuritySchemes = map[string]Security{
			"apiKey": {
				Type:        "apiKey",
				Location:    "header",
				Name:        "X-API-Key",
				Description: "API key authentication",
			},
		}
		s.card.Security = []map[string][]string{
			{"apiKey": {}},
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/agent.json", s.handleAgentCard)
	mux.HandleFunc("/.well-known/a2a.json", s.handleAgentCard)
	mux.HandleFunc("/", s.a2aMiddleware(s.handleRPC))

	host := cfg.Host
	if host == "" {
		host = "127.0.0.1"
	}

	s.server = &http.Server{
		Addr:    fmt.Sprintf("%s:%d", host, cfg.Port),
		Handler: mux,
		BaseContext: func(l net.Listener) context.Context {
			s.port = l.Addr().(*net.TCPAddr).Port
			return context.Background()
		},
	}

	return s
}

// Start starts the HTTP server. If port is 0, a random port is assigned.
func (s *Server) Start() error {
	ln, err := net.Listen("tcp", s.server.Addr)
	if err != nil {
		return fmt.Errorf("a2a listen: %w", err)
	}
	s.port = ln.Addr().(*net.TCPAddr).Port

	// Update the card URL with the actual port.
	s.card.URL = fmt.Sprintf("http://%s", ln.Addr().String())

	safego.Go("a2a.server.serve", func() {
		if err := s.server.Serve(ln); err != nil && err != http.ErrServerClosed {
			debug.Log("a2a", "server error: %v", err)
		}
		close(s.done)
	})

	debug.Log("a2a", "server listening on %s (card: %s/.well-known/agent.json)",
		ln.Addr().String(), s.card.URL)
	return nil
}

// Port returns the actual port (only valid after Start).
func (s *Server) Port() int { return s.port }

// Endpoint returns the base URL of the server.
func (s *Server) Endpoint() string { return s.card.URL }

// AgentCard returns a copy of the current agent card.
func (s *Server) AgentCard() AgentCard { return s.card }

// Stop gracefully shuts down the server.
func (s *Server) Stop() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = s.server.Shutdown(ctx)
	<-s.done
}

// ---------------------------------------------------------------------------
// HTTP handlers
// ---------------------------------------------------------------------------

// A2AProtocolVersion is the implemented A2A protocol version.
const A2AProtocolVersion = "1.0"

// a2aMiddleware adds A2A protocol headers to all JSON-RPC responses.
func (s *Server) a2aMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("A2A-Version", A2AProtocolVersion)
		next(w, r)
	}
}

func (s *Server) handleAgentCard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.card)
}

func (s *Server) handleRPC(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Auth check.
	if !s.authenticate(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Cap body to prevent OOM via huge Content-Length. 4 MiB is enough for
	// even very large JSON-RPC payloads with embedded artifacts.
	r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
	body, err := io.ReadAll(r.Body)
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
	if s.apiKey == "" {
		return true // no auth required
	}
	provided := r.Header.Get("X-API-Key")
	if provided == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(s.apiKey)) == 1
}

func (s *Server) routeRPC(w http.ResponseWriter, r *http.Request, req *JSONRPCRequest) {
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

// ---------------------------------------------------------------------------
// JSON-RPC method handlers
// ---------------------------------------------------------------------------

func (s *Server) handleMessageSend(w http.ResponseWriter, r *http.Request, req *JSONRPCRequest) {
	var params SendMessageParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeRPCError(w, req.ID, ErrInvalidParams)
		return
	}

	// Validate acceptedOutputModes if provided.
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

	// Wait for task to reach a terminal state using notification channel.
	done := s.handler.GetTaskDone(task.ID)
	if done == nil {
		// Task already terminal (e.g., immediate rejection).
		t, _ := s.handler.GetTask(task.ID)
		writeRPCResult(w, req.ID, t)
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
		t, _ := s.handler.GetTask(task.ID)
		writeRPCResult(w, req.ID, t)
	case <-timer.C:
		writeRPCError(w, req.ID, &JSONRPCError{
			Code:    -32060,
			Message: "task timed out",
		})
	case <-r.Context().Done():
		// Client disconnected — let the task continue in background.
	}
}

func (s *Server) handleMessageStream(w http.ResponseWriter, r *http.Request, req *JSONRPCRequest) {
	var params SendMessageParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeRPCError(w, req.ID, ErrInvalidParams)
		return
	}

	// Validate acceptedOutputModes if provided.
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

	// SSE streaming response.
	flusher, ok := w.(http.Flusher)
	if !ok {
		// Fallback to sync.
		writeRPCResult(w, req.ID, task)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Send initial status.
	s.sendSSE(w, flusher, req.ID, TaskStatusUpdateEvent{
		TaskID: task.ID,
		Status: TaskStatus{State: TaskStateWorking, Timestamp: time.Now()},
		Final:  false,
	})

	// Wait for task to reach terminal state.
	timeout := s.handler.Timeout()
	if timeout == 0 {
		timeout = 5 * time.Minute
	}
	done := s.handler.GetTaskDone(task.ID)
	if done == nil {
		// Already terminal.
		t, _ := s.handler.GetTask(task.ID)
		s.sendSSE(w, flusher, req.ID, TaskStatusUpdateEvent{TaskID: t.ID, Status: t.Status, Final: t.Status.State.IsTerminal()})
		return
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-done:
		t, _ := s.handler.GetTask(task.ID)
		s.sendSSE(w, flusher, req.ID, TaskStatusUpdateEvent{TaskID: t.ID, Status: t.Status, Final: t.Status.State.IsTerminal()})
	case <-timer.C:
		s.sendSSE(w, flusher, req.ID, map[string]interface{}{
			"error": "task timed out",
		})
	case <-r.Context().Done():
		// Client disconnected.
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

	// Trim history if historyLength requested.
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

	tasks, nextToken := s.handler.ListTasks(params.PageToken, params.PageSize)
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

	// Existence check.
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

	// Fetch fresh snapshot AFTER cancellation.
	result, _ := s.handler.GetTask(params.ID)
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
		// Return JSON-RPC error. Note: this is HTTP 200 per JSON-RPC spec.
		// Client should check the JSON-RPC error field, not HTTP status.
		writeRPCError(w, req.ID, ErrTaskNotFound)
		return
	}

	// If task is already terminal, just return it immediately.
	if task.Status.IsTerminal() {
		writeRPCResult(w, req.ID, task)
		return
	}

	// Stream updates until terminal.
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeRPCResult(w, req.ID, task)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Send current state immediately.
	s.sendSSE(w, flusher, req.ID, TaskStatusUpdateEvent{TaskID: task.ID, Status: task.Status, Final: task.Status.State.IsTerminal()})

	// Wait for terminal state using notification channel.
	done := s.handler.GetTaskDone(params.ID)
	if done == nil {
		// Already terminal.
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
		t, _ := s.handler.GetTask(params.ID)
		s.sendSSE(w, flusher, req.ID, TaskStatusUpdateEvent{TaskID: t.ID, Status: t.Status, Final: t.Status.State.IsTerminal()})
	case <-timer.C:
		s.sendSSE(w, flusher, req.ID, map[string]interface{}{
			"error": "task timed out",
		})
	case <-r.Context().Done():
		// Client disconnected.
	}
}

// ---------------------------------------------------------------------------
// SSE helper
// ---------------------------------------------------------------------------

func (s *Server) sendSSE(w io.Writer, flusher http.Flusher, id json.RawMessage, data interface{}) {
	payload, _ := json.Marshal(JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  data,
	})
	fmt.Fprintf(w, "data: %s\n\n", payload)
	flusher.Flush()
}

// ---------------------------------------------------------------------------
// ---------------------------------------------------------------------------
// Push Notification Config handlers
// ---------------------------------------------------------------------------

func (s *Server) handlePushConfigSet(w http.ResponseWriter, req *JSONRPCRequest) {
	var cfg PushNotificationConfig
	if err := json.Unmarshal(req.Params, &cfg); err != nil {
		writeRPCError(w, req.ID, ErrInvalidParams)
		return
	}
	if cfg.ID == "" {
		cfg.ID = fmt.Sprintf("push-%d", time.Now().UnixNano())
	}
	s.pushMu.Lock()
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
		writeRPCError(w, req.ID, ErrTaskNotFound)
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

// ---------------------------------------------------------------------------
// Extended Agent Card handler
// ---------------------------------------------------------------------------

func (s *Server) handleGetExtendedCard(w http.ResponseWriter, req *JSONRPCRequest) {
	if len(s.extendedCard) == 0 {
		writeRPCError(w, req.ID, ErrExtendedCardNotConfigured)
		return
	}
	var result interface{}
	json.Unmarshal(s.extendedCard, &result)
	writeRPCResult(w, req.ID, result)
}

// SetExtendedCard sets the optional extended agent card content.
func (s *Server) SetExtendedCard(card json.RawMessage) {
	s.extendedCard = card
	if len(card) > 0 {
		s.card.Capabilities.ExtendedAgentCard = true
	}
}

// SetHandler connects the TaskHandler and wires push notifications.
func (s *Server) SetHandler(h *TaskHandler) {
	s.handler = h
	if h != nil {
		h.SetPushNotifier(s.firePushNotifications)
	}
}

// firePushNotifications sends HTTP POST callbacks to all registered push
// configs for the given task.
func (s *Server) firePushNotifications(taskID string, payload StreamResponse) {
	s.pushMu.RLock()
	configs := make([]PushNotificationConfig, 0)
	for _, cfg := range s.pushConfigs {
		if cfg.TaskID == "" || cfg.TaskID == taskID {
			configs = append(configs, cfg)
		}
	}
	s.pushMu.RUnlock()

	body, err := json.Marshal(payload)
	if err != nil {
		debug.Log("a2a", "push marshal error: %v", err)
		return
	}

	for _, cfg := range configs {
		go func(url, token string) {
			req, err := http.NewRequest("POST", url, bytes.NewReader(body))
			if err != nil {
				debug.Log("a2a", "push request error: %v", err)
				return
			}
			req.Header.Set("Content-Type", "application/json")
			if token != "" {
				req.Header.Set("Authorization", "Bearer "+token)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				debug.Log("a2a", "push delivery error: %v", err)
				return
			}
			resp.Body.Close()
			debug.Log("a2a", "push delivered to %s: %d", url, resp.StatusCode)
		}(cfg.URL, cfg.Token)
	}
}

// ---------------------------------------------------------------------------
// Response helpers
// ---------------------------------------------------------------------------

func writeRPCResult(w http.ResponseWriter, id json.RawMessage, result interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	})
}

func writeRPCError(w http.ResponseWriter, id json.RawMessage, rpcErr *JSONRPCError) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   rpcErr,
	})
}

const maxConcurrentTasks = 5
