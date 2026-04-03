package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/cost"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/plugin"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
)

// ApprovalMsg is sent to TUI when agent requests permission.
type ApprovalMsg struct {
	ToolName string
	Input    string
	Response chan permission.Decision
}

// approvalResponseMsg is the user's response to an approval request.
type approvalResponseMsg struct {
	decision permission.Decision
}

// Model is the main Bubble Tea model for the REPL.
type Model struct {
	input       textinput.Model
	output      strings.Builder
	loading     bool
	quitting    bool
	width       int
	height      int
	styles      styles
	agent       *agent.Agent
	program     *tea.Program
	cancelFunc  func()
	policy      permission.PermissionPolicy
	spinner     *ToolSpinner
	history     []string
	historyIdx  int
	pendingApproval *ApprovalMsg
	session      *session.Session
	sessionStore session.Store
	costMgr     *cost.Manager
	costProvider string
	costModel    string
	lastCost    string
	mcpServers  []MCPInfo
	config      *config.Config
	pluginMgr   *plugin.Manager
}

// MCPInfo holds display info about a connected MCP server.
type MCPInfo struct {
	Name       string
	ToolNames  []string
	Connected  bool
}

type styles struct {
	user      lipgloss.Style
	assistant lipgloss.Style
	tool      lipgloss.Style
	error     lipgloss.Style
	prompt    lipgloss.Style
	title     lipgloss.Style
	approval  lipgloss.Style
	markdown  lipgloss.Style
}

// streamMsg wraps a string from the agent goroutine.
type streamMsg string

// doneMsg signals generation is complete.
type doneMsg struct{}

// errMsg signals an error.
type errMsg struct{ err error }

// toolStatusMsg wraps a tool status update.
type toolStatusMsg ToolStatusMsg

// costUpdateMsg carries token usage info from the agent goroutine.
type costUpdateMsg struct {
	InputTokens  int
	OutputTokens int
}

// NewModel creates a new TUI model.
func NewModel(a *agent.Agent, policy permission.PermissionPolicy) Model {
	ti := textinput.New()
	ti.Prompt = "> "
	ti.Placeholder = "Type a message..."
	ti.Focus()

	s := styles{
		user:      lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true),
		assistant: lipgloss.NewStyle().Foreground(lipgloss.Color("15")),
		tool:      lipgloss.NewStyle().Foreground(lipgloss.Color("3")),
		error:     lipgloss.NewStyle().Foreground(lipgloss.Color("9")),
		prompt:    lipgloss.NewStyle().Foreground(lipgloss.Color("6")),
		title: lipgloss.NewStyle().
			Foreground(lipgloss.Color("14")).
			Bold(true).
			MarginBottom(1),
		approval: lipgloss.NewStyle().
			Foreground(lipgloss.Color("11")).
			Bold(true),
	}

	return Model{
		input:   ti,
		styles:  s,
		agent:   a,
		policy:  policy,
		spinner: NewToolSpinner(),
		history: make([]string, 0, 100),
	}
}

// Init initializes the model.
func (m Model) Init() tea.Cmd {
	return textinput.Blink
}

// SetProgram sets the tea.Program reference for async sends.
func (m *Model) SetProgram(p *tea.Program) {
	m.program = p
}

// SetSession sets the active session and store.
func (m *Model) SetSession(ses *session.Session, store session.Store) {
	m.session = ses
	m.sessionStore = store
}

// Session returns the current session.
func (m *Model) Session() *session.Session {
	return m.session
}

// SetMCPServers stores MCP server info for the /mcp command.
func (m *Model) SetMCPServers(servers []MCPInfo) {
	m.mcpServers = servers
}

func (m *Model) SetPluginManager(mgr *plugin.Manager) {
	m.pluginMgr = mgr
}

func (m *Model) SetConfig(cfg *config.Config) {
	m.config = cfg
}

func (m *Model) providerNames() string {
	names := make([]string, 0, len(m.config.Providers))
	for name := range m.config.Providers {
		names = append(names, name)
	}
	return strings.Join(names, ", ")
}

