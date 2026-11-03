package extpane

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
)

type Backend interface {
	CreateTab(ctx context.Context, title, logfile string) (string, error)

	CloseTab(tabID string) error

	Name() string
}

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

	buffer strings.Builder
	dirty  bool
}

const maxConcurrentOps = 4

type Manager struct {
	backend  Backend
	panes    map[string]*ExtPane
	creating map[string]bool
	failed   map[string]bool
	retries  map[string]int
	mu       sync.Mutex
	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
	sem      chan struct{}

	tmpDir string

	gracePeriod time.Duration
}

const maxPanes = 10

const maxCreateRetries = 3

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
		retries:     make(map[string]int),
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

func (m *Manager) Available() bool {
	return m != nil && m.backend != nil
}

func (m *Manager) goBackend(fn func()) {

	select {
	case <-m.stopCh:
		return
	default:
	}
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

func isTransientCreateTabErr(err error) bool {
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)
}

func isHookBackupUsable(showErr error) bool {
	return showErr == nil
}

// EnsurePane creates a tab + log file for the given agent if one doesn't exist yet.
// Uses a `creating` set to prevent duplicate tab creation during the
// backend call. #1722 case 3: the call is SYNCHRONOUS on the Update
// goroutine, but bounded by a 5s CreateTab timeout (not the unbounded
// "async" the old comment claimed).

func (m *Manager) EnsurePane(agentID, name, kind string) {
	if !m.Available() {
		return
	}
	m.mu.Lock()
	if _, ok := m.panes[agentID]; ok {
		m.mu.Unlock()
		return
	}

	if m.creating[agentID] {
		m.mu.Unlock()
		return
	}

	if m.failed[agentID] {
		m.mu.Unlock()
		return
	}

	if len(m.panes)+len(m.creating) >= maxPanes {
		m.mu.Unlock()
		return
	}
	m.creating[agentID] = true
	m.mu.Unlock()

	fail := func(format string, args ...any) {
		debug.Logf(format, args...)
		m.mu.Lock()
		delete(m.creating, agentID)
		m.retries[agentID]++
		if m.retries[agentID] >= maxCreateRetries {
			m.failed[agentID] = true
		}
		m.mu.Unlock()
	}
	failPermanent := func(format string, args ...any) {
		debug.Logf(format, args...)
		m.mu.Lock()
		delete(m.creating, agentID)
		m.failed[agentID] = true
		m.mu.Unlock()
	}

	safeName := sanitizeFilename(name)
	logPath := filepath.Join(m.tmpDir, fmt.Sprintf("%s-%s.log", safeName, shortID(agentID)))
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		fail("extpane: create log file failed: %v", err)
		return
	}

	header := formatHeader(name, kind)
	if _, err := f.WriteString(header); err != nil {
		fail("extpane: write header failed: %v", err)
		f.Close()
		os.Remove(logPath)
		return
	}

	title := formatTitle(name, kind, "starting")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tabID, err := m.backend.CreateTab(ctx, title, logPath)
	if err != nil {

		if isTransientCreateTabErr(err) {
			fail("extpane: create tab transiently failed for %s: %v", agentID, err)
		} else {

			failPermanent("extpane: create tab failed for %s: %v", agentID, err)
		}
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

	if ep.Done {
		writeExtCapped(ep, text)
		m.mu.Unlock()
		return
	}
	ep.buffer.WriteString(text)
	ep.dirty = true
	m.mu.Unlock()
}

const maxExtPaneLogSize = 5 << 20

func writeExtCapped(ep *ExtPane, text string) {
	if ep.LogFile == nil {
		return
	}
	if info, err := ep.LogFile.Stat(); err == nil && info.Size()+int64(len(text)) > maxExtPaneLogSize {
		_ = ep.LogFile.Truncate(0)
		_, _ = ep.LogFile.Seek(0, io.SeekStart)
	}
	if _, err := ep.LogFile.WriteString(text); err != nil {
		debug.Logf("extpane: log write failed for %s: %v", ep.LogPath, err)
	}
}

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

	writeExtCapped(ep, text)
}

func (m *Manager) WriteToolCall(agentID, toolName, detail string) {
	m.WriteTextImmediate(agentID, formatToolCall(toolName, detail))
}

func (m *Manager) WriteToolResult(agentID, toolName, result string, isError bool) {
	m.WriteTextImmediate(agentID, formatToolResult(toolName, result, isError))
}

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

		_ = setTitleSafe(m.backend, tabID, title)
	})
}

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

	text := ep.buffer.String()
	ep.buffer.Reset()
	ep.dirty = false
	if text != "" {

		writeExtCapped(ep, text)
	}
	m.mu.Unlock()
	status := "done"
	if isError {
		status = "failed"
	}
	m.WriteTextImmediate(agentID, formatDone(isError))
	m.UpdateStatus(agentID, name, kind, status)

	select {
	case <-m.stopCh:
		return
	default:
	}
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

func (m *Manager) CloseAll() {
	if m == nil {
		return
	}
	m.stopOnce.Do(func() { close(m.stopCh) })
	if !m.Available() {

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

	if m.tmpDir != "" {
		os.RemoveAll(m.tmpDir)
	}
}

func (m *Manager) closePane(agentID string) {
	m.mu.Lock()
	ep, ok := m.panes[agentID]
	if !ok {
		m.mu.Unlock()
		return
	}
	delete(m.panes, agentID)

	if ep.LogFile != nil {
		ep.LogFile.Close()
	}
	m.mu.Unlock()

	if err := m.backend.CloseTab(ep.TabID); err != nil {
		debug.Logf("extpane: close tab failed for %s: %v", ep.TabID, err)
	}

	os.Remove(ep.LogPath)
}

func (m *Manager) startFlusher() {

	select {
	case <-m.stopCh:
		return
	default:
	}
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

	if text != "" {
		writeExtCapped(ep, text)
	}
	m.mu.Unlock()
}

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

type setTitleer interface {
	SetTitle(tabID, title string) error
}

func setTitleSafe(b Backend, tabID, title string) error {
	if st, ok := b.(setTitleer); ok {
		return st.SetTitle(tabID, title)
	}
	return nil
}
