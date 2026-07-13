package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	runtimedebug "runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/diff"
	"github.com/topcheer/ggcode/internal/hooks"
	"github.com/topcheer/ggcode/internal/metrics"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// executeToolWithPermission checks the permission policy before executing a tool.
// If the policy returns Ask, the approval handler is consulted interactively.
func (a *Agent) executeToolWithPermission(ctx context.Context, tc provider.ToolCallDelta) tool.Result {
	if err := ctx.Err(); err != nil {
		return tool.Result{Content: err.Error(), IsError: true}
	}
	// Don't log permission check — permission decision below is sufficient
	a.mu.RLock()
	policy := a.policy
	onApproval := a.onApproval
	a.mu.RUnlock()
	if policy != nil {
		decision, err := policy.Check(tc.Name, tc.Arguments)
		// Only log non-trivial permission decisions (deny/error), not every allow
		if err != nil {
			return tool.Result{Content: fmt.Sprintf("permission check error: %v", err), IsError: true}
		}

		switch decision {
		case permission.Deny:
			return tool.Result{
				Content: fmt.Sprintf("Permission denied for tool %q. The operation was blocked by the permission policy.", tc.Name),
				IsError: true,
			}
		case permission.Ask:
			if onApproval != nil {
				resp := onApproval(ctx, tc.Name, string(tc.Arguments))
				if resp == permission.Deny {
					return tool.Result{
						Content: fmt.Sprintf("Permission denied for tool %q. User rejected the request.", tc.Name),
						IsError: true,
					}
				}
			} else {
				// No approval handler → deny by default
				return tool.Result{
					Content: fmt.Sprintf("Permission denied for tool %q. No approval handler available (running in non-interactive mode).", tc.Name),
					IsError: true,
				}
			}
		}
	}

	toolStart := time.Now()
	result := a.executeTool(ctx, tc)
	toolDur := time.Since(toolStart)

	// Fire tool metric (non-blocking — caller must handle asynchronously).
	errMsg := ""
	if result.IsError {
		errMsg = truncateString(result.Content, 200)
	}
	a.emitMetric(metrics.MetricEvent{
		Timestamp:    time.Now(),
		Type:         "tool",
		ToolName:     tc.Name,
		ToolSuccess:  !result.IsError,
		ToolError:    errMsg,
		ToolDuration: toolDur,
	})
	return result
}

// executeTool runs pre-hooks, executes the tool, then runs post-hooks.
// File-editing tools (edit_file, write_file) are routed to executeFileTool
// for diff preview and checkpointing.
func (a *Agent) executeTool(ctx context.Context, tc provider.ToolCallDelta) tool.Result {
	if err := ctx.Err(); err != nil {
		return tool.Result{Content: err.Error(), IsError: true}
	}
	t, ok := a.tools.Get(tc.Name)
	if !ok {
		return tool.Result{Content: fmt.Sprintf("unknown tool: %s", tc.Name), IsError: true}
	}

	a.mu.RLock()
	hookCfg := a.hookConfig
	workDir := a.workingDir
	a.mu.RUnlock()
	env := hooks.HookEnv{
		ToolName:   tc.Name,
		WorkingDir: workDir,
		FilePath:   hooks.ExtractFilePath(tc.Name, string(tc.Arguments)),
		RawInput:   string(tc.Arguments),
	}

	// Pre-tool-use hooks
	preResult := hooks.RunPreHooks(hookCfg.PreToolUse, env)
	if !preResult.Allowed {
		return tool.Result{Content: preResult.Output, IsError: true}
	}

	// For file-editing tools: read old content, compute new, show diff, save checkpoint
	if tc.Name == "multi_file_edit" {
		if previewer, ok := t.(interface {
			PreviewChanges(input json.RawMessage) ([]tool.PlannedFileEdit, error)
		}); ok {
			return a.executeMultiFileTool(ctx, t, previewer, tc, env)
		}
	}
	if tc.Name == "edit_file" || tc.Name == "write_file" {
		return a.executeFileTool(ctx, t, tc, env)
	}

	// Sync working directory for tools that have a WorkingDir field.
	syncToolWorkingDir(t, workDir)

	// Execute the actual tool (with panic recovery)
	if err := ctx.Err(); err != nil {
		return tool.Result{Content: err.Error(), IsError: true}
	}
	toolStart := time.Now()
	result, err := a.safeExecute(t, ctx, tc.Arguments)
	toolDur := time.Since(toolStart)
	if err != nil {
		return tool.Result{Content: fmt.Sprintf("tool error: %v", err), IsError: true}
	}

	// Post-tool-use hooks
	postEnv := env
	postEnv.ToolSuccess = !result.IsError
	if result.IsError {
		postEnv.ToolError = truncateString(result.Content, 500)
	}
	postEnv.ToolResult = truncateString(result.Content, 4096)
	postEnv.ToolDuration = toolDur.String()
	postResult := hooks.RunPostHooks(hookCfg.PostToolUse, postEnv)
	if postResult.Output != "" {
		result.Content += "\n" + postResult.Output
	}

	return result
}

