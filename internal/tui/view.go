package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/topcheer/ggcode/internal/subagent"
)

func (m Model) View() string {
	if m.quitting {
		return ""
	}

	title := m.styles.title.Render("ggcode \u2014 AI Coding Assistant")
	input := m.input.View()

	// Set content into viewport
	m.viewport.SetContent(m.renderOutput())

	// Pre-calculate status bar
	statusBar := m.renderStatusBar()

	var sb strings.Builder
	sb.WriteString(title)
	sb.WriteString("\n")

	// Render viewport content — only use viewport for scroll offset, not padding
	content := m.renderOutput()
	lines := strings.Split(content, "\n")
	totalLines := len(lines)
	if totalLines == 0 {
		totalLines = 1
	}
	// Calculate viewport height dynamically
	headerLines := 1 // title
	footerLines := 2 // input + help
	if statusBar != "" {
		footerLines += 2 // status bar lines
	}
	if m.autoCompleteActive && len(m.autoCompleteItems) > 0 {
		footerLines += len(m.autoCompleteItems) + 1
	}
	visibleLines := m.height - headerLines - footerLines
	if visibleLines < 1 {
		visibleLines = 1
	}
	// Apply scroll offset
	offset := m.viewport.YOffset()
	maxOffset := totalLines - visibleLines
	if maxOffset < 0 {
		maxOffset = 0
	}
	if offset > maxOffset {
		offset = maxOffset
	}
	if offset < 0 {
		offset = 0
	}
	// Render visible lines only
	start := offset
	end := offset + visibleLines
	if end > totalLines {
		end = totalLines
	}
	for i := start; i < end; i++ {
		sb.WriteString(lines[i])
		sb.WriteString("\n")
	}
	// Pad remaining lines with newlines to keep input at bottom
	for i := end; i < start+visibleLines; i++ {
		sb.WriteString("\n")
	}

	// Render autocomplete overlay above input
	if m.autoCompleteActive && len(m.autoCompleteItems) > 0 {
		sb.WriteString(m.renderAutoComplete())
	}

	// Render status bar during loading
	if statusBar != "" {
		sb.WriteString("\n")
		sb.WriteString(statusBar)
		sb.WriteString("\n")
	}

	sb.WriteString(input)

	if !m.loading && m.pendingApproval == nil && m.pendingDiffConfirm == nil {
		modeStr := fmt.Sprintf("[mode: %s]", m.mode)
		agentStr := ""
		if m.subAgentMgr != nil {
			n := m.subAgentMgr.RunningCount()
			if n > 0 {
				agentStr = fmt.Sprintf(" [agents: %d running]", n)
			}
		}
		sb.WriteString(m.styles.prompt.Render("\n  " + modeStr + agentStr + " /help /sessions /resume /export /model /provider /mode /clear /exit | Shift+Tab toggle mode | Ctrl+C interrupt | Ctrl+D quit | PgUp/PgDn scroll"))
	}

	return sb.String()
}

func (m Model) renderOutput() string {
	var sb strings.Builder
	output := m.output.String()
	if output != "" {
		output = strings.TrimRight(output, "\n")
		sb.WriteString(output)
		if m.loading && m.spinner.IsActive() {
			sb.WriteString("\n")
			sb.WriteString(m.spinner.String())
		} else if m.loading {
			sb.WriteString("\u258c")
		}
		sb.WriteString("\n\n")
	}
	return sb.String()
}

func (m Model) renderApprovalOptions(options []approvalOption, cursor int) string {
	var sb strings.Builder
	maxLabel := 0
	for _, opt := range options {
		label := fmt.Sprintf("%s (%s)", opt.label, opt.shortcut)
		if len(label) > maxLabel {
			maxLabel = len(label)
		}
	}
	sb.WriteString("\n")
	for i, opt := range options {
		label := fmt.Sprintf("%s (%s)", opt.label, opt.shortcut)
		if i == cursor {
			sb.WriteString(m.styles.approvalCursor.Render(fmt.Sprintf("  \u276f %-*s", maxLabel, label)))
			sb.WriteString("\n")
		} else {
			sb.WriteString(m.styles.approvalDim.Render(fmt.Sprintf("    %-*s", maxLabel, label)))
			sb.WriteString("\n")
		}
	}
	sb.WriteString(m.styles.prompt.Render("  \u2191/\u2193 navigate, Enter confirm, shortcut keys still work\n"))
	return sb.String()
}

