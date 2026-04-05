package tui

import (
	"fmt"
	"regexp"
	"strconv"
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

// Elapsed returns how long the current tool has been running.
func (s *ToolSpinner) Elapsed() time.Duration {
	if s.startTime.IsZero() {
		return 0
	}
	return time.Since(s.startTime).Round(time.Millisecond)
}

// String returns the spinner string with tool name.
func (s *ToolSpinner) String() string {
	if !s.active {
		return ""
	}
	char := string(spinnerChars[s.frame%len(spinnerChars)])
	return s.style.Render(fmt.Sprintf(" %s %s (%s)", char, s.toolName, s.Elapsed()))
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
	Elapsed  time.Duration
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
func FormatToolResult(lang Language, msg ToolStatusMsg) string {
	var sb strings.Builder
	if msg.IsError {
		sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Render("  └ " + tr(lang, "tool.failed")))
	} else {
		sb.WriteString("  └ " + tr(lang, "tool.done"))
	}
	if msg.Elapsed > 0 {
		sb.WriteString(fmt.Sprintf(" (%s)", msg.Elapsed))
	}
	summary := summarizeToolResult(lang, msg)
	if summary != "" {
		sb.WriteString(": ")
		sb.WriteString(summary)
	}
	sb.WriteString("\n\n")
	return sb.String()
}

// FormatToolStatus formats a tool completion message (legacy compat).
func FormatToolStatus(msg ToolStatusMsg) string {
	if msg.Running {
		return ""
	}
	return FormatToolResult(LangEnglish, msg)
}

func summarizeToolResult(lang Language, msg ToolStatusMsg) string {
	result := strings.TrimSpace(msg.Result)
	if msg.IsError {
		if exit := firstMatch(result, `exit status \d+`); exit != "" {
			return exit
		}
		return msg.ToolName
	}

	switch msg.ToolName {
	case "run_command":
		if result == "" || result == "Command completed with no output." {
			return tr(lang, "tool.no_output")
		}
		return summarizeTextPayload(lang, result, tr(lang, "tool.output"))
	case "read_file", "web_fetch", "web_search", "git_diff", "git_status", "git_log":
		return summarizeTextPayload(lang, result, tr(lang, "tool.content"))
	case "glob":
		if result == "No files matched the pattern." {
			return pluralize(lang, 0, tr(lang, "tool.match"))
		}
		return pluralize(lang, len(nonEmptyLines(result)), tr(lang, "tool.match"))
	case "list_directory":
		return pluralize(lang, len(nonEmptyLines(result)), tr(lang, "tool.entry"))
	case "search_files":
		if match := regexp.MustCompile(`(?:Showing \d+ of |\bFound )(\d+) matches`).FindStringSubmatch(result); len(match) == 2 {
			return pluralize(lang, parseInt(match[1]), tr(lang, "tool.match"))
		}
		return summarizeTextPayload(lang, result, tr(lang, "tool.matches"))
	case "write_file", "edit_file":
		return compactSingleLine(result)
	default:
		if result == "" {
			return msg.ToolName
		}
		return summarizeTextPayload(lang, result, tr(lang, "tool.result"))
	}
}

func summarizeTextPayload(lang Language, result, noun string) string {
	lines := nonEmptyLines(result)
	if len(lines) == 0 {
		if lang == LangZhCN {
			return "无" + noun
		}
		return "no " + noun
	}
	if len(lines) == 1 {
		if lang == LangZhCN {
			return "1 行" + noun
		}
		return "1 line of " + noun
	}
	if lang == LangZhCN {
		return fmt.Sprintf("%d 行%s", len(lines), noun)
	}
	return fmt.Sprintf("%d lines of %s", len(lines), noun)
}

func pluralize(lang Language, n int, noun string) string {
	if lang == LangZhCN {
		return fmt.Sprintf("%d %s", n, noun)
	}
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func nonEmptyLines(s string) []string {
	raw := strings.Split(strings.TrimSpace(s), "\n")
	lines := make([]string, 0, len(raw))
	for _, line := range raw {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func compactSingleLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 120 {
		return s[:120] + "..."
	}
	return s
}

func firstMatch(s, pattern string) string {
	re := regexp.MustCompile(pattern)
	return re.FindString(s)
}

func parseInt(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}
