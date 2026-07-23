package agentruntime

import (
	"context"
	"path/filepath"

	"github.com/topcheer/ggcode/internal/acpclient"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/cron"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/subagent"
	"github.com/topcheer/ggcode/internal/swarm"
	"github.com/topcheer/ggcode/internal/tool"
)

// CronStorePaths returns the per-session store path and the legacy
// workspace-scoped store path for migration.
func CronStorePaths(sessionID string) (sessionPath, legacyPath string) {
	base := filepath.Join(config.HomeDir(), ".ggcode")
	legacyPath = filepath.Join(base, "cron-jobs.json")
	if sessionID != "" {
		sessionPath = filepath.Join(base, "cron-jobs", sessionID+".json")
	}
	return
}

// NewSessionCronScheduler creates a cron scheduler that persists jobs
// per-session under ~/.ggcode/cron-jobs/<sessionID>.json.
// If sessionID is empty, the scheduler works without persistence until
// SetSession is called.
func NewSessionCronScheduler(sessionID, workingDir string, enqueue func(prompt string, queueIfBusy bool)) *cron.Scheduler {
	sessionPath, legacyPath := CronStorePaths(sessionID)
	scheduler := cron.NewScheduler(enqueue, sessionPath)

	// Migrate old workspace-scoped jobs to this session (once per workspace).
	if sessionPath != "" && workingDir != "" {
		cron.MigrateWorkspaceJobs(legacyPath, sessionPath, workingDir)
	}

	scheduler.Load()
	return scheduler
}

func RegisterCronTools(registry *tool.Registry, scheduler *cron.Scheduler) {
	if registry == nil || scheduler == nil {
		return
	}
	_ = registry.Register(tool.CronCreateTool{Scheduler: scheduler})
	_ = registry.Register(tool.CronDeleteTool{Scheduler: scheduler})
	_ = registry.Register(tool.CronListTool{Scheduler: scheduler})
	_ = registry.Register(tool.CronUpdateTool{Scheduler: scheduler})
	_ = registry.Register(tool.CronPauseTool{Scheduler: scheduler})
	_ = registry.Register(tool.CronResumeTool{Scheduler: scheduler})
	_ = registry.Register(tool.CronGetTool{Scheduler: scheduler})
}

func NewACPClientManager(
	workingDir string,
	policy permission.PermissionPolicy,
	approvalHandler func(context.Context, string, string) permission.Decision,
) *acpclient.ClientManager {
	mgr := acpclient.NewClientManager(workingDir, policy)
	if approvalHandler != nil {
		mgr.SetApprovalHandler(approvalHandler)
	}
	return mgr
}

func RegisterDelegateTool(
	registry *tool.Registry,
	mgr *acpclient.ClientManager,
	subMgrFn func() *subagent.Manager,
	workingDir string,
	workingDirFn func() string,
) {
	if registry == nil || mgr == nil || len(mgr.Available()) == 0 {
		return
	}
	_ = registry.Register(tool.DelegateTool{
		Manager:           mgr,
		SubAgentManagerFn: subMgrFn,
		WorkingDir:        workingDir,
		WorkingDirFn:      workingDirFn,
	})
}

func NewSubAgentManager(
	subCfg config.SubAgentConfig,
	registry *tool.Registry,
	prov provider.Provider,
	providerGetter func() provider.Provider,
	workingDir string,
	onUsage func(provider.TokenUsage),
	agentFactory func(provider.Provider, interface{}, string, int) subagent.AgentRunner,
	systemPromptBuilder func(task, agentType string) string,
) *subagent.Manager {
	mgr := subagent.NewManager(subCfg)
	if registry == nil || prov == nil || agentFactory == nil {
		return mgr
	}
	_ = registry.Register(tool.SpawnAgentTool{
		Manager:             mgr,
		Provider:            prov,
		ProviderGetter:      providerGetter,
		Tools:               registry,
		AgentFactory:        agentFactory,
		WorkingDir:          workingDir,
		OnUsage:             onUsage,
		SystemPromptBuilder: systemPromptBuilder,
	})
	_ = registry.Register(tool.WaitAgentTool{Manager: mgr})
	_ = registry.Register(tool.ListAgentsTool{Manager: mgr})

	// Named subagent templates (persisted per-workspace)
	tmplStore := subagent.NewTemplateStore(workingDir)
	_ = registry.Register(tool.CreateNamedAgentTool{Store: tmplStore})
	_ = registry.Register(tool.DeleteNamedAgentTool{Store: tmplStore})
	_ = registry.Register(tool.ListNamedAgentTool{Store: tmplStore})
	_ = registry.Register(tool.UseNamedAgentTool{
		Store:               tmplStore,
		Manager:             mgr,
		Provider:            prov,
		ProviderGetter:      providerGetter,
		Tools:               registry,
		AgentFactory:        agentFactory,
		WorkingDir:          workingDir,
		OnUsage:             onUsage,
		SystemPromptBuilder: systemPromptBuilder,
	})
	return mgr
}

func NewSwarmManager(
	cfg config.SwarmConfig,
	prov provider.Provider,
	registry *tool.Registry,
	onUsage func(provider.TokenUsage),
	factory func(provider.Provider, interface{}, string, int) swarm.AgentRunner,
	toolBuilder func([]string) interface{},
) *swarm.Manager {
	mgr := swarm.NewManager(cfg, prov, factory, toolBuilder)
	if onUsage != nil {
		mgr.SetUsageHandler(onUsage)
	}
	if registry != nil {
		_ = registry.Register(tool.TeamCreateTool{Manager: mgr})
		_ = registry.Register(tool.TeamDeleteTool{Manager: mgr})
		_ = registry.Register(tool.TeammateSpawnTool{Manager: mgr})
		_ = registry.Register(tool.TeammateListTool{Manager: mgr})
		_ = registry.Register(tool.TeammateShutdownTool{Manager: mgr})
		_ = registry.Register(tool.TeammateResultsTool{Manager: mgr})
		_ = registry.Register(tool.SwarmTaskCreateTool{Manager: mgr})
		_ = registry.Register(tool.SwarmTaskListTool{Manager: mgr})
		_ = registry.Register(tool.SwarmTaskClaimTool{Manager: mgr})
		_ = registry.Register(tool.SwarmTaskCompleteTool{Manager: mgr})
	}
	return mgr
}
