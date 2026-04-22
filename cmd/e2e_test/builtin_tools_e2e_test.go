package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/cron"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/task"
	"github.com/topcheer/ggcode/internal/tool"
)

// noopModeSwitcher is a no-op ModeSwitcher for tests.
type noopModeSwitcher struct{}

func (n *noopModeSwitcher) SetMode(mode permission.PermissionMode) {}
func (n *noopModeSwitcher) RememberMode(currentMode permission.PermissionMode) permission.PermissionMode {
	return currentMode
}
func (n *noopModeSwitcher) RestoreMode(fallback permission.PermissionMode) permission.PermissionMode {
	return fallback
}

// e2eBuiltinToolRegistry creates a registry with file tools + task/plan/cron built-in tools.
func e2eBuiltinToolRegistry(t *testing.T, dir string) *tool.Registry {
	t.Helper()
	reg := tool.NewRegistry()
	sandbox := func(string) bool { return true }

	// File tools
	reg.Register(tool.ReadFile{SandboxCheck: sandbox})
	reg.Register(tool.WriteFile{SandboxCheck: sandbox})
	reg.Register(tool.ListDir{SandboxCheck: sandbox})
	reg.Register(tool.Glob{SandboxCheck: sandbox})
	reg.Register(tool.Grep{SandboxCheck: sandbox})
	reg.Register(tool.SearchFiles{SandboxCheck: sandbox})

	// Built-in tools with real dependencies
	taskMgr := task.NewManager()
	sched := cron.NewScheduler(nil)
	switcher := &noopModeSwitcher{}

	reg.Register(tool.TaskCreateTool{Manager: taskMgr})
	reg.Register(tool.TaskGetTool{Manager: taskMgr})
	reg.Register(tool.TaskListTool{Manager: taskMgr})
	reg.Register(tool.TaskUpdateTool{Manager: taskMgr})
	reg.Register(tool.TaskStopTool{Manager: taskMgr})
	reg.Register(tool.EnterPlanModeTool{Switcher: switcher})
	reg.Register(tool.ExitPlanModeTool{Switcher: switcher, DefaultMode: permission.SupervisedMode})
	reg.Register(tool.CronCreateTool{Scheduler: sched})
	reg.Register(tool.CronDeleteTool{Scheduler: sched})
	reg.Register(tool.CronListTool{Scheduler: sched})

	return reg
}

// e2eBuiltinToolLoop sends a prompt to the LLM, executes tool calls, returns final text.
// Max 5 tool rounds to prevent infinite loops.
func e2eBuiltinToolLoop(ctx context.Context, t *testing.T, prov provider.Provider, reg *tool.Registry, userPrompt string) string {
	t.Helper()
	messages := []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{provider.TextBlock(userPrompt)}},
	}

	defs := reg.ToDefinitions()

	for round := 0; round < 5; round++ {
		resp, err := prov.Chat(ctx, messages, defs)
		if err != nil {
			t.Fatalf("LLM chat round %d: %v", round, err)
		}

		var toolCalls []provider.ContentBlock
		var textParts []string
		for _, block := range resp.Message.Content {
			switch block.Type {
			case "text":
				textParts = append(textParts, block.Text)
			case "tool_use":
				toolCalls = append(toolCalls, block)
			}
		}

		if len(toolCalls) == 0 {
			return strings.Join(textParts, "")
		}

		messages = append(messages, resp.Message)

		for _, tc := range toolCalls {
			tl, ok := reg.Get(tc.ToolName)
			if !ok {
				messages = append(messages, provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{
						provider.ToolResultBlock(tc.ToolID, fmt.Sprintf("unknown tool: %s", tc.ToolName), true),
					},
				})
				continue
			}

			result, err := tl.Execute(ctx, tc.Input)
			if err != nil {
				messages = append(messages, provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{
						provider.ToolResultBlock(tc.ToolID, fmt.Sprintf("execution error: %v", err), true),
					},
				})
				continue
			}

			messages = append(messages, provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{
					provider.ToolResultBlock(tc.ToolID, result.Content, result.IsError),
				},
			})
		}
	}

	return "max tool rounds reached"
}

