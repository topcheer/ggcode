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

// Server is an A2A protocol server that handles JSON-RPC requests over HTTP.
type Server struct {
	handler        *TaskHandler
	card           AgentCard
	cardMu         sync.RWMutex    // guards card AND extendedCard (read in HTTP handlers, written by setters) #565 C #1114
	extendedCard   json.RawMessage // optional extended agent card
	apiKeys        []string
	server         *http.Server
	mux            *http.ServeMux // exposed for additional route mounting
	port           int
	done           chan struct{}
	doneOnce       sync.Once                         // #1110: guarantees close(done) exactly once - Stop before Start must not block
	pushConfigs    map[string]PushNotificationConfig // by ID
	pushMu         sync.RWMutex
	tokenValidator *auth.TokenValidator // OAuth2/OIDC token validation
	mtlsEnabled    bool
	tlsConfig      *tls.Config // TLS config for mTLS (set via SetTLSConfig)

	// #715: push callback SSRF guard + wildcard opt-in.
	pushGuard                *pushGuard
	allowWildcardPushConfigs bool
	pushClient               *http.Client
}

// ServerConfig holds A2A server configuration.
type ServerConfig struct {
	Host     string   // bind address (default "0.0.0.0")
	Port     int      // 0 = auto-assign
	APIKey   string   // single key (legacy, merged into APIKeys)
	APIKeys  []string // multiple keys (any match authenticates)
	Instance string   // instance identifier

	// PushNotifications declares push support in the AgentCard (#403). The
	// push-config CRUD + fire callbacks are ALWAYS implemented; the card
	// previously hardcoded false, so spec-compliant clients never registered
	// and the feature was unreachable. Default true.
	PushNotifications *bool

	// PushCallbackAllowlist (#715) explicitly opts in callback hosts that
	// the SSRF guard rejects by default (private/loopback/link-local
	// ranges, plain http). Entries: CIDR ("10.0.0.0/8"), bare IP, or
	// hostname ("collector.lan").
	PushCallbackAllowlist []string

	// AllowWildcardPushCallbacks (#715) permits push configs whose TaskID
	// is empty — those match notifications for ALL tasks, so they are
	// refused unless explicitly opted in.
	AllowWildcardPushCallbacks bool
}

