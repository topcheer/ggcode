package tool

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/topcheer/ggcode/internal/permission"
)

// TestNilManagerTaskTools verifies that all task tools return errors
// (not panics) when Manager is nil.
func TestNilManagerTaskTools(t *testing.T) {
	ctx := context.Background()

	t.Run("task_create_nil", func(t *testing.T) {
		tool := TaskCreateTool{Manager: nil}
		result, err := tool.Execute(ctx, json.RawMessage(`{"subject": "test"}`))
		assertNoPanic(t, "task_create", result, err)
	})

	t.Run("task_get_nil", func(t *testing.T) {
		tool := TaskGetTool{Manager: nil}
		result, err := tool.Execute(ctx, json.RawMessage(`{"taskId": "1"}`))
		assertNoPanic(t, "task_get", result, err)
	})

	t.Run("task_list_nil", func(t *testing.T) {
		tool := TaskListTool{Manager: nil}
		result, err := tool.Execute(ctx, json.RawMessage(`{}`))
		assertNoPanic(t, "task_list", result, err)
	})

	t.Run("task_update_nil", func(t *testing.T) {
		tool := TaskUpdateTool{Manager: nil}
		result, err := tool.Execute(ctx, json.RawMessage(`{"taskId": "1", "status": "completed"}`))
		assertNoPanic(t, "task_update", result, err)
	})

	t.Run("task_stop_nil", func(t *testing.T) {
		tool := TaskStopTool{Manager: nil}
		result, err := tool.Execute(ctx, json.RawMessage(`{"taskId": "1"}`))
		assertNoPanic(t, "task_stop", result, err)
	})
}

// TestNilSwitcherPlanModeTools verifies that plan mode tools return errors
// (not panics) when Switcher is nil.
func TestNilSwitcherPlanModeTools(t *testing.T) {
	ctx := context.Background()

	t.Run("enter_plan_mode_nil", func(t *testing.T) {
		tool := EnterPlanModeTool{Switcher: nil}
		result, err := tool.Execute(ctx, json.RawMessage(`{}`))
		assertNoPanic(t, "enter_plan_mode", result, err)
	})

	t.Run("exit_plan_mode_nil", func(t *testing.T) {
		tool := ExitPlanModeTool{Switcher: nil, DefaultMode: permission.SupervisedMode}
		result, err := tool.Execute(ctx, json.RawMessage(`{"plan": "test plan"}`))
		assertNoPanic(t, "exit_plan_mode", result, err)
	})
}

// TestNilSchedulerCronTools verifies that cron tools return errors
// (not panics) when Scheduler is nil.
func TestNilSchedulerCronTools(t *testing.T) {
	ctx := context.Background()

	t.Run("cron_create_nil", func(t *testing.T) {
		tool := CronCreateTool{Scheduler: nil}
		result, err := tool.Execute(ctx, json.RawMessage(`{"cron": "*/5 * * * *", "prompt": "test"}`))
		assertNoPanic(t, "cron_create", result, err)
	})

	t.Run("cron_delete_nil", func(t *testing.T) {
		tool := CronDeleteTool{Scheduler: nil}
		result, err := tool.Execute(ctx, json.RawMessage(`{"jobId": "j1"}`))
		assertNoPanic(t, "cron_delete", result, err)
	})

	t.Run("cron_list_nil", func(t *testing.T) {
		tool := CronListTool{Scheduler: nil}
		result, err := tool.Execute(ctx, json.RawMessage(`{}`))
		assertNoPanic(t, "cron_list", result, err)
	})
}

// assertNoPanic runs inside a testify-style helper: it verifies that the tool
// either returned an error Result or a Go error, but did NOT panic.
func assertNoPanic(t *testing.T, toolName string, result Result, err error) {
	t.Helper()
	if err != nil {
		t.Logf("Tool %q returned Go error (acceptable): %v", toolName, err)
		return
	}
	if !result.IsError {
		t.Errorf("Tool %q with nil dependency should return error result, got: %s", toolName, result.Content)
	} else {
		t.Logf("Tool %q correctly returned error: %s", toolName, result.Content)
	}
}
