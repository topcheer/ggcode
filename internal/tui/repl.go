package tui

import (
	"context"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/term"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/checkpoint"
	"github.com/topcheer/ggcode/internal/commands"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/memory"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/plugin"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
	"github.com/topcheer/ggcode/internal/subagent"
	"github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/update"
)

// REPL connects the agent to the TUI model.
type REPL struct {
	model      Model
	agent      *agent.Agent
	program    *tea.Program
	store      session.Store
	resumeID   string
	mcpMgr     *plugin.MCPManager
	commandMgr *commands.Manager
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

func (r *REPL) SetMCPManager(mgr *plugin.MCPManager) {
	r.mcpMgr = mgr
	r.model.SetMCPManager(mgr)
}

// SetResumeID sets the session ID to resume.
func (r *REPL) SetResumeID(id string) {
	r.resumeID = id
}

// SetConfig passes the config to the model for /model and /provider commands.
func (r *REPL) SetConfig(cfg *config.Config) {
	r.model.SetConfig(cfg)
}

func (r *REPL) SetPluginManager(mgr *plugin.Manager) {
	r.model.SetPluginManager(mgr)
}

func (r *REPL) SetUpdateService(svc *update.Service) {
	r.model.SetUpdateService(svc)
}

func (r *REPL) SetCommandsManager(mgr *commands.Manager) {
	r.commandMgr = mgr
	r.model.SetCommandsManager(mgr)
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

// SetSubAgentManager wires the sub-agent manager and registers sub-agent tools.
func (r *REPL) SetSubAgentManager(mgr *subagent.Manager, prov provider.Provider, tools *tool.Registry) {
	r.model.SetSubAgentManager(mgr)

	factory := func(prov provider.Provider, t interface{}, systemPrompt string, maxTurns int) subagent.AgentRunner {
		return agent.NewAgent(prov, t.(*tool.Registry), systemPrompt, maxTurns)
	}

	tools.Register(tool.SpawnAgentTool{
		Manager:      mgr,
		Provider:     prov,
		Tools:        tools,
		AgentFactory: factory,
	})
	tools.Register(tool.WaitAgentTool{Manager: mgr})
	tools.Register(tool.ListAgentsTool{Manager: mgr})

	// Notify TUI on live updates and completion.
	mgr.SetOnUpdate(func(sa *subagent.SubAgent) {
		if r.program != nil {
			r.program.Send(subAgentUpdateMsg{})
		}
	})
	mgr.SetOnComplete(func(sa *subagent.SubAgent) {
		if r.program != nil {
			r.program.Send(subAgentUpdateMsg{})
		}
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
	debug.Log("repl", "Run() START resumeID=%q", r.resumeID)
	// Initialize session
	if r.store != nil {
		if r.resumeID != "" {
			r.loadSession(r.resumeID)
		} else {
			r.createSession()
		}
	}
	r.primeInitialWindowSize(term.GetSize)

	r.program = tea.NewProgram(r.model, tea.WithAltScreen(), tea.WithMouseCellMotion())
	debug.Log("repl", "program created")
	if r.mcpMgr != nil {
		r.mcpMgr.SetOnUpdate(func(servers []plugin.MCPServerInfo) {
			if r.program != nil {
				r.program.Send(mcpServersMsg{Servers: servers})
			}
		})
		defer r.mcpMgr.Close()
	}
	if r.commandMgr != nil {
		stop := make(chan struct{})
		defer close(stop)
		go func() {
			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					if r.commandMgr.Reload() && r.program != nil {
						r.program.Send(skillsChangedMsg{})
					}
				case <-stop:
					return
				}
			}
		}()
	}

	// Wire the agent's approval handler into the TUI via channel bridge.
	r.agent.SetApprovalHandler(func(toolName string, input string) permission.Decision {
		if r.program == nil {
			return permission.Deny
		}
		resp := make(chan permission.Decision, 1)
		r.program.Send(ApprovalMsg{
			ToolName: toolName,
			Input:    input,
			Response: resp,
		})
		return <-resp
	})

	// NewProgram copies the model, so SetProgram on r.model is useless.
	// We can't Send before Run (deadlock). Instead, run in a goroutine and
	// send the reference once the event loop is up.
	debug.Log("repl", "scheduling setProgramMsg")
	// Send the startup logo with vendor/endpoint/model info.
	vendorName := ""
	endpointName := ""
	if r.model.config != nil {
		vendorName = r.model.config.Vendor
		endpointName = r.model.config.Endpoint
	}
	modelName := ""
	if r.model.config != nil {
		modelName = r.model.config.Model
	}
	go func() {
		time.Sleep(10 * time.Millisecond)
		r.program.Send(setProgramMsg{Program: r.program})
		r.program.Send(logoMsg{Vendor: vendorName, Endpoint: endpointName, Model: modelName})
		if r.mcpMgr != nil {
			r.mcpMgr.StartBackground(context.Background())
		}
	}()

	_, err := r.program.Run()
	debug.Log("repl", "program.Run() returned err=%v", err)
	if err == nil && r.store != nil && r.model.session != nil {
		// Save session on clean exit
		r.model.session.Messages = r.agent.Messages()
		_ = r.store.Save(r.model.session)
	}
	return err
}

func (r *REPL) primeInitialWindowSize(getSize func(fd uintptr) (int, int, error)) {
	width, height, err := getSize(os.Stdout.Fd())
	if err != nil || width <= 0 || height <= 0 {
		return
	}
	r.model.handleResize(width, height)
	r.model.rebuildMarkdownRenderer()
}

// createSession creates a fresh session and wires it into the model.
func (r *REPL) createSession() {
	vendor := ""
	endpoint := ""
	model := ""
	if r.model.config != nil {
		vendor = r.model.config.Vendor
		endpoint = r.model.config.Endpoint
		model = r.model.config.Model
	}
	ses := session.NewSession(vendor, endpoint, model)
	if err := r.store.Save(ses); err == nil {
		r.model.SetSession(ses, r.store)
		r.model.output.WriteString(r.model.t("session.new", ses.ID))
	}
}

// loadSession loads a previous session and restores messages into the agent.
func (r *REPL) loadSession(id string) {
	ses, err := r.store.Load(id)
	if err != nil {
		r.model.output.WriteString(r.model.t("session.resume_failed", id, err))
		r.model.output.WriteString(r.model.t("session.resume_fallback"))
		r.createSession()
		return
	}
	for _, msg := range ses.Messages {
		r.agent.AddMessage(msg)
	}
	r.model.SetSession(ses, r.store)
	title := ses.Title
	if title == "" {
		title = r.model.t("session.untitled")
	}
	r.model.output.WriteString(r.model.t("session.resume", ses.ID, title, len(ses.Messages)))
}