func (a *Agent) executeMultiFileTool(ctx context.Context, t tool.Tool, previewer interface {
	PreviewChanges(input json.RawMessage) ([]tool.PlannedFileEdit, error)
}, tc provider.ToolCallDelta, env hooks.HookEnv) tool.Result {
	a.mu.Lock()
	cpMgr := a.checkpoints
	diffFn := a.diffConfirm
	a.mu.Unlock()

	plans, err := previewer.PreviewChanges(tc.Arguments)
	if err == nil && diffFn != nil {
		if diffText, hasChanges := buildMultiFileDiffText(plans); hasChanges {
			label := fmt.Sprintf("%d files", len(plans))
			if len(plans) == 1 {
				label = plans[0].Path
			}
			if !diffFn(ctx, label, diffText) {
				return tool.Result{Content: "Multi-file write cancelled by user.", IsError: true}
			}
		}
	}

	a.mu.RLock()
	hookCfg := a.hookConfig
	a.mu.RUnlock()
	preResult := hooks.RunPreHooks(hookCfg.PreToolUse, env)
	if !preResult.Allowed {
		return tool.Result{Content: preResult.Output, IsError: true}
	}

	multiStart := time.Now()
	result, err := a.safeExecute(t, ctx, tc.Arguments)
	multiDur := time.Since(multiStart)
	if err != nil {
		return tool.Result{Content: fmt.Sprintf("tool error: %v", err), IsError: true}
	}

	if cpMgr != nil && len(plans) > 0 {
		var outcome tool.MultiFileEditContent
		if err := json.Unmarshal([]byte(result.Content), &outcome); err == nil {
			planByPath := make(map[string]tool.PlannedFileEdit, len(plans))
			for _, plan := range plans {
				planByPath[plan.Path] = plan
			}
			for _, path := range outcome.WrittenPaths {
				if plan, ok := planByPath[path]; ok {
					cpMgr.Save(path, plan.OldContent, plan.NewContent, tc.Name)
				}
			}
		}
	}

	postEnv := env
	postEnv.ToolSuccess = !result.IsError
	if result.IsError {
		postEnv.ToolError = truncateString(result.Content, 500)
	}
	postEnv.ToolResult = truncateString(result.Content, 4096)
	postEnv.ToolDuration = multiDur.String()
	postResult := hooks.RunPostHooks(hookCfg.PostToolUse, postEnv)
	if postResult.Output != "" {
		result.Content += "\n" + postResult.Output
	}
	return result
}

// safeExecute calls t.Execute with panic recovery and context-aware cancellation.
// If the tool panics (e.g. nil pointer dereference from an unset dependency), it returns
// an error result instead of crashing the entire application.
//
// The tool runs in a goroutine. If ctx is cancelled (e.g. user pressed Esc/Ctrl+C),
// safeExecute returns immediately with a cancellation result instead of blocking
// forever on a tool that ignores its context parameter. The goroutine may continue
// running in the background (we can't kill it), but the agent loop is unblocked.
func (a *Agent) safeExecute(t tool.Tool, ctx context.Context, args json.RawMessage) (result tool.Result, err error) {
	type execResult struct {
		result tool.Result
		err    error
	}
	ch := make(chan execResult, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				debug.Log("agent", "PANIC recovered in tool %s: %v\n%s", t.Name(), r, runtimedebug.Stack())
				ch <- execResult{tool.Result{
					Content: fmt.Sprintf("tool %s panicked: %v — this is a bug, please report it", t.Name(), r),
					IsError: true,
				}, nil}
			}
		}()
		r, e := t.Execute(ctx, args)
		ch <- execResult{r, e}
	}()

	select {
	case r := <-ch:
		return r.result, r.err
	case <-ctx.Done():
		debug.Log("agent", "tool %s cancelled via context (Execute did not honor cancellation, goroutine leaked)", t.Name())
		return tool.Result{
			Content: fmt.Sprintf("tool %s was cancelled (it did not respond to cancellation and may still be finishing in the background)", t.Name()),
			IsError: true,
		}, nil
	}
}

