// Package extpane manages external terminal tabs for sub-agent and teammate
// streaming output.
//
// Design: Each agent gets its own terminal tab running `tail -f <logfile>`.
// The TUI writes streaming output to a temp file via atomic appends. The tab
// displays the file with native scrolling. The main TUI layout is never
// affected — tabs are separate full-screen surfaces.
//
// Detection priority: tmux > iTerm2 > Kitty. Never two at once.
package extpane

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
)

// Backend abstracts a terminal's tab creation/destruction.
type Backend interface {
	// CreateTab creates a new tab/window running `tail -f <logfile>`.
	// Returns a backend-specific tab identifier.
	CreateTab(ctx context.Context, title, logfile string) (string, error)
	// CloseTab closes the tab.
	CloseTab(tabID string) error
	// Name returns the backend identifier.
	Name() string
}

// ExtPane tracks one agent's external tab + log file.
type ExtPane struct {
	AgentID   string
	Name      string
	Kind      string
	TabID     string
	LogFile   *os.File
	LogPath   string
	CreatedAt time.Time
	Done      bool
	DoneAt    time.Time

	// buffer accumulates streaming text between flushes.
	buffer strings.Builder
	dirty  bool
}

// maxConcurrentOps limits concurrent backend subprocess calls.
const maxConcurrentOps = 4

// Manager owns the external tab lifecycle for all agents.
type Manager struct {
	backend  Backend
	panes    map[string]*ExtPane
	creating map[string]bool // prevents duplicate EnsurePane for same agent
	failed   map[string]bool // permanently failed — never retry CreateTab
	mu       sync.Mutex
	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
	sem      chan struct{}

	// tmpDir is where agent log files live.
	tmpDir string

	// gracePeriod is how long a tab stays open after the agent completes.
	gracePeriod time.Duration
}

// maxPanes is the hard cap on concurrent tabs. Prevents runaway creation.
const maxPanes = 10

// NewManager creates a Manager, auto-detecting the best available backend.
// Priority: tmux > iTerm2 > Kitty. Returns a Manager with a nil backend
// if no terminal is suitable (all operations become no-ops).
func NewManager() *Manager {
	backend := detectBackend()
	tmpDir, err := os.MkdirTemp("", "ggcode-extpane-*")
	if err != nil {
		debug.Logf("extpane: mkdir temp failed, disabling: %v", err)
		backend = nil
	}
	m := &Manager{
		backend:     backend,
		panes:       make(map[string]*ExtPane),
		creating:    make(map[string]bool),
		failed:      make(map[string]bool),
		stopCh:      make(chan struct{}),
		sem:         make(chan struct{}, maxConcurrentOps),
		tmpDir:      tmpDir,
		gracePeriod: 30 * time.Second,
	}
	if backend != nil {
		m.startFlusher()
	}
	return m
}

// Available reports whether external tab output is active.
func (m *Manager) Available() bool {
	return m != nil && m.backend != nil
}

// goBackend runs a backend operation in a goroutine with bounded concurrency
// and WaitGroup tracking.
func (m *Manager) goBackend(fn func()) {
	m.wg.Add(1)
	safego.Go("extpane.backend", func() {
		defer m.wg.Done()
		select {
		case m.sem <- struct{}{}:
			defer func() { <-m.sem }()
		case <-m.stopCh:
			return
		}
		fn()
	})
}

