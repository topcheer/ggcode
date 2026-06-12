package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/topcheer/ggcode/internal/stream"
)

// handleStreamSlash processes /stream sub-commands.
func (m *Model) handleStreamSlash(args string) (string, bool) {
	parts := strings.Fields(args)
	if len(parts) == 0 {
		return m.streamHelp(), false
	}

	switch parts[0] {
	case "start":
		return m.streamStart(parts[1:])
	case "stop":
		return m.streamStop(parts[1:])
	case "status":
		return m.streamStatus(), false
	case "config":
		m.openStreamPanel()
		return "", false
	default:
		return m.streamHelp(), false
	}
}

func (m *Model) streamHelp() string {
	lines := []string{
		"/stream start [targets...]  — start streaming (all enabled, or named targets)",
		"/stream stop [targets...]   — stop streaming (all, or named targets)",
		"/stream status              — show all targets with status",
		"/stream config              — open stream configuration panel",
	}
	return strings.Join(lines, "\n")
}

func (m *Model) streamStart(targets []string) (string, bool) {
	if m.streamManager != nil && m.streamManager.IsRunning() {
		return "stream: already running. Use /stream stop first.", false
	}

	// Check ffmpeg first
	check := stream.CheckFFmpeg()
	if !check.Available {
		return fmt.Sprintf("stream: %s", check.Error), false
	}

	cfg := m.config.Stream
	cfg.ExpandEnv()
	cfg.ApplyDefaults()

	// Filter to specific targets if named
	if len(targets) > 0 {
		want := make(map[string]bool)
		for _, t := range targets {
			want[t] = true
		}
		var filtered []stream.StreamTarget
		for _, t := range cfg.Targets {
			if want[t.Name] {
				t.Enabled = true
				filtered = append(filtered, t)
			}
		}
		if len(filtered) == 0 {
			return fmt.Sprintf("stream: no matching targets found for %v", targets), false
		}
		cfg.Targets = filtered
	}

	if len(cfg.Targets) == 0 {
		return "stream: no targets configured. Add targets to ggcode.yaml under 'stream.targets'.", false
	}

	mgr := stream.NewManager(cfg)
	m.streamManager = mgr

	// ViewFunc reads the cached view snapshot (thread-safe via shared pointer).
	// Never calls m.View() directly — that would race with Bubble Tea.
	viewFunc := func() (string, stream.TerminalSize) {
		snap, _ := m.streamViewState.getSnapshot()
		return snap, stream.TerminalSize{Cols: m.width, Rows: m.height}
	}

	if err := mgr.Start(viewFunc); err != nil {
		m.streamManager = nil
		return fmt.Sprintf("stream: start failed: %v", err), false
	}

	names := make([]string, 0)
	for _, t := range cfg.Targets {
		if t.Enabled {
			names = append(names, t.Name)
		}
	}
	// Show actual encoder resolution
	w, h := cfg.Width, cfg.Height
	if m.streamManager != nil {
		ew, eh := m.streamManager.EncoderSize()
		if ew > 0 && eh > 0 {
			w, h = ew, eh
		}
	}
	if w == 0 || h == 0 {
		// Predict based on current terminal orientation
		if m.width >= m.height {
			w, h = 1920, 1080
		} else {
			w, h = 1080, 1920
		}
	}
	return fmt.Sprintf("stream: started → %s (%dx%d @ %dfps)",
		strings.Join(names, ", "), w, h, cfg.FPS), false
}

func (m *Model) streamStop(targets []string) (string, bool) {
	if m.streamManager == nil {
		return "stream: not running", false
	}

	if len(targets) == 0 {
		m.streamManager.Stop()
		m.streamManager = nil
		return "stream: stopped all targets", false
	}

	var stopped []string
	for _, name := range targets {
		if err := m.streamManager.StopTarget(name); err != nil {
			return fmt.Sprintf("stream: %v", err), false
		}
		stopped = append(stopped, name)
	}

	// If no targets remain, fully stop
	if len(m.streamManager.Status()) == 0 {
		m.streamManager.Stop()
		m.streamManager = nil
	}

	return fmt.Sprintf("stream: stopped %s", strings.Join(stopped, ", ")), false
}

func (m *Model) streamStatus() string {
	check := stream.CheckFFmpeg()
	ffmpegInfo := "ffmpeg: not available"
	if check.Available {
		ffmpegInfo = fmt.Sprintf("ffmpeg: %s (%s)", check.Version, check.Path)
	}

	if m.streamManager == nil || !m.streamManager.IsRunning() {
		return ffmpegInfo + "\nstream: not running"
	}

	statuses := m.streamManager.Status()
	if len(statuses) == 0 {
		return ffmpegInfo + "\nstream: running but no active targets"
	}

	green := lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Bold(true)
	red := lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	yellow := lipgloss.NewStyle().Foreground(lipgloss.Color("3"))

	var lines []string
	lines = append(lines, "Stream Targets:")
	for _, s := range statuses {
		var stateStr string
		switch s.State {
		case "live":
			stateStr = green.Render(string(s.State))
		case "error":
			stateStr = red.Render(string(s.State))
		default:
			stateStr = yellow.Render(string(s.State))
		}

		info := fmt.Sprintf("  %s [%s] %s", s.Name, stateStr, s.URL)
		if s.Uptime > 0 {
			info += fmt.Sprintf(" uptime=%s sent=%dKB",
				s.Uptime.Truncate(time.Second).String(), s.BytesSent/1024)
		}
		if s.LastError != "" {
			info += fmt.Sprintf(" err=%s", s.LastError)
		}
		lines = append(lines, info)
	}
	return strings.Join(lines, "\n")
}
