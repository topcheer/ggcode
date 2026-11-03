package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/agentruntime"
	"github.com/topcheer/ggcode/internal/checkpoint"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/image"
	"github.com/topcheer/ggcode/internal/memory"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/subagent"
	"github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/util"
)

func RunPipe(cfg *config.Config, cfgPath, prompt string, allowedTools, allowedDirs []string, outputPath string, bypass bool, readOnlyAllowedDirs []string, outputSchemaPath string) int {

	if wd, wdErr := os.Getwd(); wdErr == nil {
		if n, sweepErr := util.SweepStaleTempFiles(filepath.Join(wd, ".ggcode"), time.Hour); sweepErr == nil && n > 0 {
			debug.Log("pipe", "swept %d stale atomic-write temp file(s)", n)
		}

		if n, sweepErr := util.SweepStaleTempFiles(config.ConfigDir(), time.Hour); sweepErr == nil && n > 0 {
			debug.Log("pipe", "swept %d stale atomic-write temp file(s) in config dir", n)
		}

		tool.SweepStaleTodoFiles(30 * 24 * time.Hour)
	}

	prov, resolved, err := ResolveProvider(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}

	var outputSchema json.RawMessage
	if outputSchemaPath != "" {
		raw, readErr := os.ReadFile(outputSchemaPath)
		if readErr != nil {
			fmt.Fprintf(os.Stderr, "reading --output-schema: %v\n", readErr)
			return 1
		}
		if !json.Valid(raw) {
			fmt.Fprintf(os.Stderr, "--output-schema: %s is not valid JSON\n", outputSchemaPath)
			return 1
		}
		outputSchema = raw
		if s, ok := prov.(provider.ResponseSchemaSetter); ok {
			s.SetResponseSchema(raw)
		} else {
			fmt.Fprintf(os.Stderr, "note: %s does not support constrained decoding; falling back to prompt guidance + JSON check\n", resolved.VendorID)
			prompt = prompt + "\n\nRespond with a single JSON object that conforms to this JSON Schema (no prose, no markdown fences):\n" + string(raw)
		}
	}

	workingDir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolving working directory: %v\n", err)
		return 1
	}

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

	if len(allowedTools) > 0 {
		for _, t := range allowedTools {
			policy.SetOverride(t, permission.Allow)
		}
	}

	var ag *agent.Agent
	core, err := agentruntime.BuildInteractiveRuntimeCore(cfg, workingDir, policy)
	if err == nil {
		core.SetConfigPath(cfgPath)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "building runtime core: %v\n", err)
		return 1
	}
	registry := core.Registry
	core.StartBackgroundServices()

	if jm := registry.JobManager(); jm != nil {
		defer jm.ShutdownAll(2 * time.Second)
	}
	defer core.Close()

	projectMemFiles, _ := memory.ProjectMemoryFilesForPath(workingDir)

	autoMem := core.AutoMemory
	projectAutoMem := core.ProjectAutoMem
	saveMemoryTool := core.SaveMemoryTool
	commandMgr := core.CommandManager
	skillAgentFactory := func(prov provider.Provider, tools interface{}, systemPrompt string, maxTurns int) subagent.AgentRunner {
		a := agent.NewAgent(prov, tools.(*tool.Registry), systemPrompt, maxTurns)
		a.SetWorkingDir(ag.WorkingDir())
		return a
	}
	_ = registry.Register(agentruntime.NewSkillTool(commandMgr, core.MCPManager, prov, registry, skillAgentFactory, workingDir, nil, nil, nil))
	if os.Getenv("GGCODE_TRIAL_FORK") != "" {
		_ = registry.Register(&tool.TrialForkTool{
			Provider:     prov,
			Tools:        registry,
			AgentFactory: skillAgentFactory,
			WorkingDir:   workingDir,
		})
	}
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
		if hint := memory.BuildProjectMemoryHint(projectMemFiles, workingDir); hint != "" {
			systemPrompt += "\n\n" + hint
		}
		return systemPrompt
	}
	systemPrompt := buildCurrentSystemPrompt()

	maxIter := cfg.MaxIterations
	ag = agent.NewAgent(prov, registry, systemPrompt, maxIter)
	agentruntime.RegisterCompactContextTool(registry, ag)
	agentruntime.RegisterRecallCompactedTool(registry, ag)
	core.SetConfigAgent(ag)
	ag.SetProjectMemoryFiles(projectMemFiles)
	agentruntime.ApplyResolvedLimitsToAgent(ag, resolved)
	agentruntime.ApplyVerifyConfigToAgent(ag, cfg)
	agentruntime.StartAsyncRelayModelLimitRefresh(cfg, resolved, ag, nil)
	ag.SetProbeKey(provider.MakeProbeKey(resolved.VendorID, resolved.BaseURL, resolved.Model))
	ag.SetPermissionPolicy(policy)
	ag.SetHookConfig(cfg.Hooks)
	ag.SetAuxModel(resolved, cfg.AuxModel)
	ag.SetWorkingDir(workingDir)

	ag.SetSessionID(fmt.Sprintf("pipe-%d", os.Getpid()))
	ag.SetCheckpointManager(checkpoint.NewPersistentManager(50, workingDir))
	tool.SetPreWriteHook(tool.CheckpointSaver(ag.CheckpointManager()))
	ag.SetSupportsVision(resolved.SupportsVision)
	saveMemoryTool.SetAfterSave(func() {
		systemPrompt = buildCurrentSystemPrompt()
		ag.UpdateSystemPrompt(systemPrompt)
	})

	stdinData, err := readStdin()
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading stdin: %v\n", err)
		return 1
	}

	fullPrompt, imageBlocks, err := buildPipePrompt(prompt, stdinData)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}

	var w io.Writer = os.Stdout
	var outFile *os.File
	if outputPath != "" {
		f, err := os.Create(outputPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "creating output file: %v\n", err)
			return 1
		}
		outFile = f
		defer func() {
			if outFile != nil {
				_ = outFile.Close()
			}
		}()

		w = io.MultiWriter(os.Stdout, f)
	}

	sigCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithCancel(sigCtx)
	defer cancel()

	var hasError bool
	var agentErr error

	var writeErr error

	var schemaOut strings.Builder
	writeText := func(text string) {
		if outputSchema != nil {
			schemaOut.WriteString(text)
		}
		if _, err := fmt.Fprint(w, text); err != nil && writeErr == nil {
			writeErr = err
			fmt.Fprintf(os.Stderr, "warning: writing output failed: %v\n", err)
		}
	}
	if imageBlocks != nil {
		agentErr = ag.RunStreamWithContent(ctx, imageBlocks, func(event provider.StreamEvent) {
			switch event.Type {
			case provider.StreamEventText:
				writeText(event.Text)
			case provider.StreamEventReasoning:

				fmt.Fprint(os.Stderr, event.Text)
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
		}, "human")
	} else {
		agentErr = ag.RunStream(ctx, fullPrompt, func(event provider.StreamEvent) {
			switch event.Type {
			case provider.StreamEventText:
				writeText(event.Text)
			case provider.StreamEventReasoning:

				fmt.Fprint(os.Stderr, event.Text)
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
		}, "human")
	}

	if ctx.Err() == nil {

		captureText := fullPrompt
		if strings.TrimSpace(captureText) == "" {
			for _, blk := range imageBlocks {
				if blk.Type == "text" && strings.TrimSpace(blk.Text) != "" {
					captureText = blk.Text
					break
				}
			}
		}
		memory.CapturePreferences(workingDir, captureText)
	}

	if agentErr != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", agentErr)
		return 1
	}
	if hasError {
		return 1
	}

	if outputSchema != nil && !pipeOutputIsValidJSON(schemaOut.String()) {
		fmt.Fprintln(os.Stderr, "ggcode pipe: --output-schema set but the final response is not valid JSON (after fence stripping)")
		return 1
	}

	if outFile != nil {
		if err := outFile.Close(); err != nil {

			fmt.Fprintf(os.Stderr, "ggcode pipe: closing output file: %v\n", err)
			writeErr = err
		}
		outFile = nil
	}

	if writeErr != nil {
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

func pipeOutputIsValidJSON(s string) bool {
	if json.Valid([]byte(s)) {
		return true
	}
	if i := strings.Index(s, "```"); i >= 0 {
		rest := s[i+3:]
		if j := strings.Index(rest, "\n"); j >= 0 {
			rest = rest[j+1:]
		}
		if k := strings.LastIndex(rest, "```"); k >= 0 {
			rest = rest[:k]
		}
		return json.Valid([]byte(strings.TrimSpace(rest)))
	}
	return false
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

var stdinIdleTimeout = 30 * time.Second

const stdinChunkSize = 32 * 1024

var stdinMaxBytes int64 = util.ReadLimitGeneral

func readStdin() ([]byte, error) {

	stat, err := os.Stdin.Stat()
	if err != nil || (stat.Mode()&os.ModeCharDevice) != 0 {
		return nil, nil
	}

	var buf []byte
	for {

		chunk := make([]byte, stdinChunkSize)
		n, readErr := readStdinChunk(chunk, stdinIdleTimeout)
		buf = append(buf, chunk[:n]...)
		if int64(len(buf)) > stdinMaxBytes {
			return nil, fmt.Errorf("stdin input exceeded %d byte limit (%d bytes received); refusing to buffer unbounded input, write large data to a file and reference it from the prompt instead", stdinMaxBytes, len(buf))
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			if os.IsTimeout(readErr) {

				fmt.Fprintf(os.Stderr, "stdin read timed out after %v; continuing with %d bytes\n", stdinIdleTimeout, len(buf))
				break
			}
			return nil, readErr
		}
	}
	if len(buf) == 0 {
		return nil, nil
	}
	return buf, nil
}

func readStdinChunk(buf []byte, timeout time.Duration) (int, error) {
	type readResult struct {
		n   int
		err error
	}
	done := make(chan readResult, 1)
	go func() {
		n, err := os.Stdin.Read(buf)
		done <- readResult{n, err}
	}()
	select {
	case r := <-done:
		return r.n, r.err
	case <-time.After(timeout):
		return 0, os.ErrDeadlineExceeded
	}
}

func buildPipePrompt(prompt string, stdinData []byte) (string, []provider.ContentBlock, error) {
	if stdinData == nil {
		return prompt, nil, nil
	}

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
			return "", blocks, nil
		}
	}

	if !utf8.Valid(stdinData) {
		return "", nil, fmt.Errorf("stdin data is not valid UTF-8 text (looks like binary); pipe plain text or a recognized image format instead")
	}
	return string(stdinData) + "\n\n" + prompt, nil, nil
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
