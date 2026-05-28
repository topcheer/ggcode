package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/topcheer/ggcode/internal/stream"
)

const streamPanelLeftWidth = 28

type streamPanelState struct {
	focus         int // 0=platform list, 1=config area
	selectedIndex int
	scrollOffset  int
	keyInput      textinput.Model
	urlInput      textinput.Model
	nameInput     textinput.Model
	message       string
	editingField  string                // "", "key", "url", "name"
	customMode    bool                  // adding a custom target
	targets       []stream.StreamTarget // current working config
	statusRefresh bool
}

func newStreamPanel(cfg stream.StreamConfig) *streamPanelState {
	p := &streamPanelState{
		targets: make([]stream.StreamTarget, len(cfg.Targets)),
	}
	copy(p.targets, cfg.Targets)

	p.keyInput = textinput.New()
	p.keyInput.Placeholder = placeholderWithPasteShortcutHint("Stream key...", LangEnglish)
	p.keyInput.EchoMode = textinput.EchoPassword

	p.urlInput = textinput.New()
	p.urlInput.Placeholder = placeholderWithPasteShortcutHint("rtmp://... or rtmps://...", LangEnglish)

	p.nameInput = textinput.New()
	p.nameInput.Placeholder = placeholderWithPasteShortcutHint("Target name (e.g., youtube)", LangEnglish)

	return p
}

func (p *streamPanelState) selectedPreset() *stream.PlatformPreset {
	if p.customMode {
		return nil
	}
	presets := stream.Presets
	idx := p.selectedIndex - len(p.targets) // index into presets after existing targets
	if idx >= 0 && idx < len(presets) {
		return &presets[idx]
	}
	// Check if selectedIndex points to a target with a preset ID
	if p.selectedIndex >= 0 && p.selectedIndex < len(p.targets) {
		t := p.targets[p.selectedIndex]
		if preset := stream.PresetByID(t.Name); preset != nil {
			return preset
		}
	}
	return nil
}

func (p *streamPanelState) totalItems() int {
	return len(p.targets) + len(stream.Presets) + 1 // +1 for "Custom"
}

// --- View ---

func (m *Model) updateStreamPanel(msg tea.Msg) (tea.Model, tea.Cmd) {
	p := m.streamPanel
	if p == nil {
		return m, nil
	}

	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "esc":
			if p.editingField != "" {
				p.editingField = ""
				p.message = ""
				return m, nil
			}
			m.closeStreamPanel()
			return m, nil
		case "up", "k":
			if p.editingField == "" {
				if p.selectedIndex > 0 {
					p.selectedIndex--
				}
				m.syncStreamPanelSelection()
				return m, nil
			}
		case "down", "j":
			if p.editingField == "" {
				if p.selectedIndex < p.totalItems()-1 {
					p.selectedIndex++
				}
				m.syncStreamPanelSelection()
				return m, nil
			}
		case "enter":
			return m.handleStreamPanelEnter()
		case "e": // edit
			if p.editingField == "" && p.selectedIndex < len(p.targets) {
				p.editingField = "key"
				p.keyInput.SetValue(p.targets[p.selectedIndex].Key)
				p.keyInput.Focus()
				return m, nil
			}
		case "d": // delete
			if p.editingField == "" && p.selectedIndex < len(p.targets) {
				p.targets = append(p.targets[:p.selectedIndex], p.targets[p.selectedIndex+1:]...)
				if p.selectedIndex >= len(p.targets) && p.selectedIndex > 0 {
					p.selectedIndex--
				}
				p.message = "Target removed"
				m.persistStreamConfig()
				return m, nil
			}
		case "s": // start
			if m.streamManager != nil && m.streamManager.IsRunning() {
				p.message = "Already streaming"
				return m, nil
			}
			cfg := m.config.Stream
			cfg.Targets = p.targets
			cfg.ExpandEnv()
			cfg.ApplyDefaults()
			if err := cfg.Validate(); err != nil {
				p.message = err.Error()
				return m, nil
			}
			mgr := stream.NewManager(cfg)
			m.streamManager = mgr
			viewFunc := func() (string, stream.TerminalSize) {
				snap, _ := m.streamViewState.getSnapshot()
				return snap, stream.TerminalSize{Cols: m.width, Rows: m.height}
			}
			if err := mgr.Start(viewFunc); err != nil {
				m.streamManager = nil
				p.message = fmt.Sprintf("Start failed: %v", err)
				return m, nil
			}
			names := make([]string, 0)
			for _, t := range cfg.Targets {
				if t.Enabled {
					names = append(names, t.Name)
				}
			}
			p.message = fmt.Sprintf("Started → %s", strings.Join(names, ", "))
			return m, nil
		case "x": // stop
			if m.streamManager != nil && m.streamManager.IsRunning() {
				m.streamManager.Stop()
				m.streamManager = nil
				p.message = "Stream stopped"
			} else {
				p.message = "Not streaming"
			}
			return m, nil
		case "?": // help
			p.message = "↑/k↓/j:nav  e:edit  d:delete  s:start  x:stop  enter:add"
			return m, nil
		}

		// Handle text input for editing fields
		if p.editingField != "" {
			switch p.editingField {
			case "key":
				var cmd tea.Cmd
				p.keyInput, cmd = p.keyInput.Update(msg)
				return m, cmd
			case "url":
				var cmd tea.Cmd
				p.urlInput, cmd = p.urlInput.Update(msg)
				return m, cmd
			case "name":
				var cmd tea.Cmd
				p.nameInput, cmd = p.nameInput.Update(msg)
				return m, cmd
			}
		}
	}

	return m, nil
}