// TestE2E_TaskCreateViaLLM verifies task tools don't crash through the LLM pipeline.
func TestE2E_TaskCreateViaLLM(t *testing.T) {
	skipE2E(t)
	prov := newProviderFromEnv(t)
	dir := t.TempDir()
	reg := e2eBuiltinToolRegistry(t, dir)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	prompt := "Use the task_create tool to create a task with subject 'Write unit tests'. " +
		"Then use task_list to list all tasks. Report what you see."

	result := e2eBuiltinToolLoop(ctx, t, prov, reg, prompt)
	t.Logf("LLM response: %s", result)
}

// TestE2E_TaskCreateAndUpdateViaLLM tests the full task lifecycle through the LLM.
func TestE2E_TaskCreateAndUpdateViaLLM(t *testing.T) {
	skipE2E(t)
	prov := newProviderFromEnv(t)
	dir := t.TempDir()
	reg := e2eBuiltinToolRegistry(t, dir)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	prompt := "Do these steps:\n" +
		"1. task_create with subject 'Fix bug #123'\n" +
		"2. task_update that task to 'in_progress'\n" +
		"3. task_update to 'completed'\n" +
		"4. task_list to verify. Report results."

	result := e2eBuiltinToolLoop(ctx, t, prov, reg, prompt)
	t.Logf("LLM response: %s", result)
}

// TestE2E_PlanModeViaLLM tests enter/exit plan mode through the LLM.
func TestE2E_PlanModeViaLLM(t *testing.T) {
	skipE2E(t)
	prov := newProviderFromEnv(t)
	dir := t.TempDir()
	reg := e2eBuiltinToolRegistry(t, dir)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	prompt := "Use enter_plan_mode. Then exit_plan_mode with plan 'Step 1: Design. Step 2: Implement.' Report what happened."
	result := e2eBuiltinToolLoop(ctx, t, prov, reg, prompt)
	t.Logf("LLM response: %s", result)
}

// TestE2E_CronCreateViaLLM tests cron tools through the LLM.
func TestE2E_CronCreateViaLLM(t *testing.T) {
	skipE2E(t)
	prov := newProviderFromEnv(t)
	dir := t.TempDir()
	reg := e2eBuiltinToolRegistry(t, dir)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	prompt := "Use cron_create with cron='30 14 25 12 *', prompt='Test reminder', recurring=false. Then cron_list. Report results."
	result := e2eBuiltinToolLoop(ctx, t, prov, reg, prompt)
	t.Logf("LLM response: %s", result)
}

// TestE2E_AgentToolExecutionNoPanic runs built-in tools through the full agent streaming pipeline.
func TestE2E_AgentToolExecutionNoPanic(t *testing.T) {
	skipE2E(t)
	prov := newProviderFromEnv(t)
	dir := t.TempDir()
	reg := e2eBuiltinToolRegistry(t, dir)

	a := agent.NewAgent(prov, reg, "You are a helpful assistant. Use the provided tools.", 10)
	a.SetPermissionPolicy(permission.NewConfigPolicyWithMode(nil, []string{"."}, permission.BypassMode))

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	var events []provider.StreamEvent
	err := a.RunStreamWithContent(ctx, []provider.ContentBlock{provider.TextBlock("Create a task called 'E2E test' and list all tasks.")}, func(ev provider.StreamEvent) {
		events = append(events, ev)
	})
	if err != nil {
		t.Fatalf("RunStreamWithContent error: %v", err)
	}

	for _, ev := range events {
		if ev.Type == provider.StreamEventToolResult {
			t.Logf("Tool result: tool=%s error=%v", ev.Tool.Name, ev.IsError)
		}
	}
}