// NewServer creates a new A2A server.
func NewServer(cfg ServerConfig, handler *TaskHandler) *Server {
	// Merge single APIKey into APIKeys for unified handling.
	apiKeys := cfg.APIKeys
	if cfg.APIKey != "" {
		apiKeys = append(apiKeys, cfg.APIKey)
	}

	s := &Server{
		handler:                  handler,
		apiKeys:                  apiKeys,
		done:                     make(chan struct{}),
		pushConfigs:              make(map[string]PushNotificationConfig),
		pushGuard:                newPushGuard(cfg.PushCallbackAllowlist),
		allowWildcardPushConfigs: cfg.AllowWildcardPushCallbacks,
	}
	s.pushClient = s.pushHTTPClient()

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
			Streaming: true,
			// Card declaration must match the (always-implemented) push
			// CRUD + fire path (#403); defaults to true.
			PushNotifications: cfg.PushNotifications == nil || *cfg.PushNotifications,
		},
		DefaultInputModes:  []string{"text/plain"},
		DefaultOutputModes: []string{"text/plain"},
		Skills:             DefaultSkills(),
		Metadata:           meta,
	}

	// Build security declarations from configured auth methods.
	s.rebuildSecuritySchemes()

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/agent.json", s.a2aMiddleware(s.handleAgentCard))
	mux.HandleFunc("/.well-known/a2a.json", s.a2aMiddleware(s.handleAgentCard))
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
		WriteTimeout: 0, // no write timeout — agent tasks can stream for minutes
		BaseContext: func(_ net.Listener) context.Context {
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

	// Wrap with TLS if mTLS is configured
	if s.tlsConfig != nil {
		ln = tls.NewListener(ln, s.tlsConfig)
	}

	s.port = ln.Addr().(*net.TCPAddr).Port

	// Update the card URL with the actual port.
	// Replace 0.0.0.0 with the preferred outbound IP for LAN reachability.
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

// Port returns the actual port (only valid after Start).
func (s *Server) Port() int { return s.port }

// Mux returns the HTTP mux for additional route mounting (e.g. lanchat).
func (s *Server) Mux() *http.ServeMux { return s.mux }

// APIKey returns the primary API key (first if multiple).
func (s *Server) APIKey() string {
	if len(s.apiKeys) > 0 {
		return s.apiKeys[0]
	}
	return ""
}

// Endpoint returns the base URL of the server.
func (s *Server) Endpoint() string {
	s.cardMu.RLock()
	defer s.cardMu.RUnlock()
	return s.card.URL
}

// AgentCard returns a copy of the current agent card.
func (s *Server) AgentCard() AgentCard {
	s.cardMu.RLock()
	defer s.cardMu.RUnlock()
	return s.card
}

// Stop gracefully shuts down the server.
func (s *Server) Stop() {
	// #1110: close done unconditionally (idempotent) so Stop() returns even
	// when Start() was never called - CLI startup cleanup paths (OAuth2/OIDC/
	// mTLS validation failures in cmd/ggcode/root.go) call Stop before Start
	// and used to block forever waiting on a channel nobody closes.
	s.doneOnce.Do(func() { close(s.done) })
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
	// #565 C: copy under read lock — setters can run concurrently (hot config).
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

	// Auth check.
	if !s.authenticate(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Cap body to prevent OOM via huge Content-Length. 4 MiB is enough for
	// even very large JSON-RPC payloads with embedded artifacts.
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
	// 1) API Key. A PRESENT-but-wrong key must NOT short-circuit the other
	// schemes (#404): instances commonly configure apiKeys AND OIDC/mTLS
	// together, and client SDKs may send both credential kinds at once.
	// Fall through to the next scheme on mismatch; only when no other
	// scheme is configured does the mismatch deny (handled by the final
	// return).
	if len(s.apiKeys) > 0 {
		if provided := r.Header.Get("X-API-Key"); provided != "" {
			for _, key := range s.apiKeys {
				if subtle.ConstantTimeCompare([]byte(provided), []byte(key)) == 1 {
					return true
				}
			}
		}
	}

	// 2) Bearer token (OAuth2 / OIDC)
	if authHeader := r.Header.Get("Authorization"); strings.HasPrefix(authHeader, "Bearer ") {
		token := strings.TrimPrefix(authHeader, "Bearer ")
		if s.tokenValidator != nil {
			_, err := s.tokenValidator.ValidateToken(r.Context(), token)
			if err == nil {
				return true
			}
			// #598 / #404 dual-credential policy: a rejected Bearer token
			// falls through to mTLS instead of hard-rejecting. Some HTTP
			// clients unconditionally attach an Authorization header, so a
			// valid-mTLS peer with a stray Bearer header was silently denied
			// while removing the header let the same peer through.
		}
		// No validator configured → reject (no fall-through: without a
		// validator the Bearer credential is unverifiable, not failed).
		if s.tokenValidator == nil {
			return false
		}
	}

	// 3) mTLS — client certificate verified at TLS handshake level
	if s.mtlsEnabled {
		if r.TLS != nil && len(r.TLS.PeerCertificates) > 0 {
			return true
		}
		return false
	}

	// 4) No auth configured: allow all connections.
	// A2A + mDNS is always-on by design; instances without explicit auth
	// rely on LAN isolation for security.
	if len(s.apiKeys) == 0 && s.tokenValidator == nil && !s.mtlsEnabled {
		return true
	}

	return false
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
		// #1111: same sweep window as the done branch below.
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
		// #1111: honor GetTask's ok return - the task can be swept by
		// cleanupExpiredTasksLocked between done closing and this read;
		// serializing a nil task would emit result:null (protocol violation).
		writeTaskResultOrNotFound(w, req.ID, s.handler, task.ID)
	case <-timer.C:
		// #1090: use -32001 to match stream/resubscribe, include task ID in Data
		writeRPCError(w, req.ID, &JSONRPCError{
			Code:    -32060,
			Message: "task timed out",
			Data:    fmt.Sprintf("task %s: use tasks/get to check status", task.ID),
		})
	case <-r.Context().Done():
		// Client disconnected — let the task continue in background.
		// #565 G: previously an empty, silent branch; log so retry storms
		// and half-closed connections are observable in debug logs.
		debug.Log("a2a", "message/send: client disconnected for task %s; task continues in background", task.ID)
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

	// Send current task status (not hardcoded working). #1073
	// #1113: honor GetTask's ok - the task can be swept between Handle
	// returning and this read (same race family as #1094/#1111); the
	// unconditional deref below would nil-panic the handler goroutine.
	t, ok := s.getTaskOrSSEError(w, flusher, req.ID, task.ID)
	if !ok {
		return
	}
	// #1115: if the task is ALREADY terminal here (messageId dedup-retry of
	// a completed task, or execution finished before this handler reached
	// the subscription point), emit artifacts plus exactly ONE final status
	// and stop. Falling through would re-send both from the done==nil
	// branch below, producing a duplicate final:true - the A2A stream
	// terminator - which strict clients reject.
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
		Final:  false, // non-terminal: guaranteed by the branch above
	})

	// Wait for task to reach terminal state.
	timeout := s.handler.Timeout()
	if timeout == 0 {
		timeout = 5 * time.Minute
	}
	done := s.handler.GetTaskDone(task.ID)
	if done == nil {
		// Task reached terminal between the non-terminal status event above
		// and this read. #565 D: emit artifacts here too or a fast-completing
		// task would stream a bare terminal status. #1113: honor ok - the
		// sweep race applies at this second read as well.
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
		// #1111: same defensive ok check as send/resubscribe (#1094) - the
		// task may be swept before this read; t.Artifacts would nil-panic.
		t, ok := s.getTaskOrSSEError(w, flusher, req.ID, task.ID)
		if !ok {
			return
		}
		// #565 D: emit artifact events before the terminal status so the
		// streamed result matches the card's declared streaming capability.
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
		// Client disconnected mid-task: events emitted after this point are
		// unrecoverable — there is no event buffer/replay, so anything the
		// task produces while the subscriber is gone is lost. The client can
		// still recover the final state via tasks/get or resubscribe.
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

	tasks, nextToken, err := s.handler.ListTasks(params.PageToken, params.PageSize)
	if err != nil {
		// Stale pageToken (task deleted by cleanup) — surface as invalid
		// params instead of silently restarting pagination (fix #258).
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

	// Fetch fresh snapshot AFTER cancellation. #1075
	result, ok := s.handler.GetTask(params.ID)
	if !ok {
		// Task was deleted after cancellation (race with cleanupExpiredTasksLocked).
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
		// Already terminal. #1088: the task finished between GetTask and GetTaskDone
		// (race window where updateStatus closed the channel). Emit artifacts here
		// too, matching the pattern from handleMessageStream (#565 D).
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
			// Task was cleaned up before we could read it (#1094).
			return
		}
		// #565 D: resubscribe previously only sent the terminal status — any
		// artifacts produced while disconnected were permanently invisible.
		// Emit them now, then the terminal status.
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
		// Client disconnected again — events produced during this gap are
		// not replayed (no event buffer); final state remains available via
		// tasks/get.
		debug.Log("a2a", "tasks/resubscribe: client disconnected for task %s; events during disconnect are not replayed", params.ID)
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

// sendSSEError sends a JSON-RPC 2.0 error response via SSE. Per the spec,
// errors must use the "error" member, not be nested inside "result".
func (s *Server) sendSSEError(w io.Writer, flusher http.Flusher, id json.RawMessage, code int, message string) {
	payload, _ := json.Marshal(JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &JSONRPCError{Code: code, Message: message},
	})
	fmt.Fprintf(w, "data: %s\n\n", payload)
	flusher.Flush()
}

// ---------------------------------------------------------------------------
// ---------------------------------------------------------------------------
// Push Notification Config handlers
// ---------------------------------------------------------------------------

func (s *Server) handlePushConfigSet(w http.ResponseWriter, req *JSONRPCRequest) {
	// #715: push callbacks stream task snapshots to a client-chosen URL.
	// With no real authentication configured (no key / only the public
	// default key), any LAN peer could register an exfiltration endpoint.
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
	// #715: empty TaskID matches ALL tasks — a wildcard exfil channel.
	// Requires explicit opt-in (ServerConfig.AllowWildcardPushCallbacks).
	if cfg.TaskID == "" && !s.allowWildcardPushConfigs {
		writeRPCError(w, req.ID, &JSONRPCError{
			Code:    ErrInvalidParams.Code,
			Message: ErrInvalidParams.Message,
			Data:    "push config with empty taskId matches ALL tasks and requires explicit operator opt-in (AllowWildcardPushCallbacks)",
		})
		return
	}
	// #715: validate the callback URL before storing it — reject
	// non-https (unless allowlisted), loopback/RFC1918/link-local targets.
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
		// Config lookup failure is not a task error - use InvalidParams. #1076
		writeRPCError(w, req.ID, &JSONRPCError{
			Code:    -32602, // InvalidParams
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

// ---------------------------------------------------------------------------
// Extended Agent Card handler
// ---------------------------------------------------------------------------

func (s *Server) handleGetExtendedCard(w http.ResponseWriter, req *JSONRPCRequest) {
	// #1114: read under cardMu - SetExtendedCard (hot config setter) can
	// swap the card concurrently with this HTTP read; the unsynchronized
	// pair was a direct `go test -race` hit.
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

// SetExtendedCard sets the optional extended agent card content.
func (s *Server) SetExtendedCard(card json.RawMessage) {
	// #1114: guard the extendedCard write with cardMu as well - the setter
	// can run concurrently with handleGetExtendedCard reads.
	s.cardMu.Lock()
	s.extendedCard = card
	if len(card) > 0 {
		s.card.Capabilities.ExtendedAgentCard = true
	}
	s.cardMu.Unlock()
}

// SetTokenValidator installs an OAuth2/OIDC token validator.
func (s *Server) SetTokenValidator(v *auth.TokenValidator) {
	s.tokenValidator = v
	s.rebuildSecuritySchemes()
}

// SetMTLSEnabled marks the server as using mutual TLS.
// Use SetTLSConfig to provide the actual TLS configuration.
func (s *Server) SetMTLSEnabled(enabled bool) {
	s.mtlsEnabled = enabled
	s.rebuildSecuritySchemes()
}

// SetTLSConfig configures the server to use TLS with client certificate verification.
// This enables mutual TLS (mTLS) authentication.
func (s *Server) SetTLSConfig(tlsCfg *tls.Config) {
	s.tlsConfig = tlsCfg
	s.mtlsEnabled = true
	s.rebuildSecuritySchemes()
}

// rebuildSecuritySchemes updates the Agent Card's securitySchemes and security
// to reflect all currently configured authentication methods.
// This is called after any auth configuration change.
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
			Scheme:      "bearer", // #1089: A2A spec 4.5 requires "scheme" for type=http
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

// SetHandler connects the TaskHandler and wires push notifications.
func (s *Server) SetHandler(h *TaskHandler) {
	s.handler = h
	if h != nil {
		h.SetPushNotifier(s.firePushNotifications)
	}
}

// firePushNotifications sends HTTP POST callbacks to all registered push
// configs for the given task. Implements failure counting, exponential backoff,
// and automatic disabling of dead endpoints. Fixes #1074.
func (s *Server) firePushNotifications(taskID string, payload StreamResponse) {
	s.pushMu.RLock()
	configs := make([]PushNotificationConfig, 0)
	for _, cfg := range s.pushConfigs {
		if cfg.TaskID == "" || cfg.TaskID == taskID {
			configs = append(configs, cfg)
		}
	}
	s.pushMu.RUnlock()

	// #715 defense-in-depth: re-check every callback URL at delivery time.
	// Configs planted directly (pre-guard state, future bugs) or whose DNS
	// has since changed to an internal address must never be dialed.
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
		// Check if config is disabled or waiting for backoff.
		// #1470-A: re-read the LIVE entry under the lock - the old shape
		// copied the snapshot-loop variable under RLock (protecting nothing:
		// neither a map read nor shared state), so health updates recorded by
		// earlier delivery goroutines (recordPushFailure/Success) were
		// invisible during rapid task transitions and the #1074 backoff plus
		// the Disabled verdict both went inert.
		s.pushMu.RLock()
		live, ok := s.pushConfigs[cfg.ID]
		configCopy := live
		s.pushMu.RUnlock()
		if !ok {
			continue
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
			// #715: dedicated client — hard timeout + CheckRedirect that
			// re-validates every redirect hop against the same SSRF rules
			// (http.DefaultClient followed redirects to internal targets).
			resp, err := s.pushClient.Do(req)
			if err != nil {
				s.recordPushFailure(configID, err.Error())
				debug.Log("a2a", "push delivery error: %v", err)
				return
			}
			io.Copy(io.Discard, resp.Body) // drain for connection reuse
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

// ---------------------------------------------------------------------------
// Response helpers
// ---------------------------------------------------------------------------

// getTaskOrSSEError fetches the task for an SSE response; on a sweep miss
// (the #1094/#1111/#1113 race family: cleanupExpiredTasksLocked can delete
// a terminal task between completion signaling and a later read) it emits
// the TaskNotFound SSE error and reports ok=false. Without this the
// unconditional derefs would nil-panic the handler goroutine.
func (s *Server) getTaskOrSSEError(w http.ResponseWriter, flusher http.Flusher, reqID json.RawMessage, taskID string) (*Task, bool) {
	t, ok := s.handler.GetTask(taskID)
	if !ok {
		s.sendSSEError(w, flusher, reqID, ErrTaskNotFound.Code, ErrTaskNotFound.Message)
		return nil, false
	}
	return t, true
}

// writeTaskResultOrNotFound writes the task as the JSON-RPC result, or the
// ErrTaskNotFound error when GetTask reports the task was swept (the
// #1094/#1111 race family: cleanupExpiredTasksLocked can delete a terminal
// task between completion signaling and this read). Serializing the nil
// task would emit a protocol-violating result:null.
func writeTaskResultOrNotFound(w http.ResponseWriter, id json.RawMessage, h *TaskHandler, taskID string) {
	if t, ok := h.GetTask(taskID); ok {
		writeRPCResult(w, id, t)
		return
	}
	writeRPCError(w, id, ErrTaskNotFound)
}

func writeRPCResult(w http.ResponseWriter, id json.RawMessage, result interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      normalizeResponseID(id),
		Result:  result,
	})
}

func writeRPCError(w http.ResponseWriter, id json.RawMessage, rpcErr *JSONRPCError) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(JSONRPCResponse{
		JSONRPC: "2.0",
		// #565 E: JSON-RPC 2.0 requires the id member to be present (null
		// when the request id could not be determined — parse error /
		// invalid request); omitempty dropped it entirely.
		ID:    normalizeResponseID(id),
		Error: rpcErr,
	})
}

// Constants for push notification health tracking. Fixes #1074.
const (
	maxPushFailuresBeforeDisable = 5
	initialPushBackoff           = 10 * time.Second
	maxPushBackoff               = 10 * time.Minute
)

// recordPushFailure updates failure count and backoff for a push config.
// Implements exponential backoff and disables after max failures. Fixes #1074.
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

// recordPushSuccess resets failure count and backoff for a push config. Fixes #1074.
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

// normalizeResponseID maps an absent request id to JSON null for response
// serialization (fix #565 E). It returns json.RawMessage because the
// JSONRPCResponse.ID field is json.RawMessage — a literal `null` byte
// sequence passes omitempty's emptiness check and serializes as "id":null.
func normalizeResponseID(id json.RawMessage) json.RawMessage {
	if len(id) == 0 {
		return json.RawMessage(`null`)
	}
	return id
}
