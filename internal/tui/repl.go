package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/checkpoint"
	"github.com/topcheer/ggcode/internal/commands"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/cost"
	"github.com/topcheer/ggcode/internal/memory"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/plugin"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
)

// REPL connects the agent to the TUI model.
type REPL struct {
	model    Model
	agent    *agent.Agent
	program  *tea.Program
	store    session.Store
	resumeID string
}

// NewREPL creates a new REPL with optional permission policy.
func NewREPL(a *agent.Agent, policy permission.PermissionPolicy) *REPL {
	m := NewModel(a, policy)
	return &REPL{
		model: m,
		agent: a,
	}
}

// SetSessionStore sets the session persistence store.
func (r *REPL) SetSessionStore(s session.Store) {
	r.store = s
}

// SetMCPServers passes MCP server info to the TUI model.
func (r *REPL) SetMCPServers(servers []MCPInfo) {
	r.model.SetMCPServers(servers)
}

// SetResumeID sets the session ID to resume.
func (r *REPL) SetResumeID(id string) {
	r.resumeID = id
}

// SetCostManager wires up cost tracking for the REPL.
func (r *REPL) SetCostManager(mgr *cost.Manager, providerName, modelName string) {
	r.model.costMgr = mgr
	r.model.costProvider = providerName
	r.model.costModel = modelName

	r.agent.SetUsageHandler(func(usage provider.TokenUsage) {
		if r.program == nil {
			return
		}
		tracker := mgr.GetOrCreateTracker("current", providerName, modelName)
		tracker.Record(cost.TokenUsage{
			InputTokens:  usage.InputTokens,
			OutputTokens: usage.OutputTokens,
		})
		r.program.Send(costUpdateMsg{
			InputTokens:  usage.InputTokens,
			OutputTokens: usage.OutputTokens,
		})
	})
}

// SetConfig passes the config to the model for /model and /provider commands.
func (r *REPL) SetConfig(cfg *config.Config) {
	r.model.SetConfig(cfg)
}

func (r *REPL) SetPluginManager(mgr *plugin.Manager) {
	r.model.SetPluginManager(mgr)
}

func (r *REPL) SetCustomCommands(cmds map[string]*commands.Command) {
	r.model.SetCustomCommands(cmds)
}

func (r *REPL) SetAutoMemory(am *memory.AutoMemory) {
	r.model.SetAutoMemory(am)
}

func (r *REPL) SetProjectMemoryFiles(files []string) {
	r.model.SetProjectMemoryFiles(files)
}

func (r *REPL) SetAutoMemoryFiles(files []string) {
	r.model.SetAutoMemoryFiles(files)
}

// SetCheckpointManager wires the checkpoint manager into the agent and REPL.
func (r *REPL) SetCheckpointManager(m *checkpoint.Manager) {
	r.agent.SetCheckpointManager(m)
	r.agent.SetDiffConfirm(func(filePath, diffText string) bool {
		return r.requestDiffConfirm(filePath, diffText)
	})
}

// requestDiffConfirm sends a diff confirmation request to the TUI and waits for response.
func (r *REPL) requestDiffConfirm(filePath, diffText string) bool {
	if r.program == nil {
		// Non-interactive (pipe) mode: auto-approve
		return true
	}
	resp := make(chan bool, 1)
	r.program.Send(DiffConfirmMsg{
		FilePath: filePath,
		DiffText: diffText,
		Response: resp,
	})
	return <-resp
}

// Run starts the REPL event loop.
func (r *REPL) Run() error {
	// Initialize session
	if r.store != nil {
		if r.resumeID != "" {
			r.loadSession(r.resumeID)
		} else {
			r.createSession()
		}
	}

	r.program = tea.NewProgram(r.model, tea.WithAltScreen(), tea.WithMouseCellMotion())
	r.model.SetProgram(r.program)

	_, err := r.program.Run()
	if err == nil && r.store != nil && r.model.session != nil {
		// Save session on clean exit
		r.model.session.Messages = r.agent.Messages()
		_ = r.store.Save(r.model.session)
	}
	return err
}

// createSession creates a fresh session and wires it into the model.
func (r *REPL) createSession() {
	ses := session.NewSession("", "")
	if err := r.store.Save(ses); err == nil {
		r.model.SetSession(ses, r.store)
		r.model.output.WriteString(fmt.Sprintf("New session: %s\n\n", ses.ID))
	}
}

// loadSession loads a previous session and restores messages into the agent.
func (r *REPL) loadSession(id string) {
	ses, err := r.store.Load(id)
	if err != nil {
		r.model.output.WriteString(fmt.Sprintf("Failed to resume session %s: %v\nStarting new session instead.\n\n", id, err))
		r.createSession()
		return
	}
	for _, msg := range ses.Messages {
		r.agent.AddMessage(msg)
	}
	r.model.SetSession(ses, r.store)
	title := ses.Title
	if title == "" {
		title = "untitled"
	}
	r.model.output.WriteString(fmt.Sprintf("Resumed session: %s \u2014 %s (%d messages)\n\n", ses.ID, title, len(ses.Messages)))
}
