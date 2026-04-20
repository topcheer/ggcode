package a2a

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

// Server is an A2A protocol server that handles JSON-RPC requests over HTTP.
type Server struct {
	handler *TaskHandler
	card    AgentCard
	apiKey  string
	server  *http.Server
	port    int
	done    chan struct{}
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
		handler: handler,
		apiKey:  cfg.APIKey,
		done:    make(chan struct{}),
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
	mux.HandleFunc("/", s.handleRPC)

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

	go func() {
		if err := s.server.Serve(ln); err != nil && err != http.ErrServerClosed {
			debug.Log("a2a", "server error: %v", err)
		}
		close(s.done)
	}()

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
		s.handleMessageSend(w, req)
	case "message/stream":
		s.handleMessageStream(w, r, req)
	case "tasks/get":
		s.handleTaskGet(w, req)
	case "tasks/cancel":
		s.handleTaskCancel(w, req)
	case "tasks/resubscribe":
		s.handleTaskResubscribe(w, r, req)
	default:
		writeRPCError(w, req.ID, ErrMethodNotFound)
	}
}

// ---------------------------------------------------------------------------
// JSON-RPC method handlers
// ---------------------------------------------------------------------------

func (s *Server) handleMessageSend(w http.ResponseWriter, req *JSONRPCRequest) {
	var params SendMessageParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeRPCError(w, req.ID, ErrInvalidParams)
		return
	}

	task, err := s.handler.Handle(context.Background(), params.Skill, params.Message, params.TaskID)
	if err != nil {
		writeRPCError(w, req.ID, &JSONRPCError{
			Code:    -32000,
			Message: err.Error(),
		})
		return
	}

	// For message/send, wait for completion (with timeout from handler config).
	timeout := s.handler.Timeout()
	if timeout == 0 {
		timeout = 5 * time.Minute
	}
	deadline := time.After(timeout)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			t, ok := s.handler.GetTask(task.ID)
			if !ok {
				writeRPCError(w, req.ID, ErrTaskNotFound)
				return
			}
			if t.Status.IsTerminal() {
				writeRPCResult(w, req.ID, t)
				return
			}
		case <-deadline:
			writeRPCError(w, req.ID, &JSONRPCError{
				Code:    -32060,
				Message: "task timed out",
			})
			return
		}
	}
}

func (s *Server) handleMessageStream(w http.ResponseWriter, r *http.Request, req *JSONRPCRequest) {
	var params SendMessageParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeRPCError(w, req.ID, ErrInvalidParams)
		return
	}

	task, err := s.handler.Handle(context.Background(), params.Skill, params.Message, params.TaskID)
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
	s.sendSSE(w, flusher, req.ID, map[string]interface{}{
		"id":     task.ID,
		"status": map[string]string{"state": string(TaskStateWorking)},
		"kind":   "task",
	})

	// Poll for completion.
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		<-ticker.C
		t, ok := s.handler.GetTask(task.ID)
		if !ok {
			s.sendSSE(w, flusher, req.ID, map[string]interface{}{
				"error": "task not found",
			})
			return
		}

		if t.Status.IsTerminal() {
			// Send final event with full task.
			s.sendSSE(w, flusher, req.ID, t)
			return
		}
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

	writeRPCResult(w, req.ID, task)
}

func (s *Server) handleTaskCancel(w http.ResponseWriter, req *JSONRPCRequest) {
	var params CancelTaskParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeRPCError(w, req.ID, ErrInvalidParams)
		return
	}

	task, ok := s.handler.GetTask(params.ID)
	if !ok {
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

	writeRPCResult(w, req.ID, task)
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
	s.sendSSE(w, flusher, req.ID, task)

	// Poll until terminal.
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		<-ticker.C
		t, ok := s.handler.GetTask(params.ID)
		if !ok {
			s.sendSSE(w, flusher, req.ID, map[string]interface{}{
				"error": "task not found",
			})
			return
		}

		if t.Status.IsTerminal() {
			s.sendSSE(w, flusher, req.ID, t)
			return
		}
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
