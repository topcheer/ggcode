package cmdpane

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Manager manages a persistent tmux split pane that mirrors command
// execution output in real time via a tail -f log file.
type Manager struct {
	mu        sync.Mutex
	paneID    string // tmux pane ID (e.g. "%5")
	logFile   *os.File
	logPath   string
	active    bool       // pane has been created
	selfPane  string     // ggcode's own pane ID (never kill)
	workspace string     // workspace dir (for unique log file)
	writeMu   sync.Mutex // serializes log file writes from concurrent goroutines

	// Track the placement that was used to create the current pane,
	// so we can detect when a resize invalidates it.
	curPlacement Placement
}

// lockedWriter wraps a Manager's writeMu around log file writes to serialize
// concurrent stdout/stderr tee writes from parallel command executions.
type lockedWriter struct {
	mgr *Manager
}

func (w *lockedWriter) Write(p []byte) (int, error) {
	w.mgr.writeMu.Lock()
	defer w.mgr.writeMu.Unlock()
	if w.mgr.logFile == nil {
		return 0, nil
	}
	return w.mgr.logFile.Write(p)
}

// NewManager creates a new command pane manager for the given workspace.
func NewManager(workspace string) *Manager {
	return &Manager{
		workspace: workspace,
	}
}

func (m *Manager) logFilePath() string {
	h := sha256.Sum256([]byte(m.workspace))
	short := hex.EncodeToString(h[:8])
	return filepath.Join(os.TempDir(), fmt.Sprintf("ggcode-cmdpane-%s.log", short))
}

// Writer returns an io.Writer that appends to the command pane log file.
// The writer is safe for concurrent use — writes are serialized so that
// parallel command executions (e.g. from sub-agents) don't interleave.
func (m *Manager) Writer() (io.Writer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.logFile == nil {
		m.logPath = m.logFilePath()
		if err := os.MkdirAll(filepath.Dir(m.logPath), 0o755); err != nil {
			return nil, fmt.Errorf("cmdpane: failed to create log dir: %w", err)
		}
		f, err := os.OpenFile(m.logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return nil, fmt.Errorf("cmdpane: failed to open log file: %w", err)
		}
		m.logFile = f
	}
	return &lockedWriter{mgr: m}, nil
}

// EnsurePane creates or recreates the tmux command pane based on the
// current terminal dimensions. It is called before every command execution.
//
// Resize handling:
//   - Terminal too small now → kill existing pane (if any)
//   - Pane direction changed (e.g. right→bottom after rotating screen) → recreate
//   - Pane size drifts >50% from target → recreate (tmux auto-resizes proportionally,
//     so we only recreate on significant drift, not minor pixel changes)
//   - Pane killed externally → recreate
func (m *Manager) EnsurePane(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if we're in tmux.
	if os.Getenv("TMUX") == "" {
		return nil // not in tmux, no-op
	}

	// Get current terminal dimensions on every call.
	cols, rows, pixW, pixH, err := m.getWindowSize(ctx)
	if err != nil {
		return fmt.Errorf("cmdpane: failed to get window size: %w", err)
	}

	wantPlacement := DeterminePlacement(cols, rows, pixW, pixH)

	// If pane exists, check if it's still valid for the current size.
	if m.active && m.paneID != "" {
		alive, err := m.paneExists(ctx, m.paneID)
		if err != nil {
			return fmt.Errorf("cmdpane: failed to check pane: %w", err)
		}

		if !alive {
			// Pane was killed externally → fall through to recreate.
			m.active = false
			m.paneID = ""
		} else if m.shouldRecreatePane(wantPlacement) {
			// Pane exists but placement is stale → kill and recreate.
			m.killPane(ctx)
			m.active = false
			m.paneID = ""
		} else if !wantPlacement.IsActive() {
			// Terminal shrank below threshold → kill pane, don't recreate.
			m.killPane(ctx)
			m.active = false
			m.paneID = ""
			m.curPlacement = Placement{}
			return nil
		} else {
			// Pane still valid → nothing to do.
			return nil
		}
	}

	// At this point, pane doesn't exist. Check if we should create one.
	if !wantPlacement.IsActive() {
		return nil // terminal too small
	}

	// Ensure log file exists before creating pane.
	if m.logFile == nil {
		m.logPath = m.logFilePath()
		f, err := os.OpenFile(m.logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return fmt.Errorf("cmdpane: failed to create log file: %w", err)
		}
		m.logFile = f
	}

	// Capture self pane ID for safety.
	if m.selfPane == "" {
		out, err := m.runTmux(ctx, "display-message", "-p", "#{pane_id}")
		if err == nil {
			m.selfPane = strings.TrimSpace(out)
		}
	}

	// Create the split pane, targeting our own pane so the split always
	// happens in the window/tab where ggcode is running — not the
	// currently-active tab (which may differ if the user switched tabs).
	var flag string
	if wantPlacement.Direction == "right" {
		flag = "-h"
	} else {
		flag = "-v"
	}

	splitArgs := []string{"split-window", flag, "-l", fmt.Sprintf("%d", wantPlacement.Size),
		"-P", "-F", "#{pane_id}"}
	if m.selfPane != "" {
		splitArgs = append(splitArgs, "-t", m.selfPane)
	}
	splitArgs = append(splitArgs, "tail", "-f", m.logPath)

	paneID, err := m.runTmux(ctx, splitArgs...)
	if err != nil {
		return fmt.Errorf("cmdpane: failed to create pane: %w", err)
	}

	m.paneID = strings.TrimSpace(paneID)
	m.active = true
	m.curPlacement = wantPlacement
	return nil
}