// EnsurePane creates a tab + log file for the given agent if one doesn't exist yet.
// Uses a `creating` set to prevent duplicate tab creation during the async backend call.
func (m *Manager) EnsurePane(agentID, name, kind string) {
	if !m.Available() {
		return
	}
	m.mu.Lock()
	if _, ok := m.panes[agentID]; ok {
		m.mu.Unlock()
		return
	}
	// Prevent duplicate creation: if EnsurePane is already in progress for this agent,
	// bail out. This is the critical guard against spawning hundreds of tabs.
	if m.creating[agentID] {
		m.mu.Unlock()
		return
	}
	// If CreateTab previously failed for this agent, never retry.
	// This prevents runaway tab creation when the backend creates a tab
	// but returns an error (e.g., iTerm2 returns numeric session ID).
	if m.failed[agentID] {
		m.mu.Unlock()
		return
	}
	// Hard cap: never exceed maxPanes concurrent tabs.
	if len(m.panes) >= maxPanes {
		m.mu.Unlock()
		return
	}
	m.creating[agentID] = true
	m.mu.Unlock()

	// Cleanup helper on failure: mark as permanently failed so we never retry.
	fail := func(format string, args ...any) {
		debug.Logf(format, args...)
		m.mu.Lock()
		delete(m.creating, agentID)
		m.failed[agentID] = true // permanent — never retry CreateTab
		m.mu.Unlock()
	}

	// Create log file
	safeName := sanitizeFilename(name)
	logPath := filepath.Join(m.tmpDir, fmt.Sprintf("%s-%s.log", safeName, shortID(agentID)))
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		fail("extpane: create log file failed: %v", err)
		return
	}

	// Write header to log
	header := formatHeader(name, kind)
	if _, err := f.WriteString(header); err != nil {
		fail("extpane: write header failed: %v", err)
		f.Close()
		os.Remove(logPath)
		return
	}

	// Create tab
	title := formatTitle(name, kind, "starting")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tabID, err := m.backend.CreateTab(ctx, title, logPath)
	if err != nil {
		fail("extpane: create tab failed for %s: %v", agentID, err)
		f.Close()
		os.Remove(logPath)
		return
	}

	m.mu.Lock()
	delete(m.creating, agentID)
	m.panes[agentID] = &ExtPane{
		AgentID:   agentID,
		Name:      name,
		Kind:      kind,
		TabID:     tabID,
		LogFile:   f,
		LogPath:   logPath,
		CreatedAt: time.Now(),
	}
	m.mu.Unlock()
}

// WriteText queues streaming text for an agent's log file.
// Text is buffered and flushed periodically.
func (m *Manager) WriteText(agentID, text string) {
	if !m.Available() || text == "" {
		return
	}
	m.mu.Lock()
	ep, ok := m.panes[agentID]
	if !ok {
		m.mu.Unlock()
		return
	}
	ep.buffer.WriteString(text)
	ep.dirty = true
	m.mu.Unlock()
}

// WriteTextImmediate writes text to the log file without buffering.
func (m *Manager) WriteTextImmediate(agentID, text string) {
	if !m.Available() || text == "" {
		return
	}
	m.flushAgent(agentID)
	m.mu.Lock()
	defer m.mu.Unlock()
	ep, ok := m.panes[agentID]
	if !ok || ep.LogFile == nil {
		return
	}
	if _, err := ep.LogFile.WriteString(text); err != nil {
		debug.Logf("extpane: log write failed for %s: %v", ep.LogPath, err)
	}
}

// WriteToolCall writes a formatted tool call line.
func (m *Manager) WriteToolCall(agentID, toolName, detail string) {
	m.WriteTextImmediate(agentID, formatToolCall(toolName, detail))
}

// WriteToolResult writes a formatted tool result line.
func (m *Manager) WriteToolResult(agentID, toolName, result string, isError bool) {
	m.WriteTextImmediate(agentID, formatToolResult(toolName, result, isError))
}

// UpdateStatus updates the tab title.
func (m *Manager) UpdateStatus(agentID, name, kind, status string) {
	if !m.Available() {
		return
	}
	m.mu.Lock()
	ep, ok := m.panes[agentID]
	m.mu.Unlock()
	if !ok {
		return
	}
	title := formatTitle(name, kind, status)
	tabID := ep.TabID
	m.goBackend(func() {
		// Backends that don't support SetTitle (none currently) can ignore this.
		_ = setTitleSafe(m.backend, tabID, title)
	})
}

// HandleDone marks the agent as complete and schedules tab cleanup.
func (m *Manager) HandleDone(agentID, name string, isError bool) {
	if !m.Available() {
		return
	}
	m.mu.Lock()
	ep, ok := m.panes[agentID]
	if !ok || ep.Done {
		m.mu.Unlock()
		return
	}
	ep.Done = true
	ep.DoneAt = time.Now()
	kind := ep.Kind
	// Flush remaining buffer BEFORE setting Done.
	// flushAgent skips Done panes, so we extract and write the buffer here.
	text := ep.buffer.String()
	ep.buffer.Reset()
	ep.dirty = false
	if text != "" && ep.LogFile != nil {
		ep.LogFile.WriteString(text)
	}
	m.mu.Unlock()
	status := "done"
	if isError {
		status = "failed"
	}
	m.WriteTextImmediate(agentID, formatDone(isError))
	m.UpdateStatus(agentID, name, kind, status)

	// Schedule cleanup after grace period
	m.wg.Add(1)
	safego.Go("extpane.cleanup", func() {
		defer m.wg.Done()
		select {
		case <-time.After(m.gracePeriod):
			m.closePane(agentID)
		case <-m.stopCh:
		}
	})
}