// executeFileTool handles edit_file and write_file with diff preview and checkpointing.
func (a *Agent) executeFileTool(ctx context.Context, t tool.Tool, tc provider.ToolCallDelta, env hooks.HookEnv) tool.Result {
	a.mu.Lock()
	cpMgr := a.checkpoints
	diffFn := a.diffConfirm
	a.mu.Unlock()

	// Determine file path and compute old/new content
	filePath, oldContent, newContent, err := a.computeFileChange(tc)
	if err != nil {
		return tool.Result{Content: fmt.Sprintf("file change error: %v", err), IsError: true}
	}

	// Show diff and ask for confirmation if diffConfirm is set
	if diffFn != nil && diff.HasChanges(oldContent, newContent) {
		diffText := diff.UnifiedDiff(oldContent, newContent, 3)
		if !diffFn(ctx, filePath, diffText) {
			return tool.Result{Content: fmt.Sprintf("File write to %s cancelled by user.", filePath), IsError: true}
		}
	}

	// Pre-tool-use hooks
	a.mu.RLock()
	hookCfg2 := a.hookConfig
	a.mu.RUnlock()
	preResult := hooks.RunPreHooks(hookCfg2.PreToolUse, env)
	if !preResult.Allowed {
		return tool.Result{Content: preResult.Output, IsError: true}
	}

	// Execute the actual tool (with panic recovery)
	fileStart := time.Now()
	result, err := a.safeExecute(t, ctx, tc.Arguments)
	fileDur := time.Since(fileStart)
	if err != nil {
		return tool.Result{Content: fmt.Sprintf("tool error: %v", err), IsError: true}
	}

	// Save checkpoint
	if cpMgr != nil && !result.IsError {
		cpMgr.Save(filePath, oldContent, newContent, tc.Name)
	}

	// Post-tool-use hooks
	postEnv := env
	postEnv.ToolSuccess = !result.IsError
	if result.IsError {
		postEnv.ToolError = truncateString(result.Content, 500)
	}
	postEnv.ToolResult = truncateString(result.Content, 4096)
	postEnv.ToolDuration = fileDur.String()
	postResult := hooks.RunPostHooks(hookCfg2.PostToolUse, postEnv)
	if postResult.Output != "" {
		result.Content += "\n" + postResult.Output
	}

	return result
}

// computeFileChange reads the old content and computes the new content for a file tool call.
func (a *Agent) computeFileChange(tc provider.ToolCallDelta) (filePath, oldContent, newContent string, err error) {
	switch tc.Name {
	case "edit_file":
		var args struct {
			FilePath string `json:"file_path"`
			OldText  string `json:"old_text"`
			NewText  string `json:"new_text"`
		}
		if err := json.Unmarshal(tc.Arguments, &args); err != nil {
			return "", "", "", fmt.Errorf("invalid arguments: %w", err)
		}
		filePath = args.FilePath
		data, err := os.ReadFile(filePath)
		if err != nil {
			// File may not exist yet — that's OK for write_file, but edit_file needs it
			return "", "", "", fmt.Errorf("cannot read file: %w", err)
		}
		oldContent = string(data)
		newContent = replaceFirst(oldContent, args.OldText, args.NewText)
		return filePath, oldContent, newContent, nil

	case "write_file":
		var args struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal(tc.Arguments, &args); err != nil {
			return "", "", "", fmt.Errorf("invalid arguments: %w", err)
		}
		filePath = args.Path
		data, err := os.ReadFile(filePath)
		if err != nil {
			oldContent = ""
		} else {
			oldContent = string(data)
		}
		newContent = args.Content
		return filePath, oldContent, newContent, nil

	default:
		return "", "", "", fmt.Errorf("not a file tool: %s", tc.Name)
	}
}

// replaceFirst replaces the first occurrence of old in s with new.
func replaceFirst(s, old, new string) string {
	return strings.Replace(s, old, new, 1)
}

func buildMultiFileDiffText(plans []tool.PlannedFileEdit) (string, bool) {
	var out strings.Builder
	hasChanges := false
	for _, plan := range plans {
		if !diff.HasChanges(plan.OldContent, plan.NewContent) {
			continue
		}
		if hasChanges {
			out.WriteString("\n")
		}
		out.WriteString("=== ")
		out.WriteString(plan.Path)
		out.WriteString(" ===\n")
		out.WriteString(diff.UnifiedDiff(plan.OldContent, plan.NewContent, 3))
		hasChanges = true
	}
	return out.String(), hasChanges
}

// indexOf returns the index of the first occurrence of substr in s, or -1.
// Delegates to strings.Index which uses optimized search algorithms.
func indexOf(s, substr string) int {
	return strings.Index(s, substr)
}

// toolWorkingDirMu is a safety-net mutex for syncToolWorkingDir. With Registry.Clone(),
// each agent has its own tool instances and this mutex should never be contended.
// It exists as a last resort in case a tool without a Clone() implementation is
// shared between agents and has a WorkingDir field that needs mutation.
var toolWorkingDirMu sync.Mutex

// syncToolWorkingDir uses reflection to set the WorkingDir field on tools
// that have one. This ensures tools always use the agent's current working
// directory, even after it changes (e.g., after enter_worktree).
//
// Note: With Registry.Clone(), each agent has independent tool instances, so
// this reflection is only mutating per-agent copies. The mutex is a safety net.
func syncToolWorkingDir(t tool.Tool, dir string) {
	toolWorkingDirMu.Lock()
	defer toolWorkingDirMu.Unlock()

	// Dereference pointer if needed
	v := reflect.ValueOf(t)
	if v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return
	}
	f := v.FieldByName("WorkingDir")
	if f.IsValid() && f.CanSet() && f.Kind() == reflect.String {
		f.SetString(dir)
	}
}

// truncateString truncates s to at most maxLen runes, appending "..." if truncated.
// Uses rune-based truncation to avoid breaking multi-byte UTF-8 characters.
func truncateString(s string, maxLen int) string {
	if maxLen < 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return string(runes[:maxLen])
	}
	return string(runes[:maxLen-3]) + "..."
}
