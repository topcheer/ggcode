package agentruntime

import (
	"context"
	"strings"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/commands"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/lsp"
	"github.com/topcheer/ggcode/internal/mcp"
	"github.com/topcheer/ggcode/internal/memory"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/plugin"
	grpcplugin "github.com/topcheer/ggcode/internal/plugin/grpc"
	"github.com/topcheer/ggcode/internal/safego"

	"github.com/topcheer/ggcode/internal/metrics"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
	"github.com/topcheer/ggcode/internal/sessionidx"
	"github.com/topcheer/ggcode/internal/subagent"
	"github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/tunnel"
	"github.com/topcheer/ggcode/internal/webrtc"
)

type InteractiveRuntimeCore struct {
	Registry         *tool.Registry
	MCPManager       *plugin.MCPManager
	PluginManager    *plugin.Manager
	GRPCPluginMgr    *grpcplugin.Manager
	AutoMemory       *memory.AutoMemory
	ProjectAutoMem   *memory.AutoMemory
	SaveMemoryTool   *tool.SaveMemoryTool
	DeleteMemoryTool *tool.DeleteMemoryTool
	StartupAssets    StartupAssets
	StartupNotices   []string
	CommandManager   *commands.Manager
	Tunnel           *TunnelHost
	configAccess     *configAccess
	configPath       string
	workingDir       string

	mcpCtx          context.Context
	mcpCancel       context.CancelFunc
	mcpHotReload    *MCPHotReload
	configHotReload *ConfigHotReload
	skillHotReload  *SkillHotReload

	samplingProvider provider.Provider
}

func BuildInteractiveRuntimeCore(cfg *config.Config, workingDir string, policy permission.PermissionPolicy) (*InteractiveRuntimeCore, error) {

	lsp.SetServerOverrides(cfg.LSPServers)

	registry := tool.NewRegistry()
	if err := tool.RegisterBuiltinTools(registry, policy, workingDir, cfg.ProtectedPaths, tool.NewSandboxPolicy(cfg.Sandbox.Enabled, cfg.Sandbox.AllowNetwork, cfg.Sandbox.ExtraWritePaths)); err != nil {
		return nil, err
	}

	mergedServers, mergeWarnings := mcp.MergeStartupServersWithDeleted(workingDir, cfg.MCPServers, cfg.DeletedMCPServers)

	mergedServers, gateWarnings := applyProjectMCPGate(mergedServers, workingDir)

	notices := append([]string{}, mergeWarnings...)
	notices = append(notices, gateWarnings...)
	for _, warning := range notices {
		debug.Log("mcp", "%s", warning)
	}

	core := &InteractiveRuntimeCore{}
	mcpMgr := plugin.NewMCPManager(mergedServers, registry, workingDir)
	_ = registry.Register(tool.ListMCPCapabilitiesTool{Runtime: mcpMgr})
	_ = registry.Register(tool.GetMCPPromptTool{Runtime: mcpMgr})
	_ = registry.Register(tool.ReadMCPResourceTool{Runtime: mcpMgr})
	mcpMgr.SetSamplingHandler(newMCPSamplingHandler(func() provider.Provider {
		return core.samplingProvider
	}, cfg.MCPSamplingDisabled))
	mcpMgr.SetElicitationHandler(newMCPElicitationHandler(registry))

	pluginMgr := plugin.NewManager()
	pluginMgr.LoadAll(cfg.Plugins)
	if err := pluginMgr.RegisterTools(registry); err != nil {
		return nil, err
	}

	grpcMgr := grpcplugin.NewManager(workingDir)
	grpcConfigs := buildGRPCPluginConfigs(cfg.Plugins)
	_ = grpcMgr.LoadAll(grpcConfigs, registry)

	autoMem := memory.NewAutoMemory()
	projectAutoMem := memory.NewProjectAutoMemory(workingDir)
	saveMemoryTool := tool.NewSaveMemoryTool(autoMem, projectAutoMem)
	_ = registry.Register(saveMemoryTool)

	deleteMemoryTool := tool.NewDeleteMemoryTool(autoMem, projectAutoMem)
	_ = registry.Register(deleteMemoryTool)

	_ = registry.Register(tool.NewListMemoryTool(autoMem, projectAutoMem))

	_ = registry.Register(tool.NewRecallExperienceTool(workingDir))

	// Session-recall index: own-session writes + sibling-session read-only
	// aggregation (background prewarm; refresh cadence per manager defaults).
	if sessDir, err := session.DefaultDir(); err == nil {
		idxMgr := sessionidx.NewManager(sessDir, "")
		registry.SetSessionIndex(idxMgr)
		safego.Go("agentruntime.sessionidxStart", idxMgr.Start)
	}

	_ = registry.Register(tool.NewRecallToolFlowTool())

	cfgAccess := NewConfigAccess(cfg, workingDir)
	_ = registry.Register(tool.ConfigTool{Access: cfgAccess})

	startupAssets := LoadInteractiveStartupAssets(workingDir, autoMem)
	commandMgr := startupAssets.CommandManager
	commandMgr.SetExtraProviders(func() []*commands.Command {
		return BuildMCPSkillCommands(mcpMgr.SnapshotMCP())
	})

	th := NewTunnelHost()
	upgCfg := tunnel.DefaultUpgradeConfig()
	upgCfg.Enabled = !cfg.P2P.Disabled
	th.SetP2PEnabled(webrtc.HostPeerFactory(), upgCfg)

	*core = InteractiveRuntimeCore{
		Registry:         registry,
		MCPManager:       mcpMgr,
		PluginManager:    pluginMgr,
		GRPCPluginMgr:    grpcMgr,
		AutoMemory:       autoMem,
		ProjectAutoMem:   projectAutoMem,
		SaveMemoryTool:   saveMemoryTool,
		DeleteMemoryTool: deleteMemoryTool,
		StartupAssets:    startupAssets,
		StartupNotices:   notices,
		CommandManager:   commandMgr,
		Tunnel:           th,
		configAccess:     cfgAccess,
		workingDir:       workingDir,
	}
	return core, nil
}

