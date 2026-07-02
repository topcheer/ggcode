package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/agentruntime"
	"github.com/topcheer/ggcode/internal/checkpoint"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/harness"
	"github.com/topcheer/ggcode/internal/image"
	"github.com/topcheer/ggcode/internal/memory"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/subagent"
	"github.com/topcheer/ggcode/internal/tool"
)

// RunPipe executes the agent in non-interactive pipe mode.
// Returns the exit code (0=success, 1=failure).
func RunPipe(cfg *config.Config, cfgPath, prompt string, allowedTools, allowedDirs []string, outputPath string, bypass bool, noHarness bool, readOnlyAllowedDirs []string) int {
	prov, resolved, err := ResolveProvider(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}

	workingDir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolving working directory: %v\n", err)
		return 1
	}

	// Setup permission: non-interactive, but honor explicit bypass mode and
	// always include the live workspace so worker subprocesses can fully operate
	// inside their assigned directory even when the config lives elsewhere.
	allowedDirs = effectivePipeAllowedDirs(cfg, cfgPath, workingDir, allowedDirs)
	rules := make(map[string]permission.Decision)
	for name, perm := range cfg.ToolPerms {
		switch config.ToolPermission(perm) {
		case "allow":
			rules[name] = permission.Allow
		case "deny":
			rules[name] = permission.Deny
		}
	}
	mode := pipePermissionMode(bypass, cfg.DefaultMode)
	policy := permission.NewConfigPolicyWithModeAndReadOnlyDirs(rules, allowedDirs, readOnlyAllowedDirs, mode)

	// Apply allowedTools filter
	if len(allowedTools) > 0 {
		for _, t := range allowedTools {
			policy.SetOverride(t, permission.Allow)
		}
	}

	// Setup tools (after policy so sandbox checks can be wired)
	var ag *agent.Agent
	core, err := agentruntime.BuildInteractiveRuntimeCore(cfg, workingDir, policy)
	if err != nil {
		fmt.Fprintf(os.Stderr, "building runtime core: %v\n", err)
		return 1
	}
	registry := core.Registry
	core.StartBackgroundServices()
	defer core.Close()

	// Load project memory documents.
	projectMem, projectMemFiles, _ := memory.LoadProjectMemory(workingDir)

	autoMem := core.AutoMemory
	projectAutoMem := core.ProjectAutoMem
	saveMemoryTool := core.SaveMemoryTool
	commandMgr := core.CommandManager
	skillAgentFactory := func(prov provider.Provider, tools interface{}, systemPrompt string, maxTurns int) subagent.AgentRunner {
		a := agent.NewAgent(prov, tools.(*tool.Registry), systemPrompt, maxTurns)
		a.SetWorkingDir(ag.WorkingDir())
		return a
	}
	_ = registry.Register(agentruntime.NewSkillTool(commandMgr, core.MCPManager, prov, registry, skillAgentFactory, workingDir, nil, nil))
	acpClientMgr := agentruntime.NewACPClientManager(workingDir, policy, func(_ context.Context, _ string, _ string) permission.Decision {
		return permission.Deny
	})
	if len(acpClientMgr.Available()) > 0 {
		agentruntime.RegisterDelegateTool(registry, acpClientMgr, nil, workingDir, func() string {
			if ag != nil {
				return ag.WorkingDir()
			}
			return workingDir
		})
		defer acpClientMgr.CloseAll()
	}

	buildCurrentSystemPrompt := func() string {
		gitStatus := detectGitStatus(workingDir)
		systemPrompt := agentruntime.BuildInteractiveSystemPrompt(cfg, workingDir, mode, registry, commandMgr, autoMem, projectAutoMem, gitStatus, "")
		if projectMem != "" {
			systemPrompt += "\n\n## Project Memory\n" + projectMem
		}
		return systemPrompt
	}
	systemPrompt := buildCurrentSystemPrompt()

	// Setup agent
	maxIter := cfg.MaxIterations
	ag = agent.NewAgent(prov, registry, systemPrompt, maxIter)
	core.SetConfigAgent(ag)
	ag.SetProjectMemoryFiles(projectMemFiles)
	agentruntime.ApplyResolvedLimitsToAgent(ag, resolved)
	agentruntime.StartAsyncRelayModelLimitRefresh(cfg, resolved, ag, nil)
	ag.SetProbeKey(provider.MakeProbeKey(resolved.VendorID, resolved.BaseURL, resolved.Model))
	ag.SetPermissionPolicy(policy)
	ag.SetHookConfig(cfg.Hooks)
	ag.SetWorkingDir(workingDir)
	// Pipe mode has no session JSONL, but todo_write needs a session ID.
	// Use a PID-based pseudo ID so todos work during pipe execution and are
	// cleaned up automatically when the run ends (agent defer ClearTodos).
	ag.SetSessionID(fmt.Sprintf("pipe-%d", os.Getpid()))
	ag.SetCheckpointManager(checkpoint.NewManager(50))
	tool.SetPreWriteHook(tool.CheckpointSaver(ag.CheckpointManager()))
	ag.SetSupportsVision(resolved.SupportsVision)
	saveMemoryTool.SetAfterSave(func() {
		systemPrompt = buildCurrentSystemPrompt()
		ag.UpdateSystemPrompt(systemPrompt)
	})

	// Read stdin if available
	stdinData, err := readStdin()
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading stdin: %v\n", err)
		return 1
	}

	// Compose the full prompt (may include image from stdin)
	fullPrompt, imageBlocks := buildPipePrompt(prompt, stdinData)

	// Output destination
	var w io.Writer = os.Stdout
	if outputPath != "" {
		f, err := os.Create(outputPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "creating output file: %v\n", err)
			return 1
		}
		defer f.Close()
		w = f
	}

	// Auto-run routing: if harness.auto_run is enabled and --no-harness is not set,
	// check whether this prompt should be routed to harness instead of the normal agent.
	// Use fullPrompt (includes stdin data) for routing and harness goal.
	// Skip auto-run for image inputs (harness uses text-only goals).
	if !noHarness && len(imageBlocks) == 0 {
		if autoRunResult, err := checkPipeAutoRun(cfg, fullPrompt, workingDir, prov); err == nil && autoRunResult != nil {
			switch autoRunResult.Decision {
			case harness.RouteHarness:
				return runPipeHarness(autoRunResult, fullPrompt)
			case harness.RouteSuggest:
				fmt.Fprintf(os.Stderr, "harness auto-run: %s\n", autoRunResult.Message)
				// Fall through to normal agent
			}
		}
	}

	// Run agent non-interactively
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var hasError bool
	var agentErr error
	if imageBlocks != nil {
		agentErr = ag.RunStreamWithContent(ctx, imageBlocks, func(event provider.StreamEvent) {
			switch event.Type {
			case provider.StreamEventText:
				fmt.Fprint(w, event.Text)
			case provider.StreamEventToolCallDone:
				if line := formatPipeProgressEvent(event); line != "" {
					fmt.Fprintln(os.Stderr, line)
				}
			case provider.StreamEventToolResult:
				if line := formatPipeProgressEvent(event); line != "" {
					fmt.Fprintln(os.Stderr, line)
				}
			case provider.StreamEventError:
				fmt.Fprintf(os.Stderr, "error: %v\n", event.Error)
				hasError = true
			}
		})
	} else {
		agentErr = ag.RunStream(ctx, fullPrompt, func(event provider.StreamEvent) {
			switch event.Type {
			case provider.StreamEventText:
				fmt.Fprint(w, event.Text)
			case provider.StreamEventToolCallDone:
				if line := formatPipeProgressEvent(event); line != "" {
					fmt.Fprintln(os.Stderr, line)
				}
			case provider.StreamEventToolResult:
				if line := formatPipeProgressEvent(event); line != "" {
					fmt.Fprintln(os.Stderr, line)
				}
			case provider.StreamEventError:
				fmt.Fprintf(os.Stderr, "error: %v\n", event.Error)
				hasError = true
			}
		})
	}

	if agentErr != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", agentErr)
		return 1
	}
	if hasError {
		return 1
	}
	return 0
}