// TestE2E_NilManagerViaAgent verifies nil-dependency tools don't crash the agent
// when called through the full streaming pipeline with a real LLM.
func TestE2E_NilManagerViaAgent(t *testing.T) {
	skipE2E(t)
	prov := newProviderFromEnv(t)

	reg := tool.NewRegistry()
	// Register tools with nil dependencies — worst case crash scenario
	reg.Register(tool.TaskCreateTool{Manager: nil})
	reg.Register(tool.TaskListTool{Manager: nil})
	reg.Register(tool.EnterPlanModeTool{Switcher: nil})

	a := agent.NewAgent(prov, reg, "Use the provided tools when asked.", 5)
	a.SetPermissionPolicy(permission.NewConfigPolicyWithMode(nil, []string{"."}, permission.BypassMode))

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var panicked bool
	func() {
		defer func() {
			if r := recover(); r != nil {
				panicked = true
				t.Errorf("AGENT PANICKED: %v", r)
			}
		}()

		err := a.RunStreamWithContent(ctx, []provider.ContentBlock{provider.TextBlock("Create a task called 'crash test' and list all tasks.")}, func(ev provider.StreamEvent) {
			if ev.Type == provider.StreamEventError {
				t.Logf("Agent error: %v", ev.Error)
			}
		})
		if err != nil {
			t.Logf("RunStreamWithContent error (acceptable): %v", err)
		}
	}()

	if panicked {
		t.Fatal("Agent should NOT panic with nil-dependency tools")
	}
}

// TestE2E_NilManagerDirectCalls verifies each tool with nil deps doesn't panic (direct calls).
func TestE2E_NilManagerDirectCalls(t *testing.T) {
	skipE2E(t)

	reg := tool.NewRegistry()
	reg.Register(tool.TaskCreateTool{Manager: nil})
	reg.Register(tool.TaskListTool{Manager: nil})
	reg.Register(tool.TaskGetTool{Manager: nil})
	reg.Register(tool.TaskUpdateTool{Manager: nil})
	reg.Register(tool.TaskStopTool{Manager: nil})
	reg.Register(tool.EnterPlanModeTool{Switcher: nil})
	reg.Register(tool.ExitPlanModeTool{Switcher: nil, DefaultMode: permission.SupervisedMode})
	reg.Register(tool.CronCreateTool{Scheduler: nil})
	reg.Register(tool.CronDeleteTool{Scheduler: nil})
	reg.Register(tool.CronListTool{Scheduler: nil})

	ctx := context.Background()
	tests := []struct {
		name  string
		input string
	}{
		{"task_create", `{"subject": "test"}`},
		{"task_list", `{}`},
		{"task_get", `{"taskId": "1"}`},
		{"task_update", `{"taskId": "1", "status": "completed"}`},
		{"task_stop", `{"taskId": "1"}`},
		{"enter_plan_mode", `{}`},
		{"exit_plan_mode", `{"plan": "test plan"}`},
		{"cron_create", `{"cron": "*/5 * * * *", "prompt": "test"}`},
		{"cron_delete", `{"jobId": "j1"}`},
		{"cron_list", `{}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tl, ok := reg.Get(tt.name)
			if !ok {
				t.Fatalf("tool %q not found", tt.name)
			}
			result, err := tl.Execute(ctx, json.RawMessage(tt.input))
			if err != nil {
				t.Logf("Returned Go error (acceptable): %v", err)
				return
			}
			if !result.IsError {
				t.Errorf("Should return error for nil dependency, got: %s", result.Content)
			} else {
				t.Logf("Correctly returned error: %s", result.Content)
			}
		})
	}
}

// e2eBuiltinProvider creates a provider using ggcode's official config (from env).
// Uses the same env vars as swarm_e2e_test.go.
func e2eBuiltinProvider(t *testing.T) provider.Provider {
	t.Helper()
	apiKey := os.Getenv("GGCODE_E2E_API_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("ZAI_API_KEY")
	}
	if apiKey == "" {
		t.Skip("GGCODE_E2E_API_KEY or ZAI_API_KEY required")
	}
	baseURL := os.Getenv("GGCODE_E2E_BASE_URL")
	if baseURL == "" {
		baseURL = "https://open.bigmodel.cn/api/anthropic"
	}
	model := os.Getenv("GGCODE_E2E_MODEL")
	if model == "" {
		model = "glm-5-turbo"
	}
	prov, err := provider.NewProvider(&config.ResolvedEndpoint{
		VendorID:   "anthropic",
		VendorName: "anthropic",
		Protocol:   "anthropic",
		BaseURL:    baseURL,
		APIKey:     apiKey,
		Model:      model,
		MaxTokens:  1024,
	})
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}
	return prov
}
