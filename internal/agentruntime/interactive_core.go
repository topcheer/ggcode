package agentruntime

import (
	"context"
	"strings"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/commands"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/lsp"
	"github.com/topcheer/ggcode/internal/mcp"
	"github.com/topcheer/ggcode/internal/memory"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/plugin"
	grpcplugin "github.com/topcheer/ggcode/internal/plugin/grpc"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/subagent"
	"github.com/topcheer/ggcode/internal/tool"
)

type InteractiveRuntimeCore struct {
	Registry       *tool.Registry
	MCPManager     *plugin.MCPManager
	PluginManager  *plugin.Manager
	GRPCPluginMgr  *grpcplugin.Manager
	AutoMemory     *memory.AutoMemory
	ProjectAutoMem *memory.AutoMemory
	SaveMemoryTool *tool.SaveMemoryTool
	StartupAssets  StartupAssets
	CommandManager *commands.Manager
	Tunnel         *TunnelHost // unified tunnel event management
	configAccess   *configAccess

	mcpCtx    context.Context
	mcpCancel context.CancelFunc
}

func BuildInteractiveRuntimeCore(cfg *config.Config, workingDir string, policy permission.PermissionPolicy) (*InteractiveRuntimeCore, error) {
	// Apply LSP server overrides from config before registering tools.
	lsp.SetServerOverrides(cfg.LSPServers)

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

	// Load gRPC plugins (type: grpc)
	grpcMgr := grpcplugin.NewManager(workingDir)
	grpcConfigs := buildGRPCPluginConfigs(cfg.Plugins)
	_ = grpcMgr.LoadAll(grpcConfigs, registry) // errors are per-plugin, not fatal

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
		GRPCPluginMgr:  grpcMgr,
		AutoMemory:     autoMem,
		ProjectAutoMem: projectAutoMem,
		SaveMemoryTool: saveMemoryTool,
		StartupAssets:  startupAssets,
		CommandManager: commandMgr,
		Tunnel:         NewTunnelHost(),
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
	if c.Tunnel != nil {
		c.Tunnel.Close()
	}
	// Shut down tools that hold resources (e.g. browser Chrome processes).
	// This prevents resource leaks — without it, Chrome processes accumulate.
	if c.Registry != nil {
		c.Registry.CloseAll()
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
	systemPromptBuilder func(task, agentType string) string,
) tool.SkillTool {
	return tool.SkillTool{
		Skills:              commandMgr,
		Runtime:             mcpMgr,
		Provider:            prov,
		Tools:               registry,
		AgentFactory:        agentFactory,
		WorkingDir:          workingDir,
		OnUsage:             onUsage,
		SystemPromptBuilder: systemPromptBuilder,
	}
}

// buildGRPCPluginConfigs filters plugin config entries for type:"grpc"
// and converts them to GRPCPluginConfig.
func buildGRPCPluginConfigs(entries []config.PluginConfigEntry) []grpcplugin.GRPCPluginConfig {
	var out []grpcplugin.GRPCPluginConfig
	for _, e := range entries {
		if strings.ToLower(e.Type) != "grpc" {
			continue
		}
		if len(e.Command) == 0 {
			continue
		}
		out = append(out, grpcplugin.GRPCPluginConfig{
			Name:    e.Name,
			Command: e.Command,
			Env:     e.Env,
		})
	}
	return out
}