func (m *Model) handleStreamPanelEnter() (tea.Model, tea.Cmd) {
	p := m.streamPanel
	if p == nil {
		return m, nil
	}

	// If editing, save and exit edit mode
	if p.editingField != "" {
		switch p.editingField {
		case "key":
			if p.selectedIndex < len(p.targets) {
				p.targets[p.selectedIndex].Key = p.keyInput.Value()
			}
		case "url":
			if p.customMode {
				p.urlInput.SetValue(p.urlInput.Value())
			}
		case "name":
			// For custom: name → url → key flow
			if p.customMode && p.editingField == "name" {
				p.editingField = "url"
				p.urlInput.Focus()
				return m, nil
			}
		}

		// Continue to next field or finish
		if p.customMode {
			switch p.editingField {
			case "url":
				p.editingField = "key"
				p.keyInput.Focus()
				return m, nil
			case "key":
				// Save custom target
				name := strings.TrimSpace(p.nameInput.Value())
				url := strings.TrimSpace(p.urlInput.Value())
				key := p.keyInput.Value()
				if name == "" || url == "" || key == "" {
					p.message = "All fields required"
					p.editingField = ""
					return m, nil
				}
				p.targets = append(p.targets, stream.StreamTarget{
					Name:    name,
					Enabled: true,
					URL:     url,
					Key:     key,
				})
				p.selectedIndex = len(p.targets) - 1
				p.customMode = false
				p.editingField = ""
				p.message = fmt.Sprintf("Added %s", name)
				m.persistStreamConfig()
				return m, nil
			}
		}

		p.editingField = ""
		p.message = "Saved"
		m.persistStreamConfig()
		return m, nil
	}

	// Not editing: add from preset or start custom flow
	totalItems := p.totalItems()
	if p.selectedIndex >= len(p.targets) && p.selectedIndex < totalItems-1 {
		// Preset selected
		presetIdx := p.selectedIndex - len(p.targets)
		preset := stream.Presets[presetIdx]

		// Check if already added
		for _, t := range p.targets {
			if t.Name == preset.ID || t.Name == preset.Name {
				p.message = fmt.Sprintf("%s already added", preset.Name)
				return m, nil
			}
		}

		p.targets = append(p.targets, stream.StreamTarget{
			Name:    preset.ID,
			Enabled: true,
			URL:     preset.URL,
		})
		p.selectedIndex = len(p.targets) - 1
		p.editingField = "key"
		p.keyInput.SetValue("")
		p.keyInput.Placeholder = placeholderWithPasteShortcutHint(fmt.Sprintf("Enter %s stream key...", preset.Name), LangEnglish)
		p.keyInput.Focus()
		p.message = fmt.Sprintf("Added %s — enter stream key", preset.Name)
		m.persistStreamConfig()
		return m, nil
	}

	// Custom target
	if p.selectedIndex == totalItems-1 {
		p.customMode = true
		p.editingField = "name"
		p.nameInput.SetValue("")
		p.nameInput.Focus()
		p.message = "Add custom target"
		return m, nil
	}

	return m, nil
}

