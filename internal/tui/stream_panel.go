package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/topcheer/ggcode/internal/stream"
)

type streamPanelState struct {
	focus         int
	selectedIndex int
	scrollOffset  int
	keyInput      textinput.Model
	urlInput      textinput.Model
	nameInput     textinput.Model
	message       string
	editingField  string
	customMode    bool
	targets       []stream.StreamTarget
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

func (p *streamPanelState) totalItems() int {

	visible := 0
	for _, preset := range stream.Presets {
		if !p.presetAdded(preset) {
			visible++
		}
	}
	return len(p.targets) + visible + 1
}

func (p *streamPanelState) presetAdded(preset stream.PlatformPreset) bool {
	for _, t := range p.targets {
		if t.Name == preset.ID || t.Name == preset.Name {
			return true
		}
	}
	return false
}

func (p *streamPanelState) visiblePresets() []stream.PlatformPreset {
	out := make([]stream.PlatformPreset, 0, len(stream.Presets))
	for _, preset := range stream.Presets {
		if !p.presetAdded(preset) {
			out = append(out, preset)
		}
	}
	return out
}

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

				p.customMode = false
				p.nameInput.SetValue("")
				p.urlInput.SetValue("")
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

				p.customMode = false
				p.editingField = "key"
				p.keyInput.SetValue(p.targets[p.selectedIndex].Key)
				p.keyInput.Focus()
				return m, nil
			}
		case "d":
			if p.editingField == "" && p.selectedIndex < len(p.targets) {
				p.targets = append(p.targets[:p.selectedIndex], p.targets[p.selectedIndex+1:]...)
				if p.selectedIndex >= len(p.targets) && p.selectedIndex > 0 {
					p.selectedIndex--
				}
				p.message = "Target removed"
				m.persistStreamConfig()
				return m, nil
			}
		case "s":
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
				w, h := m.streamViewState.getSize()
				return snap, stream.TerminalSize{Cols: w, Rows: h}
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
		case "x":
			if m.streamManager != nil && m.streamManager.IsRunning() {
				m.streamManager.Stop()
				m.streamManager = nil
				p.message = "Stream stopped"
			} else {
				p.message = "Not streaming"
			}
			return m, nil
		case "?":
			p.message = "↑/k↓/j:nav  e:edit  d:delete  s:start  x:stop  enter:add"
			return m, nil
		}

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

	if p.editingField != "" {
		switch p.editingField {
		case "key":
			if p.selectedIndex < len(p.targets) {
				p.targets[p.selectedIndex].Key = p.keyInput.Value()
			}

		case "name":

			if p.customMode && p.editingField == "name" {
				p.editingField = "url"
				p.urlInput.Focus()
				return m, nil
			}
		}

		if p.customMode {
			switch p.editingField {
			case "url":
				p.editingField = "key"
				p.keyInput.Focus()
				return m, nil
			case "key":

				name := strings.TrimSpace(p.nameInput.Value())
				url := strings.TrimSpace(p.urlInput.Value())
				key := p.keyInput.Value()
				if name == "" || url == "" || key == "" {
					p.message = "All fields required"
					p.editingField = ""
					return m, nil
				}

				for _, t := range p.targets {
					if t.Name == name {
						p.message = fmt.Sprintf("%s already exists", name)
						p.editingField = ""
						return m, nil
					}
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

	totalItems := p.totalItems()
	if p.selectedIndex >= len(p.targets) && p.selectedIndex < totalItems-1 {

		vis := p.visiblePresets()
		presetIdx := p.selectedIndex - len(p.targets)
		if presetIdx >= len(vis) {
			return m, nil
		}
		preset := vis[presetIdx]

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

	if p.selectedIndex < len(p.targets) {
		p.keyInput.SetValue(p.targets[p.selectedIndex].Key)
		p.urlInput.SetValue(p.targets[p.selectedIndex].URL)
	} else if p.selectedIndex < p.totalItems()-1 {
		presetIdx := p.selectedIndex - len(p.targets)

		vis := p.visiblePresets()
		if presetIdx < len(vis) {
			p.urlInput.SetValue(vis[presetIdx].URL)
			p.keyInput.SetValue("")
		}
	}
}

func (m *Model) renderStreamPanel() string {
	p := m.streamPanel
	if p == nil {
		return ""
	}

	contentWidth := m.boxInnerWidth(m.mainColumnWidth())
	if contentWidth < 60 {
		return "Terminal too small for stream panel (need >= 60 columns)"
	}

	left := m.renderStreamPanelLeft()

	right := m.renderStreamPanelRight(contentWidth)

	joined := lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right)
	return m.renderContextBox("/stream", joined, lipgloss.Color("11"))
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

	lines = append(lines, "")
	lines = append(lines, dimColor.Render("── Add ──"))

	for i, preset := range p.visiblePresets() {
		idx := len(p.targets) + i
		prefix := "  "
		if idx == p.selectedIndex {
			prefix = cursorColor.Render("> ")
		}

		lines = append(lines, prefix+"  "+preset.Name)
	}

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

var (
	ffmpegCheckOnce sync.Once
	ffmpegCheckVal  stream.FFmpegCheck
	cjkFontOnce     sync.Once
	cjkFontVal      string
)

func cachedFFmpegCheck() stream.FFmpegCheck {
	ffmpegCheckOnce.Do(func() { ffmpegCheckVal = stream.CheckFFmpeg() })
	return ffmpegCheckVal
}

func cachedCJKFont() string {
	cjkFontOnce.Do(func() { cjkFontVal = stream.FindCJKFont() })
	return cjkFontVal
}

func (m *Model) renderStreamPanelRight(w int) string {
	p := m.streamPanel
	var lines []string

	bold := lipgloss.NewStyle().Bold(true)
	green := lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	red := lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	yellow := lipgloss.NewStyle().Foreground(lipgloss.Color("3"))

	check := cachedFFmpegCheck()
	if !check.Available {
		lines = append(lines, red.Render("⚠ FFmpeg not available"))

		errLines := strings.SplitN(check.Error, "\n", 4)
		for _, el := range errLines[:min(len(errLines), 3)] {
			lines = append(lines, dim.Render(el))
		}
	} else {
		lines = append(lines, green.Render("● FFmpeg "+check.Version)+" "+dim.Render(check.Path))
	}

	fontPath := cachedCJKFont()
	if fontPath != "" {
		lines = append(lines, green.Render("● CJK Font")+" "+dim.Render(filepath.Base(fontPath)))
	} else {
		lines = append(lines, yellow.Render("⚠ No CJK font")+" "+dim.Render("Chinese chars may show as boxes"))
	}

	lines = append(lines, "")
	lines = append(lines, bold.Render("Configuration"))

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
					masked = string([]rune(t.Key)[:4]) + "..." + string([]rune(t.Key)[len([]rune(t.Key))-4:])
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

		vis := p.visiblePresets()
		presetIdx := p.selectedIndex - len(p.targets)
		if presetIdx < len(vis) {
			preset := vis[presetIdx]
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

	if m.streamManager != nil && m.streamManager.IsRunning() {
		lines = append(lines, "")
		lines = append(lines, green.Bold(true).Render("● Streaming"))
		statuses := m.streamManager.Status()
		for _, s := range statuses {
			lines = append(lines, fmt.Sprintf("  %s: %s (%dKB sent)", s.Name, s.State, s.BytesSent/1024))
		}
	}

	if p.message != "" {
		lines = append(lines, "")
		lines = append(lines, yellow.Render(p.message))
	}

	lines = append(lines, "")
	lines = append(lines, dim.Render("[s]tart  [x]stop  [e]dit  [d]elete  [?]help  [Esc]close"))

	content := strings.Join(lines, "\n")

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("6")).
		Padding(0, 1).
		Render(content)
}

func (m *Model) openStreamPanel() {
	cfg := stream.StreamConfig{}
	if m.config != nil {
		cfg = m.config.Stream
	}
	m.streamPanel = newStreamPanel(cfg)
}

func (m *Model) closeStreamPanel() {

	if m.streamPanel != nil && m.config != nil {
		m.config.Stream.Targets = m.streamPanel.targets
		if err := m.saveConfig(); err != nil {
			m.streamPanel.message = fmt.Sprintf("config save failed: %v", err)
			return
		}
	}
	m.streamPanel = nil
}

func (m *Model) persistStreamConfig() {
	if m.streamPanel == nil || m.config == nil {
		return
	}
	m.config.Stream.Targets = m.streamPanel.targets
	if err := m.saveConfig(); err != nil {
		m.streamPanel.message = fmt.Sprintf("config save failed: %v", err)
	}
}