func (m Model) renderAutoComplete() string {
	if len(m.autoCompleteItems) == 0 {
		return ""
	}

	// Limit visible items
	maxVisible := 8
	start := 0
	if len(m.autoCompleteItems) > maxVisible {
		start = m.autoCompleteIndex
		// Keep selection visible
		if start >= len(m.autoCompleteItems)-maxVisible/2 {
			start = len(m.autoCompleteItems) - maxVisible
		}
		if start < 0 {
			start = 0
		}
	}
	end := start + maxVisible
	if end > len(m.autoCompleteItems) {
		end = len(m.autoCompleteItems)
	}

	items := m.autoCompleteItems[start:end]

	// Find max width for alignment
	maxWidth := 0
	for _, item := range items {
		if len(item) > maxWidth {
			maxWidth = len(item)
		}
	}

	var sb strings.Builder
	sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render("\n  Commands:\n"))

	for i, item := range items {
		realIdx := start + i
		selected := realIdx == m.autoCompleteIndex

		if selected {
			sb.WriteString(lipgloss.NewStyle().
				Foreground(lipgloss.Color("226")).
				Background(lipgloss.Color("236")).
				Render(fmt.Sprintf("  \u25b6 %-*s", maxWidth, item)))
			sb.WriteString(" ")
			if desc, ok := SlashCommandDescriptions[item]; ok {
				sb.WriteString(lipgloss.NewStyle().
					Foreground(lipgloss.Color("8")).
					Background(lipgloss.Color("236")).
					Render(desc))
			}
			sb.WriteString("\n")
		} else {
			sb.WriteString(lipgloss.NewStyle().
				Foreground(lipgloss.Color("15")).
				Render(fmt.Sprintf("    %-*s", maxWidth, item)))
			sb.WriteString(" ")
			if desc, ok := SlashCommandDescriptions[item]; ok {
				sb.WriteString(lipgloss.NewStyle().
					Foreground(lipgloss.Color("8")).
					Render(desc))
			}
			sb.WriteString("\n")
		}
	}

	sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render("  Tab/Enter to select, Esc to cancel\n"))
	return sb.String()
}

func (m Model) renderStatusBar() string {
	if !m.loading {
		return ""
	}

	var sb strings.Builder

	// Main status line
	activity := m.statusActivity
	if activity == "" {
		activity = "Thinking..."
	}

	spinnerChar := ""
	if m.spinner.IsActive() {
		frame := m.spinner.CurrentFrame()
		spinnerChar = string(spinnerChars[frame%len(spinnerChars)])
	} else {
		spinnerChar = "⏳"
	}

	// Format tokens with commas
	tokens := fmt.Sprintf("%d", m.statusTokens)
	if len(tokens) > 3 {
		for i := len(tokens) - 3; i > 0; i -= 3 {
			tokens = tokens[:i] + "," + tokens[i:]
		}
	}

	// Format cost
	cost := fmt.Sprintf("%.4f", m.statusCost)
	if cost == "0.0000" {
		cost = "0.00"
	}

	line1 := fmt.Sprintf(" %s %s │ 📊 %s tokens │ 💰 $%s",
		spinnerChar, activity, tokens, cost)
	sb.WriteString(m.styles.statusBar.Render(line1))

	// Tool info line
	if m.statusToolCount > 0 || m.statusToolName != "" {
		sb.WriteString("\n ")
		if m.statusToolCount > 0 {
			sb.WriteString(fmt.Sprintf("🔧 %d tools used", m.statusToolCount))
			if m.statusToolName != "" {
				sb.WriteString(" │ ")
			}
		}
		if m.statusToolName != "" {
			sb.WriteString(fmt.Sprintf("%s", m.statusToolName))
			if m.statusToolArg != "" {
				arg := m.statusToolArg
				if len(arg) > 50 {
					arg = arg[:50] + "..."
				}
				sb.WriteString(fmt.Sprintf(": %s", arg))
			}
		}
	}

	// Subagent info line
	if m.subAgentMgr != nil && m.subAgentMgr.RunningCount() > 0 {
		agents := m.subAgentMgr.List()
		sb.WriteString("\n 🤖 ")
		first := true
		for _, a := range agents {
			if !first {
				sb.WriteString(" │ ")
			}
			first = false
			icon := "✅"
			if a.Status == subagent.StatusRunning {
				icon = "⏳"
			}
			sb.WriteString(fmt.Sprintf("%s %s (%d tools)", icon, a.ID, a.ToolCallCount))
		}
	}

	return sb.String()
}

func helpText() string {
	return `Available commands:
  /help              Show this help message
  /cost              Show current session cost stats
  /cost all          Show all session cost summary
  /sessions          List all saved sessions
  /resume <id>       Resume a previous session
  /export <id>       Export session to markdown file
  /model <name>      Switch model
  /provider <name>    Switch provider
  /clear             Clear conversation history
  /mcp               Show connected MCP servers and tools
  /memory            Show loaded memory files
  /memory list       List auto memory entries
  /memory clear      Clear all auto memories
  /undo              Undo the last file edit (checkpoint rollback)
  /checkpoints       List all file edit checkpoints

  /allow <tool>      Always allow a specific tool
  /plugins           List loaded plugins and their tools
  /image <path>       Attach an image file
  /fullscreen         Toggle fullscreen mode
  /mode <mode>       Set permission mode (supervised|plan|auto|bypass)
  /agents            List sub-agents
  /agent <id>        Show sub-agent details
  /agent cancel <id> Cancel a sub-agent

  /compact           Compress conversation history
  /todo              View todo list
  /todo clear        Clear todo list
  /bug               Report a bug with diagnostics
  /config            Show current configuration
  /config set <k> <v> Set a config value
  /status            Show current status
  /exit, /quit       Exit ggcode

Keyboard shortcuts:
  Tab               Autocomplete slash commands
  Esc               Cancel autocomplete
  \u2191/\u2193                Browse command history (or autocomplete)
  Shift+Tab         Toggle permission mode
  Ctrl+C             Interrupt current generation
  Ctrl+D             Exit

Mouse:
  Option+drag / Shift+drag  Select text to copy (bypasses app mouse capture)
  Mouse wheel              Scroll conversation output`
}