func (m *Model) syncStreamPanelSelection() {
	p := m.streamPanel
	if p == nil {
		return
	}
	// Update URL/key display for selected item
	if p.selectedIndex < len(p.targets) {
		p.keyInput.SetValue(p.targets[p.selectedIndex].Key)
		p.urlInput.SetValue(p.targets[p.selectedIndex].URL)
	} else if p.selectedIndex < p.totalItems()-1 {
		presetIdx := p.selectedIndex - len(p.targets)
		if presetIdx < len(stream.Presets) {
			p.urlInput.SetValue(stream.Presets[presetIdx].URL)
			p.keyInput.SetValue("")
		}
	}
}

// --- View ---

func (m *Model) renderStreamPanel() string {
	p := m.streamPanel
	if p == nil {
		return ""
	}

	contentWidth := m.boxInnerWidth(m.mainColumnWidth())
	if contentWidth < 60 {
		return "Terminal too small for stream panel (need >= 60 columns)"
	}

	// Left: platform list
	left := m.renderStreamPanelLeft()
	// Right: config/status — fills remaining space
	right := m.renderStreamPanelRight(contentWidth)

	return lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right)
}

func (m *Model) renderStreamPanelLeft() string {
	p := m.streamPanel
	var lines []string

	cursorColor := lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)
	activeColor := lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	dimColor := lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Bold(true)
	bold := lipgloss.NewStyle().Bold(true)

	lines = append(lines, bold.Render("Platforms"))
	lines = append(lines, "")

	// Configured targets
	for i, t := range p.targets {
		preset := stream.PresetByID(t.Name)
		displayName := t.Name
		if preset != nil {
			displayName = preset.Name
		}

		prefix := "  "
		if i == p.selectedIndex {
			prefix = cursorColor.Render("> ")
		}

		check := "✗ "
		if t.Enabled {
			check = activeColor.Render("✓ ")
		}

		lines = append(lines, prefix+check+displayName)
	}

	// Presets
	lines = append(lines, "")
	lines = append(lines, dimColor.Render("── Add ──"))

	for i, preset := range stream.Presets {
		// Skip if already added
		found := false
		for _, t := range p.targets {
			if t.Name == preset.ID {
				found = true
				break
			}
		}
		if found {
			continue
		}

		idx := len(p.targets) + i
		prefix := "  "
		if idx == p.selectedIndex {
			prefix = cursorColor.Render("> ")
		}

		lines = append(lines, prefix+"  "+preset.Name)
	}

	// Custom
	customIdx := p.totalItems() - 1
	prefix := "  "
	if customIdx == p.selectedIndex {
		prefix = cursorColor.Render("> ")
	}
	lines = append(lines, prefix+"  + Custom...")

	content := strings.Join(lines, "\n")

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("6")).
		Padding(0, 1).
		Render(content)
}