// shouldRecreatePane returns true if the current pane's placement is
// incompatible with the desired placement.
func (m *Manager) shouldRecreatePane(want Placement) bool {
	cur := m.curPlacement

	// Direction changed (right ↔ bottom) → must recreate.
	if cur.Direction != want.Direction {
		return true
	}

	// Size drifted more than 50% → recreate to fix the proportion.
	// tmux auto-resizes panes proportionally on window resize, so we
	// only care about large drifts, not minor adjustments.
	if cur.Size > 0 && want.Size > 0 {
		larger := cur.Size
		smaller := want.Size
		if want.Size > cur.Size {
			larger = want.Size
			smaller = cur.Size
		}
		if smaller < larger/2 {
			return true
		}
	}

	return false
}

// paneExists checks whether a tmux pane ID is still alive.
// Searches across ALL windows, not just the active one, because the
// command pane might be in a different window than the current active tab.
func (m *Manager) paneExists(ctx context.Context, paneID string) (bool, error) {
	out, err := m.runTmux(ctx, "list-panes", "-a", "-F", "#{pane_id}")
	if err != nil {
		return false, err
	}
	return strings.Contains(out, paneID), nil
}

// killPane kills the current command pane (if it's not self).
func (m *Manager) killPane(ctx context.Context) {
	if m.paneID != "" && m.paneID != m.selfPane {
		_, _ = m.runTmux(ctx, "kill-pane", "-t", m.paneID)
	}
}

// WriteHeader writes a formatted header before command execution.
func (m *Manager) WriteHeader(command, description string) {
	m.writeMu.Lock()
	defer m.writeMu.Unlock()

	if m.logFile == nil {
		return
	}

	ts := time.Now().Format("15:04:05")
	header := fmt.Sprintf("\n\033[36m── run_command [%s] ────────────────────\033[0m\n", ts)
	if description != "" {
		header += fmt.Sprintf("\033[2m# %s\033[0m\n", description)
	}
	cmdDisplay := command
	if len(cmdDisplay) > 500 {
		cmdDisplay = cmdDisplay[:497] + "..."
	}
	header += fmt.Sprintf("\033[33m$ %s\033[0m\n", cmdDisplay)
	_, _ = m.logFile.WriteString(header)
}

// WriteFooter writes a formatted footer after command execution.
func (m *Manager) WriteFooter(exitCode int, err error) {
	m.writeMu.Lock()
	defer m.writeMu.Unlock()

	if m.logFile == nil {
		return
	}

	var footer string
	if err != nil || exitCode != 0 {
		footer = fmt.Sprintf("\033[31m✗ exit code: %d\033[0m\n", exitCode)
	} else {
		footer = "\033[32m✓ exit code: 0\033[0m\n"
	}
	footer += "\033[36m───────────────────────────────────────\033[0m\n\n"
	_, _ = m.logFile.WriteString(footer)
}

// Close kills the tmux pane and closes the log file.
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.paneID != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		m.killPane(ctx)
	}
	if m.logFile != nil {
		_ = m.logFile.Close()
		m.logFile = nil
	}
	if m.logPath != "" {
		_ = os.Remove(m.logPath)
	}
	m.active = false
	m.paneID = ""
	m.curPlacement = Placement{}
}

// --- tmux helpers ---

func (m *Manager) runTmux(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "tmux", args...)
	var stdout strings.Builder
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return stdout.String(), nil
}

// getWindowSize returns the terminal dimensions: character-cell counts
// (cols/rows) from tmux, and pixel dimensions (pixW/pixH) from TIOCGWINSZ.
// Pixel dimensions are 0 when unavailable (some terminals don't report them).
func (m *Manager) getWindowSize(ctx context.Context) (cols, rows, pixW, pixH int, err error) {
	out, err := m.runTmux(ctx, "display-message", "-p", "#{window_width} #{window_height}")
	if err != nil {
		return 0, 0, 0, 0, err
	}
	parts := strings.Fields(strings.TrimSpace(out))
	if len(parts) < 2 {
		return 0, 0, 0, 0, fmt.Errorf("unexpected tmux output: %q", out)
	}
	fmt.Sscanf(parts[0], "%d", &cols)
	fmt.Sscanf(parts[1], "%d", &rows)

	// Try to get pixel dimensions via TIOCGWINSZ on /dev/tty.
	// This works even inside tmux when the outer terminal reports pixels.
	pixW, pixH = getPixelSize()
	return cols, rows, pixW, pixH, nil
}