func (c *InteractiveRuntimeCore) SetConfigAgent(ag *agent.Agent) {
	if c.configAccess != nil {
		c.configAccess.SetAgent(ag)
	}
	if ag != nil {

		c.samplingProvider = ag.Provider()
	}
}

func (c *InteractiveRuntimeCore) SetConfigPath(path string) {
	c.configPath = path
}

func (c *InteractiveRuntimeCore) SetConfigUINotify(fn func()) {
	if c.configAccess != nil {
		c.configAccess.SetUINotify(fn)
	}
}

func (c *InteractiveRuntimeCore) StartBackgroundServices() {
	if c.MCPManager != nil {
		c.mcpCtx, c.mcpCancel = context.WithCancel(context.Background())
		c.MCPManager.StartBackground(c.mcpCtx)

		c.mcpHotReload = NewMCPHotReload(config.ConfigDir(), c.workingDir, c.MCPManager)
		c.mcpHotReload.Start(c.mcpCtx)
	}

	if c.CommandManager != nil {
		dirs := c.CommandManager.WatchedDirs()
		if len(dirs) > 0 {
			c.skillHotReload = NewSkillHotReload(c.CommandManager, dirs)
			c.skillHotReload.Start(c.mcpCtx)
		}
	}

	if c.configAccess != nil && c.configPath != "" {
		c.configHotReload = NewConfigHotReload(c.configPath, c.configAccess)
		c.configHotReload.Start(c.mcpCtx)
	}
}

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

	if c.Registry != nil {
		c.Registry.CloseAll()
	}

	lsp.ShutdownAll()
}

func NewSkillTool(
	commandMgr *commands.Manager,
	mcpMgr *plugin.MCPManager,
	prov provider.Provider,
	registry *tool.Registry,
	agentFactory func(provider.Provider, interface{}, string, int) subagent.AgentRunner,
	workingDir string,
	onUsage func(provider.TokenUsage),
	onMetric func(metrics.MetricEvent),
	systemPromptBuilder func(task, agentType string) string,
) tool.SkillTool {
	return tool.SkillTool{
		Skills:              commandMgr,
		NameLister:          commandMgr,
		Runtime:             mcpMgr,
		Provider:            prov,
		Tools:               registry,
		AgentFactory:        agentFactory,
		WorkingDir:          workingDir,
		OnUsage:             onUsage,
		OnMetric:            onMetric,
		SystemPromptBuilder: systemPromptBuilder,
	}
}

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
