package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const sessionIdleTTL = 2 * time.Minute
const csharpWarmupRetryDelay = 400 * time.Millisecond
const csharpWarmupRetryAttempts = 8
const publishedDiagnosticsWait = 1200 * time.Millisecond

type documentState struct {
	version int
	text    string
}

type diagnosticsState struct {
	seen        bool
	diagnostics []Diagnostic
}

type sessionClient struct {
	workspace string
	resolved  ResolvedServer
	client    *stdioClient

	opMu sync.Mutex
	mu   sync.Mutex

	readyOnce   sync.Once
	readySignal chan struct{}

	docs                  map[string]documentState
	diagnostics           map[string]diagnosticsState
	pullDiagnosticsKnown  bool
	pullDiagnosticsUsable bool
	lastUsed              time.Time
	closed                bool
}

func (s *sessionClient) shouldRetryEmptyResults() bool {
	return binaryBaseName(s.resolved.Binary) == "csharp-ls"
}

type sessionManager struct {
	mu       sync.Mutex
	sessions map[string]*sessionClient
	once     sync.Once
}

var globalSessions = &sessionManager{sessions: make(map[string]*sessionClient)}

func withOpenDocument[T any](ctx context.Context, workspace, path string, fn func(context.Context, *sessionClient, string) (T, error)) (T, error) {
	var zero T
	resolved, ok := ResolveServerForFile(workspace, path)
	if !ok {
		return zero, fmt.Errorf("no supported LSP server detected for %s", path)
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return zero, err
	}
	if workspace == "" {
		workspace = filepath.Dir(absPath)
	}
	session, err := globalSessions.acquire(ctx, workspace, resolved)
	if err != nil {
		return zero, err
	}
	docURI, err := session.prepareDocument(ctx, absPath, resolved.LanguageID)
	if err != nil {
		return zero, err
	}
	if err := session.primeProject(ctx, docURI); err != nil {
		return zero, err
	}
	return fn(ctx, session, docURI)
}

func (m *sessionManager) acquire(ctx context.Context, workspace string, resolved ResolvedServer) (*sessionClient, error) {
	m.once.Do(func() {
		go m.reapIdle()
	})
	key := workspace + "\x00" + resolved.Binary + "\x00" + strings.Join(resolved.Args, "\x1f")
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing := m.sessions[key]; existing != nil && !existing.isClosed() {
		existing.touch()
		return existing, nil
	}
	client, err := startClient(ctx, workspace, resolved)
	if err != nil {
		return nil, err
	}
	session := &sessionClient{
		workspace:             workspace,
		resolved:              resolved,
		client:                client,
		readySignal:           make(chan struct{}),
		docs:                  make(map[string]documentState),
		diagnostics:           make(map[string]diagnosticsState),
		pullDiagnosticsUsable: true,
		lastUsed:              time.Now(),
	}
	if !session.shouldRetryEmptyResults() {
		session.markProjectReady()
	}
	client.notificationHandler = session.handleNotification
	m.sessions[key] = session
	return session, nil
}

func (m *sessionManager) reapIdle() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		var stale []*sessionClient
		m.mu.Lock()
		for key, session := range m.sessions {
			if session.isClosed() || now.Sub(session.lastTouch()) > sessionIdleTTL {
				delete(m.sessions, key)
				stale = append(stale, session)
			}
		}
		m.mu.Unlock()
		for _, session := range stale {
			session.close()
		}
	}
}

func (s *sessionClient) prepareDocument(ctx context.Context, path, languageID string) (string, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	text := string(content)
	uri := fileURI(path)
	s.mu.Lock()
	state, exists := s.docs[uri]
	if state.text == text && exists {
		s.lastUsed = time.Now()
		s.mu.Unlock()
		return uri, nil
	}
	nextVersion := state.version + 1
	s.diagnostics[uri] = diagnosticsState{}
	s.mu.Unlock()
	if exists {
		if err := s.client.notify(ctx, "textDocument/didChange", map[string]any{
			"textDocument": map[string]any{
				"uri":     uri,
				"version": nextVersion,
			},
			"contentChanges": []map[string]any{{
				"text": text,
			}},
		}); err != nil {
			return "", err
		}
	} else {
		if err := s.client.notify(ctx, "textDocument/didOpen", map[string]any{
			"textDocument": map[string]any{
				"uri":        uri,
				"languageId": languageID,
				"version":    nextVersion,
				"text":       text,
			},
		}); err != nil {
			return "", err
		}
	}
	s.mu.Lock()
	s.docs[uri] = documentState{version: nextVersion, text: text}
	s.lastUsed = time.Now()
	s.mu.Unlock()
	return uri, nil
}

