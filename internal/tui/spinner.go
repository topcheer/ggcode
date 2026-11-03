package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

const (
	spinnerChars = "⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏"
)

var spinnerRunes = []rune(spinnerChars)

type spinnerMsg struct {
	time.Time
	generation uint64
}

type ToolSpinner struct {
	active     bool
	generation uint64
	label      string
	frame      int
	style      lipgloss.Style
	startTime  time.Time
}

func NewToolSpinner() *ToolSpinner {
	return &ToolSpinner{
		style: lipgloss.NewStyle().Foreground(lipgloss.Color("3")),
	}
}

func (s *ToolSpinner) Start(label string) tea.Cmd {
	s.generation++
	s.active = true
	s.label = label
	s.frame = 0
	s.startTime = time.Now()
	return s.tick(s.generation)
}

func (s *ToolSpinner) Stop() {
	s.generation++
	s.active = false
	s.label = ""
}

func (s *ToolSpinner) IsActive() bool {
	return s.active
}

func (s *ToolSpinner) CurrentFrame() int {
	return s.frame
}

func (s *ToolSpinner) Elapsed() time.Duration {
	if s.startTime.IsZero() {
		return 0
	}
	return time.Since(s.startTime).Round(time.Millisecond)
}

func (s *ToolSpinner) String() string {
	if !s.active {
		return ""
	}
	char := spinnerFrameGlyph(s.frame)
	return s.style.Render(fmt.Sprintf(" %s %s (%s)", char, s.label, s.Elapsed()))
}

func spinnerFrameGlyph(frame int) string {
	return string(spinnerRunes[frame%len(spinnerRunes)])
}

func (s *ToolSpinner) tick(generation uint64) tea.Cmd {
	return tea.Tick(150*time.Millisecond, func(t time.Time) tea.Msg {
		return spinnerMsg{Time: t, generation: generation}
	})
}

func (s *ToolSpinner) Update(msg tea.Msg) tea.Cmd {
	tick, ok := msg.(spinnerMsg)
	if !ok {
		return nil
	}
	if !s.active || tick.generation != s.generation {
		return nil
	}
	s.frame++
	return s.tick(s.generation)
}

func (m *Model) startLoadingSpinner(label string) tea.Cmd {
	label = strings.TrimSpace(label)
	if label == "" {
		label = m.t("status.thinking")
	}
	return m.spinner.Start(label)
}

func (m *Model) ensureLoadingSpinner(label string) tea.Cmd {
	if !m.loading || m.spinner.IsActive() {
		return nil
	}
	return m.startLoadingSpinner(label)
}

type ToolStatusMsg struct {
	ToolID      string
	ToolName    string
	DisplayName string
	Detail      string
	Activity    string
	Running     bool
	Result      string
	RawArgs     string
	Args        string
	IsError     bool
	Elapsed     time.Duration
}

func toolDisplayName(msg ToolStatusMsg) string {
	if msg.DisplayName != "" {
		return msg.DisplayName
	}
	return prettifyToolName(msg.ToolName)
}

func toolDetail(msg ToolStatusMsg) string {
	if msg.Detail != "" {
		return msg.Detail
	}
	if isTrivialToolDetail(msg.Args) {
		return ""
	}
	return msg.Args
}

func compactSingleLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.Join(strings.Fields(s), " ")
	return s
}