func pipeAllowedDirs(cfg *config.Config, cfgPath, workingDir string) []string {
	if cfg == nil {
		return nil
	}
	merged := cfg.ExpandAllowedDirs(workingDir)
	trimmed := strings.TrimSpace(cfgPath)
	if trimmed == "" {
		return dedupeStrings(merged)
	}
	configRelative := cfg.ExpandAllowedDirs(filepath.Dir(trimmed))
	return dedupeStrings(append(merged, configRelative...))
}

func effectivePipeAllowedDirs(cfg *config.Config, cfgPath, workingDir string, allowedDirs []string) []string {
	if len(allowedDirs) > 0 {
		return dedupeStrings(allowedDirs)
	}
	return pipeAllowedDirs(cfg, cfgPath, workingDir)
}

func pipePermissionMode(bypass bool, defaultMode string) permission.PermissionMode {
	if bypass {
		return permission.BypassMode
	}
	if m := permission.ParsePermissionMode(defaultMode); m != permission.SupervisedMode {
		return m
	}
	return permission.AutoMode
}

func dedupeStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

// readStdin reads all data from stdin if it's a pipe, otherwise returns "".
func readStdin() ([]byte, error) {
	stat, _ := os.Stdin.Stat()
	if (stat.Mode() & os.ModeCharDevice) != 0 {
		return nil, nil
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// registryToolNames extracts tool names from the registry.
func registryToolNames(r *tool.Registry) []string {
	tools := r.List()
	names := make([]string, len(tools))
	for i, t := range tools {
		names[i] = t.Name()
	}
	return names
}

// buildPipePrompt builds the prompt with optional image from stdin.
func buildPipePrompt(prompt string, stdinData []byte) (string, []provider.ContentBlock) {
	if stdinData == nil {
		return prompt, nil
	}

	// Check if stdin is an image
	mime := image.DetectMIME(stdinData)
	if mime != "" {
		img, err := image.Decode(stdinData)
		if err == nil {
			placeholder := image.Placeholder("stdin", img)
			fmt.Fprintf(os.Stderr, "Detected image from stdin: %s\n", placeholder)
			blocks := []provider.ContentBlock{
				provider.TextBlock(prompt),
				provider.ImageBlock(img.MIME, image.EncodeBase64(img)),
			}
			return "", blocks
		}
	}

	// Plain text
	return string(stdinData) + "\n\n" + prompt, nil
}

func formatPipeProgressEvent(event provider.StreamEvent) string {
	switch event.Type {
	case provider.StreamEventToolCallDone:
		name := strings.TrimSpace(event.Tool.Name)
		if name == "" {
			return ""
		}
		detail := summarizePipeToolArguments(event.Tool.Arguments)
		if detail == "" {
			return fmt.Sprintf("tool: %s", name)
		}
		return fmt.Sprintf("tool: %s %s", name, detail)
	case provider.StreamEventToolResult:
		text := strings.TrimSpace(event.Result)
		if text == "" {
			if event.IsError {
				return "tool result: error"
			}
			return ""
		}
		if idx := strings.IndexByte(text, '\n'); idx >= 0 {
			text = text[:idx]
		}
		text = strings.TrimSpace(text)
		if text == "" {
			return ""
		}
		if event.IsError {
			return "tool result: error — " + truncatePipeProgress(text, 120)
		}
		return "tool result: " + truncatePipeProgress(text, 120)
	default:
		return ""
	}
}

func summarizePipeToolArguments(raw json.RawMessage) string {
	var args map[string]any
	if err := json.Unmarshal(raw, &args); err != nil {
		return ""
	}
	for _, key := range []string{"path", "file_path", "directory", "url", "query", "pattern", "description", "job_id", "skill"} {
		if value := strings.TrimSpace(pipeArgString(args[key])); value != "" {
			return truncatePipeProgress(value, 100)
		}
	}
	for _, key := range []string{"command", "cmd"} {
		if value := strings.TrimSpace(pipeArgString(args[key])); value != "" {
			lines := strings.Split(value, "\n")
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				return truncatePipeProgress(line, 100)
			}
		}
	}
	return ""
}

func pipeArgString(value any) string {
	switch v := value.(type) {
	case string:
		return v
	default:
		return ""
	}
}

func truncatePipeProgress(text string, maxLen int) string {
	text = strings.TrimSpace(text)
	if len(text) <= maxLen {
		return text
	}
	if maxLen < 4 {
		return text[:maxLen]
	}
	return text[:maxLen-3] + "..."
}

func checkPipeAutoRun(cfg *config.Config, prompt string, workingDir string, prov provider.Provider) (*harness.AutoRunResult, error) {
	mode := cfg.Harness.AutoRunMode()
	if mode == "off" {
		return nil, nil
	}
	ctx := harness.RouteContext{
		Input:                 prompt,
		WorkingDir:            workingDir,
		LLMClassifierProvider: prov,
	}
	return harness.ShouldAutoRun(cfg, prompt, ctx)
}

func runPipeHarness(result *harness.AutoRunResult, prompt string) int {
	if result.Project == nil {
		fmt.Fprintln(os.Stderr, "harness auto-run: no project available. Run ggcode harness init first.")
		return 1
	}

	project := *result.Project
	cfg := result.Config
	if cfg == nil {
		loadedCfg, err := harness.LoadConfig(project.ConfigPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "harness auto-run: failed to load config: %v\n", err)
			return 1
		}
		cfg = loadedCfg
	}

	displayPrompt := prompt
	if len(prompt) > 60 {
		displayPrompt = prompt[:57] + "..."
	}
	fmt.Fprintf(os.Stderr, "🔀 Harness auto-run: %s\n", displayPrompt)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	opts := harness.RunTaskOptions{}
	svc := harness.NewRunService()
	runResult := svc.Run(ctx, harness.RunServiceInput{
		Project: project,
		Config:  cfg,
		Goal:    prompt,
		Runner:  harness.BinaryRunner{},
		Options: opts,
	})
	if runResult.Error != nil {
		fmt.Fprintf(os.Stderr, "harness auto-run failed: %v\n", runResult.Error)
		if runResult.Summary != nil && runResult.Summary.Result != nil && runResult.Summary.Result.Output != "" {
			fmt.Fprint(os.Stdout, runResult.Summary.Result.Output)
		}
		return 1
	}

	// Output the result
	fmt.Fprint(os.Stdout, harness.FormatRunServiceResult(runResult))
	return 0
}
