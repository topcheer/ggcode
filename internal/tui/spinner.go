package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	spinnerChars = "⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏"
)

// spinnerMsg is sent by tea.Tick to animate the spinner.
type spinnerMsg struct{ time.Time }

// ToolSpinner manages spinner state for active tool execution.
type ToolSpinner struct {
	active    bool
	toolName  string
	frame     int
	style     lipgloss.Style
	startTime time.Time
}

// NewToolSpinner creates a new spinner.
func NewToolSpinner() *ToolSpinner {
	return &ToolSpinner{
		style: lipgloss.NewStyle().Foreground(lipgloss.Color("3")),
	}
}

// Start begins the spinner for a tool.
func (s *ToolSpinner) Start(toolName string) tea.Cmd {
	s.active = true
	s.toolName = toolName
	s.frame = 0
	s.startTime = time.Now()
	return s.tick()
}

// Stop ends the spinner.
func (s *ToolSpinner) Stop() {
	s.active = false
	s.toolName = ""
}

// IsActive returns whether the spinner is running.
func (s *ToolSpinner) IsActive() bool {
	return s.active
}

// CurrentFrame returns the current spinner frame index.
func (s *ToolSpinner) CurrentFrame() int {
	return s.frame
}

// String returns the spinner string with tool name.
func (s *ToolSpinner) String() string {
	if !s.active {
		return ""
	}
	char := string(spinnerChars[s.frame%len(spinnerChars)])
	elapsed := time.Since(s.startTime).Round(time.Millisecond)
	return s.style.Render(fmt.Sprintf(" %s %s (%s)", char, s.toolName, elapsed))
}

// tick returns a tea.Cmd that sends the next spinner frame.
func (s *ToolSpinner) tick() tea.Cmd {
	return tea.Tick(80*time.Millisecond, func(t time.Time) tea.Msg {
		return spinnerMsg{Time: t}
	})
}

// Update handles spinner animation frames.
func (s *ToolSpinner) Update(msg tea.Msg) tea.Cmd {
	if _, ok := msg.(spinnerMsg); !ok {
		return nil
	}
	if !s.active {
		return nil
	}
	s.frame++
	return s.tick()
}

// ToolStatusMsg is sent when a tool starts or finishes execution.
type ToolStatusMsg struct {
	ToolName string
	Running  bool // true = start, false = done
	Result   string
	Args     string // tool arguments summary
	IsError  bool
}

// bulletStyle renders the ● prefix for assistant/tool lines.
var bulletStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("14"))

// FormatToolStart formats the header line when a tool begins executing.
func FormatToolStart(toolName string, args string) string {
	var sb strings.Builder
	sb.WriteString(bulletStyle.Render("● "))
	sb.WriteString(toolName)
	if args != "" {
		sb.WriteString("\n  │ ")
		sb.WriteString(args)
	}
	sb.WriteString("\n")
	return sb.String()
}

// FormatToolResult formats the closing line when a tool finishes.
func FormatToolResult(msg ToolStatusMsg) string {
	var sb strings.Builder
	if msg.IsError {
		sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Render("  └ error: "))
	} else {
		sb.WriteString("  └ ")
	}
	result := strings.TrimSpace(msg.Result)
	if result == "" {
		result = "done"
	}
	// Truncate long results
	if len(result) > 200 {
		result = result[:200] + "..."
	}
	// Collapse multi-line results to single line
	result = strings.ReplaceAll(result, "\n", " ")
	result = strings.Join(strings.Fields(result), " ")
	sb.WriteString(result)
	sb.WriteString("\n\n")
	return sb.String()
}

// FormatToolStatus formats a tool completion message (legacy compat).
func FormatToolStatus(msg ToolStatusMsg) string {
	if msg.Running {
		return ""
	}
	return FormatToolResult(msg)
}
