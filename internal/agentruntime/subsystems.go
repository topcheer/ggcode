package agentruntime

import (
	"context"
	"path/filepath"
	"time"

	"github.com/topcheer/ggcode/internal/acpclient"
	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/cron"
	"github.com/topcheer/ggcode/internal/metrics"
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

// ApplyIdleMaintenance enables sleep-time compute (r373) on the agent:
// an idle watcher that pre-compacts the context during user-idle windows
// so the next message doesn't pay compaction latency. Returns nil (and
// changes nothing) unless cfg.Enabled.
func ApplyIdleMaintenance(ag *agent.Agent, cfg config.IdleConfig) *agent.IdleMaintainer {
	if ag == nil || !cfg.Enabled {
		return nil
	}
	afterMin := cfg.AfterMin
	if afterMin <= 0 {
		afterMin = 10
	}
	ratio := cfg.PrecompactRatio
	if ratio <= 0 {
		ratio = 0.6
	}
	m := agent.NewIdleMaintainer(time.Duration(afterMin)*time.Minute, ratio,
		func() float64 {
			cm := ag.ContextManager()
			if cm == nil {
				return 0
			}
			return cm.UsageRatio()
		},
		func() { ag.StartPreCompact() })
	ag.SetIdleMaintainer(m)
	m.Start()
	return m
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
	availableModels func() []string,
	workingDir string,
	onUsage func(provider.TokenUsage),
	onMetric func(metrics.MetricEvent), // sa-218: sub-agent telemetry into the parent collector
	agentFactory func(provider.Provider, interface{}, string, int) subagent.AgentRunner,
	systemPromptBuilder func(task, agentType string) string,
) *subagent.Manager {
	mgr := subagent.NewManager(subCfg)
	if registry == nil || prov == nil || agentFactory == nil {
		return mgr
	}
	spawnTool := tool.SpawnAgentTool{
		Manager:             mgr,
		Provider:            prov,
		ProviderGetter:      providerGetter,
		AvailableModels:     availableModels,
		Tools:               registry,
		AgentFactory:        agentFactory,
		WorkingDir:          workingDir,
		OnUsage:             onUsage,
		OnMetric:            onMetric,
		SystemPromptBuilder: systemPromptBuilder,
		// r460: worktree-isolated sub-agent experience backflow (injected
		// here to keep the tool package free of an agent import).
		TrajBackflow: agent.TrajBackflowFromWorktree,
	}
	_ = registry.Register(spawnTool)
	// r377: trajectory-level best-of-N sampling on top of the spawn pipeline.
	// r380: AvailableModels enables per-candidate model validation for
	// heterogeneous ensembles (models=[...] on best_of_n).
	_ = registry.Register(tool.BestOfNTool{
		Manager:         mgr,
		Run:             BestOfNRunnerFor(spawnTool, mgr),
		AvailableModels: availableModels,
	})
	// r436: dynamic workflow orchestration (externalized task graph:
	// decompose -> parallel workers -> adversarial verify -> synthesize).
	_ = registry.Register(tool.WorkflowRunTool{
		Manager: mgr,
		Run:     WorkflowRunnerFor(spawnTool, mgr),
	})
	cascadeHints := tool.NewCascadeHintTracker()
	parentModel := tool.ParentModelFromProviderGetter(providerGetter)
	_ = registry.Register(tool.WaitAgentTool{
		Manager:      mgr,
		ParentModel:  parentModel,
		CascadeHints: cascadeHints,
	})
	_ = registry.Register(tool.ListAgentsTool{
		Manager:      mgr,
		ParentModel:  parentModel,
		CascadeHints: cascadeHints,
	})

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
		AvailableModels:     availableModels,
		Tools:               registry,
		AgentFactory:        agentFactory,
		WorkingDir:          workingDir,
		OnUsage:             onUsage,
		OnMetric:            onMetric, // #3296: named-agent forks stop being OTLP black boxes
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
