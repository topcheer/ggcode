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
	"github.com/topcheer/ggcode/internal/image"
	"github.com/topcheer/ggcode/internal/memory"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/subagent"
	"github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/util"
)

// RunPipe executes the agent in non-interactive pipe mode.
// Returns the exit code (0=success, 1=failure).
func RunPipe(cfg *config.Config, cfgPath, prompt string, allowedTools, allowedDirs []string, outputPath string, bypass bool, readOnlyAllowedDirs []string) int {
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

	// Load project memory file list (for path-triggered dynamic loading).
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
		if hint := memory.BuildProjectMemoryHint(projectMemFiles); hint != "" {
			systemPrompt += "\n\n" + hint
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
	fullPrompt, imageBlocks, err := buildPipePrompt(prompt, stdinData)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}

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

	// Run agent non-interactively. Watch SIGINT/SIGTERM so CI-style stop
	// signals (`timeout --signal=TERM ... ggcode -p ...`) cancel the context
	// and let the deferred cleanup below (core.Close, ACP clients, output
	// file, checkpoints) run instead of killing the process outright (#722).
	sigCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithCancel(sigCtx)
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

// stdinIdleTimeout bounds how long readStdin waits for the *next* chunk of
// stdin data before giving up. A pipe whose writer never closes (e.g.
// `tail -f x | ggcode -p`, or a parent holding the write-end open) used to
// block io.ReadAll — and therefore all of startup — forever (#537).
// A var (not const) so tests can shorten it.
var stdinIdleTimeout = 30 * time.Second

// stdinChunkSize is the per-read buffer size for readStdin.
const stdinChunkSize = 32 * 1024

// stdinMaxBytes caps total buffered stdin data. Aligned with
// util.ReadLimitGeneral (the repo's generic-input limit family) so pipe mode
// gets the same "prevents unbounded memory allocation" guarantee (#722).
// A var (not const) so tests can shrink it.
var stdinMaxBytes int64 = util.ReadLimitGeneral

// readStdin reads all data from stdin if it's a pipe, otherwise returns nil.
// Empty piped input returns nil (not []byte{}) so buildPipePrompt's nil check
// correctly treats it as "no stdin data" (#537).
func readStdin() ([]byte, error) {
	// #402: a closed stdin fd (parent close(0) before exec — common in
	// cron/daemon/CI callers) makes Stat return EBADF with a nil stat;
	// dereferencing it panicked at startup. Treat as "no stdin data".
	stat, err := os.Stdin.Stat()
	if err != nil || (stat.Mode()&os.ModeCharDevice) != 0 {
		return nil, nil
	}
	// Read manually with a per-read idle deadline instead of io.ReadAll so a
	// stalled writer can't block startup indefinitely. A slow but flowing
	// stream (data arriving at least once per window) still completes.
	// The cumulative size is also capped (#722): `cat 4GB.bin | ggcode -p`
	// used to buffer everything (then copy it again into the prompt string),
	// ballooning RSS until the process got OOM-killed with no diagnosis.
	var buf []byte
	for {
		// Fresh chunk per iteration: on idle-timeout a reader goroutine stays
		// parked in os.Stdin.Read holding this buffer, so chunks must never be
		// reused across reads (#722).
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
				// Stalled pipe: keep whatever arrived so far and move on.
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

// readStdinChunk reads once from stdin with an idle timeout. On timeout the
// reader goroutine remains parked holding buf, so callers must pass a buffer
// that is not reused across calls (see readStdin).
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

// buildPipePrompt builds the prompt with optional image from stdin. Non-image
// stdin must be valid UTF-8 text; raw binaries are rejected with an error
// instead of being silently mangled into the prompt (#722).
func buildPipePrompt(prompt string, stdinData []byte) (string, []provider.ContentBlock, error) {
	if stdinData == nil {
		return prompt, nil, nil
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
			return "", blocks, nil
		}
	}

	// Plain text: reject binary input before it becomes a lossy string.
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