func (m *Model) renderStreamPanelRight(w int) string {
	p := m.streamPanel
	var lines []string

	bold := lipgloss.NewStyle().Bold(true)
	green := lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	red := lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	yellow := lipgloss.NewStyle().Foreground(lipgloss.Color("3"))

	// FFmpeg status
	check := stream.CheckFFmpeg()
	if !check.Available {
		lines = append(lines, red.Render("⚠ FFmpeg not available"))
		// Show first 2 lines of error (install hints)
		errLines := strings.SplitN(check.Error, "\n", 4)
		for _, el := range errLines[:min(len(errLines), 3)] {
			lines = append(lines, dim.Render(el))
		}
	} else {
		lines = append(lines, green.Render("● FFmpeg "+check.Version)+" "+dim.Render(check.Path))
	}

	// Show font status
	fontPath := stream.FindCJKFont()
	if fontPath != "" {
		lines = append(lines, green.Render("● CJK Font")+" "+dim.Render(filepath.Base(fontPath)))
	} else {
		lines = append(lines, yellow.Render("⚠ No CJK font")+" "+dim.Render("Chinese chars may show as boxes"))
	}

	lines = append(lines, "")
	lines = append(lines, bold.Render("Configuration"))

	// Show selected item details
	if p.selectedIndex < len(p.targets) {
		t := p.targets[p.selectedIndex]
		preset := stream.PresetByID(t.Name)

		name := t.Name
		if preset != nil {
			name = preset.Name
		}

		lines = append(lines, "")
		lines = append(lines, fmt.Sprintf("  Name:    %s", name))
		lines = append(lines, fmt.Sprintf("  URL:     %s", t.URL))

		if p.editingField == "key" {
			lines = append(lines, "  Key:     "+p.keyInput.View())
		} else {
			masked := "not set"
			if t.Key != "" {
				if len(t.Key) > 8 {
					masked = t.Key[:4] + "..." + t.Key[len(t.Key)-4:]
				} else {
					masked = "****"
				}
			}
			lines = append(lines, fmt.Sprintf("  Key:     %s  %s", masked, dim.Render("[e] edit")))
		}

		enabled := "No"
		if t.Enabled {
			enabled = green.Render("Yes")
		}
		lines = append(lines, fmt.Sprintf("  Enabled: %s", enabled))

		if preset != nil {
			lines = append(lines, "")
			lines = append(lines, dim.Render("  "+preset.KeyHint))
		}

	} else if p.selectedIndex < p.totalItems()-1 {
		// Preset selected
		presetIdx := p.selectedIndex - len(p.targets)
		if presetIdx < len(stream.Presets) {
			preset := stream.Presets[presetIdx]
			lines = append(lines, "")
			lines = append(lines, fmt.Sprintf("  Platform: %s", preset.Name))
			lines = append(lines, fmt.Sprintf("  URL:      %s", preset.URL))
			lines = append(lines, fmt.Sprintf("  Protocol: %s", preset.Protocol))
			lines = append(lines, "")
			lines = append(lines, dim.Render("  "+preset.KeyHint))
			lines = append(lines, "")
			lines = append(lines, "  Press Enter to add this platform")
		}
	} else {
		// Custom selected
		if p.customMode {
			lines = append(lines, "")
			lines = append(lines, "Add Custom Target:")
			lines = append(lines, "")
			if p.editingField == "name" || p.editingField == "" {
				lines = append(lines, "  Name: "+p.nameInput.View())
			}
			if p.editingField == "url" || p.editingField == "key" {
				lines = append(lines, "  Name: "+p.nameInput.Value())
				lines = append(lines, "  URL:  "+p.urlInput.View())
			}
			if p.editingField == "key" {
				lines = append(lines, "  Name: "+p.nameInput.Value())
				lines = append(lines, "  URL:  "+p.urlInput.Value())
				lines = append(lines, "  Key:  "+p.keyInput.View())
			}
		} else {
			lines = append(lines, "")
			lines = append(lines, "  Press Enter to add a custom RTMP target")
		}
	}

	// Streaming status
	if m.streamManager != nil && m.streamManager.IsRunning() {
		lines = append(lines, "")
		lines = append(lines, green.Bold(true).Render("● Streaming"))
		statuses := m.streamManager.Status()
		for _, s := range statuses {
			lines = append(lines, fmt.Sprintf("  %s: %s (%dKB sent)", s.Name, s.State, s.BytesSent/1024))
		}
	}

	// Message
	if p.message != "" {
		lines = append(lines, "")
		lines = append(lines, yellow.Render(p.message))
	}

	// Controls
	lines = append(lines, "")
	lines = append(lines, dim.Render("[s]tart  [x]stop  [e]dit  [d]elete  [?]help  [Esc]close"))

	content := strings.Join(lines, "\n")

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("6")).
		Padding(0, 1).
		Render(content)
}

// --- Open/Close ---

func (m *Model) openStreamPanel() {
	cfg := stream.StreamConfig{}
	if m.config != nil {
		cfg = m.config.Stream
	}
	m.streamPanel = newStreamPanel(cfg)
}

func (m *Model) closeStreamPanel() {
	// Save targets back to config
	if m.streamPanel != nil && m.config != nil {
		m.config.Stream.Targets = m.streamPanel.targets
		if err := m.saveConfig(); err != nil {
			m.streamPanel.message = fmt.Sprintf("config save failed: %v", err)
			return
		}
	}
	m.streamPanel = nil
}

// persistStreamConfig saves the current panel targets to the config file.
func (m *Model) persistStreamConfig() {
	if m.streamPanel == nil || m.config == nil {
		return
	}
	m.config.Stream.Targets = m.streamPanel.targets
	if err := m.saveConfig(); err != nil {
		m.streamPanel.message = fmt.Sprintf("config save failed: %v", err)
	}
}

func activeGreen(s string) string {
	return lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Render(s)
}