// Update handles messages.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Handle spinner ticks first
	if m.spinner.IsActive() {
		if cmd := m.spinner.Update(msg); cmd != nil {
			_ = cmd
		}
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		// Handle approval mode
		if m.pendingApproval != nil {
			switch msg.String() {
			case "y", "Y":
				return m, m.handleApproval(permission.Allow)
			case "n", "N":
				return m, m.handleApproval(permission.Deny)
			case "a", "A":
				return m, m.handleApprovalAllowAlways()
			case "ctrl+c":
				return m, m.handleApproval(permission.Deny)
			}
			return m, nil
		}

		if m.loading {
			if msg.String() == "ctrl+c" {
				if m.cancelFunc != nil {
					m.cancelFunc()
				}
				m.loading = false
				m.spinner.Stop()
				m.output.WriteString("\n[interrupted]\n\n")
				return m, nil
			}
			return m, nil
		}

		switch msg.String() {
		case "ctrl+c", "ctrl+d":
			m.quitting = true
			return m, tea.Quit
		case "up":
			return m.handleHistoryUp()
		case "down":
			return m.handleHistoryDown()
		case "enter":
			text := strings.TrimSpace(m.input.Value())
			m.input.SetValue("")
			if text == "" {
				return m, nil
			}
			// Add to history
			m.history = append(m.history, text)
			m.historyIdx = len(m.history)
			return m, m.handleCommand(text)
		}

	case streamMsg:
		m.output.WriteString(string(msg))
		return m, nil

	case doneMsg:
		m.loading = false
		m.spinner.Stop()
		m.cancelFunc = nil
		if m.lastCost != "" {
			m.output.WriteString(m.styles.prompt.Render(m.lastCost + "\n"))
		}
		m.output.WriteString("\n")
		return m, nil

	case costUpdateMsg:
		m.lastCost = fmt.Sprintf("tokens: %d in / %d out", msg.InputTokens, msg.OutputTokens)
		if m.costMgr != nil {
			if sc, ok := m.costMgr.SessionCost("current"); ok {
				m.lastCost += fmt.Sprintf(" | session cost: %s", cost.FormatCost(sc.TotalCostUSD))
			}
		}
		return m, nil

	case errMsg:
		m.loading = false
		m.spinner.Stop()
		m.cancelFunc = nil
		m.output.WriteString(m.styles.error.Render(fmt.Sprintf("Error: %v\n\n", msg.err)))
		return m, nil

	case ApprovalMsg:
		// Agent is requesting approval
		m.pendingApproval = &msg
		m.output.WriteString(m.styles.approval.Render(
			fmt.Sprintf("\n\u26a0 Permission required: %s\n", msg.ToolName),
		))
		m.output.WriteString(fmt.Sprintf("  Input: %s\n", truncateString(msg.Input, 200)))
		m.output.WriteString(m.styles.prompt.Render("  [y] Allow once  [n] Deny  [a] Always allow\n"))
		return m, nil

	case toolStatusMsg:
		ts := ToolStatusMsg(msg)
		if ts.Running {
			m.spinner.Start(ts.ToolName)
		} else {
			m.spinner.Stop()
			m.output.WriteString(FormatToolStatus(ts))
		}
		return m, nil
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// View renders the UI.
func (m Model) View() string {
	if m.quitting {
		return ""
	}

	title := m.styles.title.Render("ggcode \u2014 AI Coding Assistant")
	input := m.input.View()

	var sb strings.Builder
	sb.WriteString(title)
	sb.WriteString("\n")

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

	sb.WriteString(input)

	if !m.loading && m.pendingApproval == nil {
		sb.WriteString(m.styles.prompt.Render("\n  /help /sessions /resume /export /model /provider /clear /exit | \u2191\u2193 history | Ctrl+C interrupt | Ctrl+D quit"))
	}

	return sb.String()
}

// handleCommand processes user input commands.
func (m *Model) handleCommand(text string) tea.Cmd {
	// Slash commands
	if strings.HasPrefix(text, "/") {
		parts := strings.Fields(text)
		cmd := strings.ToLower(parts[0])
		switch cmd {
		case "/exit", "/quit":
			m.quitting = true
			return tea.Quit
		case "/clear":
			m.output.Reset()
			return nil
		case "/help":
			m.output.WriteString(m.styles.assistant.Render(helpText()))
			m.output.WriteString("\n\n")
			return nil
		case "/model":
			if len(parts) > 1 {
				m.config.Model = parts[1]
				m.costModel = parts[1]
				// Recreate provider with new model
				if prov, err := provider.NewProvider(m.config); err == nil {
					m.agent.SetProvider(prov)
					m.output.WriteString(fmt.Sprintf("Switched model to: %s (provider: %s)\n\n", parts[1], m.config.Provider))
				} else {
					m.output.WriteString(m.styles.error.Render(fmt.Sprintf("Failed to switch model: %v\n\n", err)))
				}
			} else {
				m.output.WriteString(fmt.Sprintf("Current model: %s (provider: %s)\nUsage: /model <model-name>\n\n", m.config.Model, m.config.Provider))
			}
			return nil
		case "/provider":
			if len(parts) > 1 {
				newProvider := parts[1]
				if _, ok := m.config.Providers[newProvider]; !ok {
					m.output.WriteString(m.styles.error.Render(fmt.Sprintf("Unknown provider: %s (available: %v)\n\n", newProvider, m.providerNames())))
					return nil
				}
				m.config.Provider = newProvider
				m.costProvider = newProvider
				if prov, err := provider.NewProvider(m.config); err == nil {
					m.agent.SetProvider(prov)
					m.output.WriteString(fmt.Sprintf("Switched provider to: %s (model: %s)\n\n", newProvider, m.config.Model))
				} else {
					m.output.WriteString(m.styles.error.Render(fmt.Sprintf("Failed to switch provider: %v\n\n", err)))
				}
			} else {
				m.output.WriteString(fmt.Sprintf("Current provider: %s (model: %s)\nAvailable: %s\nUsage: /provider <name>\n\n", m.config.Provider, m.config.Model, m.providerNames()))
			}
			return nil
		case "/allow":
			if len(parts) > 1 {
				if m.policy != nil {
					m.policy.SetOverride(parts[1], permission.Allow)
					m.output.WriteString(fmt.Sprintf("\u2713 %s is now always allowed\n\n", parts[1]))
				}
			} else {
				m.output.WriteString("Usage: /allow <tool-name>\n\n")
			}
			return nil
		case "/cost":
			return m.handleCostCommand(parts)
		case "/sessions":
			return m.listSessions()
		case "/resume":
			if len(parts) > 1 {
				return m.resumeSession(parts[1])
			}
			m.output.WriteString("Usage: /resume <session-id>\n\n")
			return nil
		case "/export":
			if len(parts) > 1 {
				return m.exportSession(parts[1])
			}
			m.output.WriteString("Usage: /export <session-id>\n\n")
			return nil
		case "/plugins":
			return m.handlePluginsCommand()
		case "/mcp":
			return m.handleMCPCommand()
		default:
			m.output.WriteString(m.styles.error.Render(fmt.Sprintf("Unknown command: %s\n", text)))
			m.output.WriteString(m.styles.prompt.Render("Type /help for available commands\n\n"))
			return nil
		}
	}

	// Regular message → start agent
	m.output.WriteString(m.styles.user.Render("You: "))
	m.output.WriteString(text)
	m.output.WriteString("\n\n")
	m.output.WriteString(m.styles.assistant.Render("Assistant: "))

	// Save user message to session
	m.appendUserMessage(text)

	m.loading = true
	return m.startAgent(text)
}

// appendUserMessage saves a user message to the current session.
func (m *Model) appendUserMessage(text string) {
	if m.session == nil || m.sessionStore == nil {
		return
	}
	msg := provider.Message{
		Role:    "user",
		Content: []provider.ContentBlock{{Type: "text", Text: text}},
	}
	// Auto-generate title from first user message
	if m.session.Title == "" || m.session.Title == "New session" {
		if len(text) > 60 {
			m.session.Title = text[:57] + "..."
		} else {
			m.session.Title = text
		}
	}
	if store, ok := m.sessionStore.(*session.JSONLStore); ok {
		_ = store.AppendMessage(m.session, msg)
	} else {
		m.session.Messages = append(m.session.Messages, msg)
		_ = m.sessionStore.Save(m.session)
	}
}

// listSessions returns a command that lists all sessions.
func (m *Model) listSessions() tea.Cmd {
	return func() tea.Msg {
		if m.sessionStore == nil {
			return streamMsg("Session store not configured.\n\n")
		}
		sessions, err := m.sessionStore.List()
		if err != nil {
			return streamMsg(fmt.Sprintf("Error listing sessions: %v\n\n", err))
		}
		if len(sessions) == 0 {
			return streamMsg("No sessions found.\n\n")
		}
		var b strings.Builder
		b.WriteString("Sessions:\n\n")
		for i, s := range sessions {
			title := s.Title
			if title == "" {
				title = "untitled"
			}
			updated := s.UpdatedAt.Format(time.RFC3339)
			b.WriteString(fmt.Sprintf("  %d. %s  %s  (%s)\n", i+1, s.ID, title, updated))
		}
		b.WriteString("\nUse /resume <id> to continue a session\n\n")
		return streamMsg(b.String())
	}
}

// resumeSession returns a command that loads a session.
func (m *Model) resumeSession(id string) tea.Cmd {
	return func() tea.Msg {
		if m.sessionStore == nil {
			return streamMsg("Session store not configured.\n\n")
		}
		ses, err := m.sessionStore.Load(id)
		if err != nil {
			return streamMsg(fmt.Sprintf("Failed to resume session %s: %v\n\n", id, err))
		}
		// Restore messages into agent
		for _, msg := range ses.Messages {
			m.agent.AddMessage(msg)
		}
		m.session = ses
		title := ses.Title
		if title == "" {
			title = "untitled"
		}
		return streamMsg(fmt.Sprintf("Resumed session: %s \u2014 %s (%d messages)\n\n", ses.ID, title, len(ses.Messages)))
	}
}

// exportSession returns a command that exports a session to markdown.
func (m *Model) exportSession(id string) tea.Cmd {
	return func() tea.Msg {
		if m.sessionStore == nil {
			return streamMsg("Session store not configured.\n\n")
		}
		md, err := m.sessionStore.ExportMarkdown(id)
		if err != nil {
			return streamMsg(fmt.Sprintf("Error exporting session: %v\n\n", err))
		}
		filename := fmt.Sprintf("session-%s.md", id)
		if err := os.WriteFile(filename, []byte(md), 0644); err != nil {
			return streamMsg(fmt.Sprintf("Error writing file: %v\n\n", err))
		}
		absPath, _ := filepath.Abs(filename)
		return streamMsg(fmt.Sprintf("Exported session %s to %s\n\n", id, absPath))
	}
}

// handleApproval sends the user's decision back via the channel.
func (m *Model) handleApproval(d permission.Decision) tea.Cmd {
	pa := m.pendingApproval
	m.pendingApproval = nil
	if pa == nil || pa.Response == nil {
		return nil
	}
	go func() {
		pa.Response <- d
	}()
	return nil
}

// handleApprovalAllowAlways approves and adds to policy.
func (m *Model) handleApprovalAllowAlways() tea.Cmd {
	pa := m.pendingApproval
	m.pendingApproval = nil
	if pa != nil && m.policy != nil {
		m.policy.SetOverride(pa.ToolName, permission.Allow)
		m.output.WriteString(fmt.Sprintf("\u2713 %s is now always allowed\n\n", pa.ToolName))
	}
	if pa != nil && pa.Response != nil {
		go func() {
			pa.Response <- permission.Allow
		}()
	}
	return nil
}

// handleHistoryUp navigates up in command history.
func (m Model) handleHistoryUp() (tea.Model, tea.Cmd) {
	if m.historyIdx > 0 {
		m.historyIdx--
		m.input.SetValue(m.history[m.historyIdx])
	}
	return m, nil
}

// handleHistoryDown navigates down in command history.
func (m Model) handleHistoryDown() (tea.Model, tea.Cmd) {
	if m.historyIdx < len(m.history)-1 {
		m.historyIdx++
		m.input.SetValue(m.history[m.historyIdx])
	} else {
		m.historyIdx = len(m.history)
		m.input.SetValue("")
	}
	return m, nil
}

// startAgent returns a tea.Cmd that runs the agent in a goroutine.
func (m *Model) startAgent(text string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithCancel(context.Background())
		m.cancelFunc = cancel

		go func() {
			defer func() {
				if m.program != nil {
					m.program.Send(doneMsg{})
				}
				cancel()
			}()

			_ = m.agent.RunStream(ctx, text, func(event provider.StreamEvent) {
				if m.program == nil {
					return
				}
				switch event.Type {
				case provider.StreamEventText:
					m.program.Send(streamMsg(event.Text))
				case provider.StreamEventToolCallDone:
					m.program.Send(toolStatusMsg{
						ToolName: event.Tool.Name,
						Running:  true,
					})
				case provider.StreamEventError:
					m.program.Send(errMsg{err: event.Error})
				}
			})
		}()

		return nil
	}
}