func (s *sessionClient) touch() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastUsed = time.Now()
}

func (s *sessionClient) lastTouch() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastUsed
}

func (s *sessionClient) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *sessionClient) close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	client := s.client
	s.mu.Unlock()
	if client != nil {
		client.close()
	}
}

func (s *sessionClient) markProjectReady() {
	s.readyOnce.Do(func() {
		close(s.readySignal)
	})
}

func (s *sessionClient) awaitProjectReady(ctx context.Context) error {
	if !s.shouldRetryEmptyResults() {
		return nil
	}
	select {
	case <-s.readySignal:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *sessionClient) projectReady() bool {
	select {
	case <-s.readySignal:
		return true
	default:
		return false
	}
}

func (s *sessionClient) primeProject(ctx context.Context, docURI string) error {
	if !s.shouldRetryEmptyResults() || s.projectReady() {
		return nil
	}
	var raw json.RawMessage
	if err := s.client.call(ctx, "textDocument/documentSymbol", map[string]any{
		"textDocument": map[string]any{"uri": docURI},
	}, &raw); err != nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		return err
	}
	if err := s.awaitProjectReady(ctx); err != nil {
		return err
	}
	return s.refreshDocument(ctx, docURI)
}

func (s *sessionClient) refreshDocument(ctx context.Context, uri string) error {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	s.mu.Lock()
	state, ok := s.docs[uri]
	s.mu.Unlock()
	if !ok {
		return nil
	}
	nextVersion := state.version + 1
	if err := s.client.notify(ctx, "textDocument/didChange", map[string]any{
		"textDocument": map[string]any{
			"uri":     uri,
			"version": nextVersion,
		},
		"contentChanges": []map[string]any{{
			"text": state.text,
		}},
	}); err != nil {
		return err
	}
	s.mu.Lock()
	s.docs[uri] = documentState{version: nextVersion, text: state.text}
	s.lastUsed = time.Now()
	s.mu.Unlock()
	return nil
}

func (s *sessionClient) handleNotification(method string, params json.RawMessage) {
	switch method {
	case "window/logMessage":
		if !s.shouldRetryEmptyResults() {
			return
		}
		var payload struct {
			Message string `json:"message"`
		}
		if err := json.Unmarshal(params, &payload); err != nil {
			return
		}
		message := strings.TrimSpace(payload.Message)
		if strings.Contains(message, "Finished loading solution") ||
			strings.Contains(message, "0 solution(s) found") ||
			strings.Contains(message, "No solution found") {
			s.markProjectReady()
		}
	case "textDocument/publishDiagnostics":
		var payload struct {
			URI         string `json:"uri"`
			Diagnostics []struct {
				Severity int      `json:"severity"`
				Message  string   `json:"message"`
				Source   string   `json:"source"`
				Range    rawRange `json:"range"`
			} `json:"diagnostics"`
		}
		if err := json.Unmarshal(params, &payload); err != nil || payload.URI == "" {
			return
		}
		diags := make([]Diagnostic, 0, len(payload.Diagnostics))
		for _, item := range payload.Diagnostics {
			diags = append(diags, Diagnostic{
				Severity: item.Severity,
				Message:  item.Message,
				Source:   item.Source,
				Range:    toRange(item.Range),
			})
		}
		s.mu.Lock()
		s.diagnostics[payload.URI] = diagnosticsState{seen: true, diagnostics: diags}
		s.lastUsed = time.Now()
		s.mu.Unlock()
	}
}

func (s *sessionClient) supportsPullDiagnostics() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.pullDiagnosticsKnown {
		return true
	}
	return s.pullDiagnosticsUsable
}

func (s *sessionClient) setPullDiagnosticsSupport(supported bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pullDiagnosticsKnown = true
	s.pullDiagnosticsUsable = supported
	s.lastUsed = time.Now()
}

func (s *sessionClient) publishedDiagnostics(uri string) ([]Diagnostic, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.diagnostics[uri]
	if !ok {
		return nil, false
	}
	if len(state.diagnostics) == 0 {
		return nil, state.seen
	}
	out := make([]Diagnostic, len(state.diagnostics))
	copy(out, state.diagnostics)
	return out, state.seen
}

func (s *sessionClient) setPublishedDiagnostics(uri string, diagnostics []Diagnostic) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Diagnostic, len(diagnostics))
	copy(out, diagnostics)
	s.diagnostics[uri] = diagnosticsState{seen: true, diagnostics: out}
	s.lastUsed = time.Now()
}
