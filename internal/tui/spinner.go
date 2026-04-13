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

var spinnerRunes = []rune(spinnerChars)

// spinnerMsg is sent by tea.Tick to animate the spinner.
type spinnerMsg struct {
	time.Time
	generation uint64
}

// ToolSpinner manages spinner state for active tool execution.
type ToolSpinner struct {
	active     bool
	generation uint64
	label      string
	frame      int
	style      lipgloss.Style
	startTime  time.Time
}

// NewToolSpinner creates a new spinner.
func NewToolSpinner() *ToolSpinner {
	return &ToolSpinner{
		style: lipgloss.NewStyle().Foreground(lipgloss.Color("3")),
	}
}

// Start begins the spinner for a tool.
func (s *ToolSpinner) Start(label string) tea.Cmd {
	s.generation++
	s.active = true
	s.label = label
	s.frame = 0
	s.startTime = time.Now()
	return s.tick(s.generation)
}

// Stop ends the spinner.
func (s *ToolSpinner) Stop() {
	s.generation++
	s.active = false
	s.label = ""
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
	char := spinnerFrameGlyph(s.frame)
	return s.style.Render(fmt.Sprintf(" %s %s (%s)", char, s.label, s.Elapsed()))
}

func spinnerFrameGlyph(frame int) string {
	return string(spinnerRunes[frame%len(spinnerRunes)])
}

// tick returns a tea.Cmd that sends the next spinner frame.
func (s *ToolSpinner) tick(generation uint64) tea.Cmd {
	return tea.Tick(80*time.Millisecond, func(t time.Time) tea.Msg {
		return spinnerMsg{Time: t, generation: generation}
	})
}

// Update handles spinner animation frames.
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

// ToolStatusMsg is sent when a tool starts or finishes execution.
type ToolStatusMsg struct {
	ToolID      string
	ToolName    string
	DisplayName string
	Detail      string
	Activity    string
	Running     bool // true = start, false = done
	Result      string
	RawArgs     string
	Args        string // raw tool arguments summary, used as a fallback only
	IsError     bool
	Elapsed     time.Duration
}

// assistantBulletStyle renders the ● prefix for assistant text/status lines.
var assistantBulletStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))

// compactionBulletStyle renders the ● prefix for compaction status lines.
var compactionBulletStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("13"))

// toolBulletStyle renders the ● prefix for tool call lines.
var toolBulletStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("14"))

// FormatToolStart formats the header line when a tool begins executing.
func FormatToolStart(msg ToolStatusMsg) string {
	var sb strings.Builder
	sb.WriteString(toolBulletStyle.Render("● "))
	sb.WriteString(formatToolInline(toolDisplayName(msg), toolDetail(msg)))
	if msg.Args != "" && toolDetail(msg) == "" {
		sb.WriteString("\n  │ ")
		sb.WriteString(msg.Args)
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
		return toolDisplayName(msg)
	}

	switch msg.ToolName {
	case "run_command":
		if result == "" || result == "Command completed with no output." {
			return tr(lang, "tool.no_output")
		}
		return summarizeTextPayload(lang, result, tr(lang, "tool.output"))
	case "start_command", "read_command_output", "wait_command", "stop_command", "write_command_input", "list_commands":
		if summary := summarizeAsyncCommandResult(result); summary != "" {
			return summary
		}
		return toolDisplayName(msg)
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
			return toolDisplayName(msg)
		}
		return summarizeTextPayload(lang, result, tr(lang, "tool.result"))
	}
}

func summarizeAsyncCommandResult(result string) string {
	lines := strings.Split(result, "\n")
	status := ""
	total := ""
	last := ""
	for _, line := range lines {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "Status: "):
			status = strings.TrimPrefix(line, "Status: ")
		case strings.HasPrefix(line, "Total lines: "):
			total = strings.TrimPrefix(line, "Total lines: ")
		case line == "" || strings.HasSuffix(line, ":"):
		case strings.HasPrefix(line, "Job ID:"),
			strings.HasPrefix(line, "Duration:"),
			strings.HasPrefix(line, "Timeout:"),
			strings.HasPrefix(line, "Buffered lines start at:"),
			strings.HasPrefix(line, "Error:"):
		default:
			last = line
		}
	}
	parts := make([]string, 0, 3)
	if status != "" {
		parts = append(parts, status)
	}
	if total != "" {
		parts = append(parts, total+" lines")
	}
	if last != "" && last != "(no output yet)" {
		parts = append(parts, last)
	}
	return strings.Join(parts, " • ")
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
	switch {
	case strings.HasSuffix(noun, "y") && len(noun) > 1:
		prev := noun[len(noun)-2]
		if !strings.ContainsRune("aeiou", rune(prev)) {
			return fmt.Sprintf("%d %sies", n, noun[:len(noun)-1])
		}
	case strings.HasSuffix(noun, "s"),
		strings.HasSuffix(noun, "x"),
		strings.HasSuffix(noun, "z"),
		strings.HasSuffix(noun, "ch"),
		strings.HasSuffix(noun, "sh"):
		return fmt.Sprintf("%d %ses", n, noun)
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