// helpText returns the help message.
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
  /allow <tool>      Always allow a specific tool
  /plugins           List loaded plugins and their tools
  /exit, /quit       Exit ggcode

Keyboard shortcuts:
  \u2191/\u2193                Browse command history
  Ctrl+C             Interrupt current generation
  Ctrl+D             Exit`
}

// handleCostCommand displays cost statistics.
// handleMCPCommand shows connected MCP servers and their tools.
func (m *Model) handlePluginsCommand() tea.Cmd {
	if m.pluginMgr == nil {
		m.output.WriteString(m.styles.prompt.Render("Plugin manager not available.\n\n"))
		return nil
	}
	results := m.pluginMgr.Results()
	if len(results) == 0 {
		m.output.WriteString(m.styles.prompt.Render("No plugins loaded.\n\n"))
		return nil
	}
	m.output.WriteString(m.styles.title.Render("Plugins:\n"))
	for _, r := range results {
		status := "\u2713"
		style := m.styles.assistant
		if !r.Success {
			status = "\u2717"
			style = m.styles.error
		}
		m.output.WriteString(style.Render(fmt.Sprintf("  %s %s", status, r.Name)))
		if r.Error != nil {
			m.output.WriteString(style.Render(fmt.Sprintf(" - %v", r.Error)))
		}
		m.output.WriteString("\n")
		for _, tn := range r.Tools {
			m.output.WriteString(fmt.Sprintf("    - %s\n", tn))
		}
	}
	m.output.WriteString("\n")
	return nil
}

func (m *Model) handleMCPCommand() tea.Cmd {
	if len(m.mcpServers) == 0 {
		m.output.WriteString(m.styles.prompt.Render("No MCP servers configured.\n\n"))
		return nil
	}
	m.output.WriteString(m.styles.title.Render("MCP Servers:\n"))
	for _, srv := range m.mcpServers {
		status := "\u2713"
		if !srv.Connected {
			status = "\u2717"
		}
		m.output.WriteString(fmt.Sprintf("  %s %s (%d tools)\n", status, srv.Name, len(srv.ToolNames)))
		for _, tn := range srv.ToolNames {
			m.output.WriteString(fmt.Sprintf("    - %s\n", tn))
		}
	}
	m.output.WriteString("\n")
	return nil
}

func (m *Model) handleCostCommand(parts []string) tea.Cmd {
	if m.costMgr == nil {
		m.output.WriteString(m.styles.error.Render("Cost tracking not enabled.\n\n"))
		return nil
	}

	showAll := len(parts) > 1 && strings.ToLower(parts[1]) == "all"

	if showAll {
		all := m.costMgr.AllCosts()
		if len(all) == 0 {
			m.output.WriteString("No cost data yet.\n\n")
			return nil
		}
		m.output.WriteString(m.styles.title.Render("Cost Summary (all sessions)\n"))
		for _, sc := range all {
			m.output.WriteString(cost.FormatSessionCost(sc, time.Time{}) + "\n")
		}
		total := m.costMgr.TotalCost()
		m.output.WriteString(fmt.Sprintf("\n  Total: %s\n\n", cost.FormatCost(total)))
		return nil
	}

	// Current session
	if sc, ok := m.costMgr.SessionCost("current"); ok {
		m.output.WriteString(m.styles.title.Render("Current Session Cost\n"))
		m.output.WriteString(fmt.Sprintf("  Provider: %s\n", sc.Provider))
		m.output.WriteString(fmt.Sprintf("  Model:    %s\n", sc.Model))
		m.output.WriteString(fmt.Sprintf("  Input:    %s tokens\n", cost.FormatTokens(sc.InputTokens)))
		m.output.WriteString(fmt.Sprintf("  Output:   %s tokens\n", cost.FormatTokens(sc.OutputTokens)))
		m.output.WriteString(fmt.Sprintf("  Cost:     %s USD\n\n", cost.FormatCost(sc.TotalCostUSD)))
	} else {
		m.output.WriteString("No cost data for current session yet.\n\n")
	}
	return nil
}

// truncateString truncates a string to maxLen.
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