// CloseAll immediately closes all external tabs. Called on TUI shutdown.
func (m *Manager) CloseAll() {
	if m == nil {
		return
	}
	m.stopOnce.Do(func() { close(m.stopCh) })
	if !m.Available() {
		// Still clean up tmpDir even if backend was nil.
		if m.tmpDir != "" {
			os.RemoveAll(m.tmpDir)
		}
		return
	}
	m.mu.Lock()
	ids := make([]string, 0, len(m.panes))
	for id := range m.panes {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	for _, id := range ids {
		m.closePane(id)
	}
	m.wg.Wait()
	// Clean up tmp dir
	if m.tmpDir != "" {
		os.RemoveAll(m.tmpDir)
	}
}

// closePane closes and removes a single agent's tab + log file.
func (m *Manager) closePane(agentID string) {
	m.mu.Lock()
	ep, ok := m.panes[agentID]
	if !ok {
		m.mu.Unlock()
		return
	}
	delete(m.panes, agentID)
	// Close file under the mutex to prevent writes to a closed fd.
	if ep.LogFile != nil {
		ep.LogFile.Close()
	}
	m.mu.Unlock()

	// Close tab (sync — only called from CloseAll or grace goroutine)
	if err := m.backend.CloseTab(ep.TabID); err != nil {
		debug.Logf("extpane: close tab failed for %s: %v", ep.TabID, err)
	}
	// Remove log file
	os.Remove(ep.LogPath)
}

// startFlusher runs a background goroutine that flushes buffered text
// at ~10 Hz to each agent's log file.
func (m *Manager) startFlusher() {
	m.wg.Add(1)
	safego.Go("extpane.flusher", func() {
		defer m.wg.Done()
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				m.flushAll()
			case <-m.stopCh:
				return
			}
		}
	})
}

func (m *Manager) flushAll() {
	m.mu.Lock()
	ids := make([]string, 0, len(m.panes))
	for id, ep := range m.panes {
		if ep.dirty && !ep.Done {
			ids = append(ids, id)
		}
	}
	m.mu.Unlock()
	for _, id := range ids {
		m.flushAgent(id)
	}
}

func (m *Manager) flushAgent(agentID string) {
	m.mu.Lock()
	ep, ok := m.panes[agentID]
	if !ok || !ep.dirty || ep.Done {
		m.mu.Unlock()
		return
	}
	text := ep.buffer.String()
	ep.buffer.Reset()
	ep.dirty = false
	// Write under the mutex to avoid racing with closePane.
	if text != "" && ep.LogFile != nil {
		if _, err := ep.LogFile.WriteString(text); err != nil {
			debug.Logf("extpane: flush write failed: %v", err)
		}
	}
	m.mu.Unlock()
}

// detectBackend returns the best available terminal backend.
// Priority: tmux > iTerm2 > Kitty.
func detectBackend() Backend {
	if hasTmux() {
		b := newTmuxBackend()
		if b != nil {
			return b
		}
	}
	if hasITerm2() {
		b := newITerm2Backend()
		if b != nil {
			return b
		}
	}
	if hasKitty() {
		b := newKittyBackend()
		if b != nil {
			return b
		}
	}
	return nil
}

func hasTmux() bool {
	return os.Getenv("TMUX") != ""
}

func hasITerm2() bool {
	return os.Getenv("TERM_PROGRAM") == "iTerm.app" || os.Getenv("LC_TERMINAL") == "iTerm2"
}

func hasKitty() bool {
	return os.Getenv("TERM_PROGRAM") == "kitty" || os.Getenv("KITTY_WINDOW_ID") != ""
}

// ── Helpers ──

func formatTitle(name, kind, status string) string {
	icon := "◆"
	if kind == "teammate" {
		icon = "●"
	}
	return fmt.Sprintf("%s %s · %s", icon, name, status)
}

func sanitizeFilename(s string) string {
	s = strings.ReplaceAll(s, " ", "-")
	s = strings.ReplaceAll(s, "/", "-")
	for _, c := range s {
		if c < ' ' || c > '~' {
			s = strings.ReplaceAll(s, string(c), "-")
		}
	}
	return s
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// setTitleSafe calls SetTitle if the backend supports it.
// Backends embed the optional setTitleer interface.
type setTitleer interface {
	SetTitle(tabID, title string) error
}

func setTitleSafe(b Backend, tabID, title string) error {
	if st, ok := b.(setTitleer); ok {
		return st.SetTitle(tabID, title)
	}
	return nil
}
