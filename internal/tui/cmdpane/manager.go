package cmdpane

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Manager struct {
	mu        sync.Mutex
	paneID    string
	logFile   *os.File
	logPath   string
	active    bool
	selfPane  string
	workspace string
	writeMu   sync.Mutex

	curPlacement Placement
}

type lockedWriter struct {
	mgr *Manager
}

func (w *lockedWriter) Write(p []byte) (int, error) {
	w.mgr.writeMu.Lock()
	defer w.mgr.writeMu.Unlock()
	if w.mgr.logFile == nil {
		return 0, nil
	}

	if info, err := w.mgr.logFile.Stat(); err == nil && info.Size()+int64(len(p)) > maxCmdPaneLogSize {
		_ = w.mgr.logFile.Truncate(0)
		_, _ = w.mgr.logFile.Seek(0, io.SeekStart)
	}
	return w.mgr.logFile.Write(p)
}

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

const maxCmdPaneLogSize = 5 << 20

func (m *Manager) Writer() (io.Writer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.logFile == nil {
		m.logPath = m.logFilePath()
		if err := os.MkdirAll(filepath.Dir(m.logPath), 0o755); err != nil {
			return nil, fmt.Errorf("cmdpane: failed to create log dir: %w", err)
		}

		if info, err := os.Stat(m.logPath); err == nil && info.Size() > maxCmdPaneLogSize {
			_ = os.Truncate(m.logPath, 0)
		}
		f, err := os.OpenFile(m.logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return nil, fmt.Errorf("cmdpane: failed to open log file: %w", err)
		}
		m.logFile = f
	}
	return &lockedWriter{mgr: m}, nil
}

func (m *Manager) EnsurePane(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if os.Getenv("TMUX") == "" {
		return nil
	}

	cols, rows, pixW, pixH, err := m.getWindowSize(ctx)
	if err != nil {
		return fmt.Errorf("cmdpane: failed to get window size: %w", err)
	}

	wantPlacement := DeterminePlacement(cols, rows, pixW, pixH)

	if m.active && m.paneID != "" {
		alive, err := m.paneExists(ctx, m.paneID)
		if err != nil {
			return fmt.Errorf("cmdpane: failed to check pane: %w", err)
		}

		if !alive {

			m.active = false
			m.paneID = ""
		} else if m.shouldRecreatePane(wantPlacement) {

			m.killPane(ctx)
			m.active = false
			m.paneID = ""
		} else if !wantPlacement.IsActive() {

			m.killPane(ctx)
			m.active = false
			m.paneID = ""
			m.curPlacement = Placement{}
			return nil
		} else {

			return nil
		}
	}

	if !wantPlacement.IsActive() {
		return nil
	}

	if m.logFile == nil {
		m.logPath = m.logFilePath()
		f, err := os.OpenFile(m.logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return fmt.Errorf("cmdpane: failed to create log file: %w", err)
		}
		m.logFile = f
	}

	if m.selfPane == "" {
		out, err := m.runTmux(ctx, "display-message", "-p", "#{pane_id}")
		if err != nil || strings.TrimSpace(out) == "" {

			if err == nil {
				err = errors.New("empty pane id")
			}
			return fmt.Errorf("cmdpane: failed to determine current tmux pane: %w", err)
		}
		m.selfPane = strings.TrimSpace(out)
	}

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
	splitArgs = append(splitArgs, "tail", "-f", "-n", "0", m.logPath)

	paneID, err := m.runTmux(ctx, splitArgs...)
	if err != nil {
		return fmt.Errorf("cmdpane: failed to create pane: %w", err)
	}

	m.paneID = strings.TrimSpace(paneID)
	m.active = true
	m.curPlacement = wantPlacement
	return nil
}

func (m *Manager) shouldRecreatePane(want Placement) bool {
	cur := m.curPlacement

	if cur.Direction != want.Direction {
		return true
	}

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

func (m *Manager) paneExists(ctx context.Context, paneID string) (bool, error) {
	out, err := m.runTmux(ctx, "list-panes", "-a", "-F", "#{pane_id}")
	if err != nil {
		return false, err
	}

	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == paneID {
			return true, nil
		}
	}
	return false, nil
}

func (m *Manager) killPane(ctx context.Context) {
	if m.paneID != "" && m.paneID != m.selfPane {
		_, _ = m.runTmux(ctx, "kill-pane", "-t", m.paneID)
	}
}

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
	if len([]rune(cmdDisplay)) > 500 {
		cmdDisplay = string([]rune(cmdDisplay)[:497]) + "..."
	}
	header += fmt.Sprintf("\033[33m$ %s\033[0m\n", cmdDisplay)
	_, _ = m.logFile.WriteString(header)
}

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

func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.paneID != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		m.killPane(ctx)
	}
	if m.logFile != nil {

		m.writeMu.Lock()
		_ = m.logFile.Close()
		m.logFile = nil
		m.writeMu.Unlock()
	}
	if m.logPath != "" {
		_ = os.Remove(m.logPath)
	}
	m.active = false
	m.paneID = ""
	m.curPlacement = Placement{}
}

func (m *Manager) runTmux(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "tmux", args...)
	var stdout strings.Builder
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return stdout.String(), nil
}

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

	pixW, pixH = getPixelSize()
	return cols, rows, pixW, pixH, nil
}
