package agentruntime

import (
	"context"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/commands"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/mcp"
	"github.com/topcheer/ggcode/internal/memory"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/plugin"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/subagent"
	"github.com/topcheer/ggcode/internal/tool"
)

type InteractiveRuntimeCore struct {
	Registry       *tool.Registry
	MCPManager     *plugin.MCPManager
	PluginManager  *plugin.Manager
	AutoMemory     *memory.AutoMemory
	ProjectAutoMem *memory.AutoMemory
	SaveMemoryTool *tool.SaveMemoryTool
	StartupAssets  StartupAssets
	CommandManager *commands.Manager
	configAccess   *configAccess // for SetAgent after agent creation

	mcpCtx    context.Context
	mcpCancel context.CancelFunc
}

func BuildInteractiveRuntimeCore(cfg *config.Config, workingDir string, policy permission.PermissionPolicy) (*InteractiveRuntimeCore, error) {
	registry := tool.NewRegistry()
	if err := tool.RegisterBuiltinTools(registry, policy, workingDir); err != nil {
		return nil, err
	}

	mergedServers, _ := mcp.MergeStartupServers(workingDir, cfg.MCPServers)
	mcpMgr := plugin.NewMCPManager(mergedServers, registry)
	_ = registry.Register(tool.ListMCPCapabilitiesTool{Runtime: mcpMgr})
	_ = registry.Register(tool.GetMCPPromptTool{Runtime: mcpMgr})
	_ = registry.Register(tool.ReadMCPResourceTool{Runtime: mcpMgr})

	pluginMgr := plugin.NewManager()
	pluginMgr.LoadAll(cfg.Plugins)
	if err := pluginMgr.RegisterTools(registry); err != nil {
		return nil, err
	}

	autoMem := memory.NewAutoMemory()
	projectAutoMem := memory.NewProjectAutoMemory(workingDir)
	saveMemoryTool := tool.NewSaveMemoryTool(autoMem, projectAutoMem)
	_ = registry.Register(saveMemoryTool)

	// Config tool — unified config management across all config files
	cfgAccess := NewConfigAccess(cfg, workingDir)
	_ = registry.Register(tool.ConfigTool{Access: cfgAccess})

	startupAssets := LoadInteractiveStartupAssets(workingDir, autoMem, projectAutoMem)
	commandMgr := startupAssets.CommandManager
	commandMgr.SetExtraProviders(func() []*commands.Command {
		return BuildMCPSkillCommands(mcpMgr.SnapshotMCP())
	})

	return &InteractiveRuntimeCore{
		Registry:       registry,
		MCPManager:     mcpMgr,
		PluginManager:  pluginMgr,
		AutoMemory:     autoMem,
		ProjectAutoMem: projectAutoMem,
		SaveMemoryTool: saveMemoryTool,
		StartupAssets:  startupAssets,
		CommandManager: commandMgr,
		configAccess:   cfgAccess,
	}, nil
}

// SetConfigAgent injects the agent into the config tool for provider hot-reload.
// Must be called after the agent is created. Without this, config changes to
// vendor/endpoint/model/api_key will persist to disk but won't take effect
// until the next session restart.
func (c *InteractiveRuntimeCore) SetConfigAgent(ag *agent.Agent) {
	if c.configAccess != nil {
		c.configAccess.SetAgent(ag)
	}
}

// SetConfigUINotify sets an optional callback for UI refresh after provider changes.
func (c *InteractiveRuntimeCore) SetConfigUINotify(fn func()) {
	if c.configAccess != nil {
		c.configAccess.SetUINotify(fn)
	}
}

// StartBackgroundServices launches all background services: MCP connections, etc.
// Must be called after UI callbacks are set (SetConfigUINotify, MCP OnUpdate, etc.)
// so that status changes are forwarded to the UI layer.
func (c *InteractiveRuntimeCore) StartBackgroundServices() {
	if c.MCPManager != nil {
		c.mcpCtx, c.mcpCancel = context.WithCancel(context.Background())
		c.MCPManager.StartBackground(c.mcpCtx)
	}
}

// Close stops all background services. Call on shutdown.
func (c *InteractiveRuntimeCore) Close() {
	if c.mcpCancel != nil {
		c.mcpCancel()
	}
	if c.MCPManager != nil {
		c.MCPManager.Close()
	}
}

// MCPManagerCancel returns the MCP cancel function for callers that need
// cleanup on exit (e.g. TUI's defer chain).
func (c *InteractiveRuntimeCore) MCPManagerCancel() context.CancelFunc {
	return c.mcpCancel
}

func NewSkillTool(
	commandMgr *commands.Manager,
	mcpMgr *plugin.MCPManager,
	prov provider.Provider,
	registry *tool.Registry,
	agentFactory func(provider.Provider, interface{}, string, int) subagent.AgentRunner,
	workingDir string,
	onUsage func(provider.TokenUsage),
) tool.SkillTool {
	return tool.SkillTool{
		Skills:       commandMgr,
		Runtime:      mcpMgr,
		Provider:     prov,
		Tools:        registry,
		AgentFactory: agentFactory,
		WorkingDir:   workingDir,
		OnUsage:      onUsage,
	}
}
