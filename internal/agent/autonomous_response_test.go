package agent

import (
	"context"
	"testing"
	"time"
)

func TestNewAutonomousResponse(t *testing.T) {
	ar := NewAutonomousResponse(nil)

	if ar == nil {
		t.Fatal("NewAutonomousResponse returned nil")
	}

	if !ar.IsEnabled() {
		t.Error("NewAutonomousResponse should be enabled by default")
	}

	stats := ar.GetSessionStats()
	if stats.MaxActionsPerSession != 10 {
		t.Errorf("Expected MaxActionsPerSession=10, got %d", stats.MaxActionsPerSession)
	}
}

func TestAutonomousResponse_EnableDisable(t *testing.T) {
	ar := NewAutonomousResponse(nil)

	ar.Disable()
	if ar.IsEnabled() {
		t.Error("Should be disabled after Disable()")
	}

	ar.Enable()
	if !ar.IsEnabled() {
		t.Error("Should be enabled after Enable()")
	}
}

func TestAutonomousResponse_RegisterAction(t *testing.T) {
	ar := NewAutonomousResponse(nil)

	action := &AutonomousAction{
		ID:          "test_action",
		Description: "Test action for autonomous response",
		Severity:    SeveritySafe,
		CanApply: func(ctx context.Context, d DetectionEvent) bool {
			return d.Type == "test_detection"
		},
		Execute: func(ctx context.Context) error {
			return nil
		},
	}

	ar.RegisterAction(action)

	// Verify action can be triggered
	detection := DetectionEvent{
		ID:        "test-1",
		Type:      "test_detection",
		Severity:  "warning",
		Timestamp: time.Now(),
	}

	execution, err := ar.HandleDetection(context.Background(), detection)
	if err != nil {
		t.Errorf("HandleDetection failed: %v", err)
	}

	if execution == nil {
		t.Fatal("HandleDetection returned nil execution")
	}

	if execution.Status != "executed" {
		t.Errorf("Expected status 'executed', got '%s'", execution.Status)
	}

	if execution.ActionID != "test_action" {
		t.Errorf("Expected ActionID 'test_action', got '%s'", execution.ActionID)
	}
}

func TestAutonomousResponse_HighSeverityRequiresApproval(t *testing.T) {
	ar := NewAutonomousResponse(nil)

	// Register a HIGH severity action
	action := &AutonomousAction{
		ID:          "high_risk_action",
		Description: "High risk action requiring approval",
		Severity:    SeverityHigh,
		CanApply: func(ctx context.Context, d DetectionEvent) bool {
			return true
		},
		Execute: func(ctx context.Context) error {
			return nil
		},
		Rollback: func(ctx context.Context) error {
			return nil
		},
	}

	ar.RegisterAction(action)

	detection := DetectionEvent{
		ID:        "test-2",
		Type:      "critical_issue",
		Severity:  "critical",
		Timestamp: time.Now(),
	}

	execution, err := ar.HandleDetection(context.Background(), detection)
	if err != nil {
		t.Errorf("HandleDetection failed: %v", err)
	}

	if execution == nil {
		t.Fatal("HandleDetection returned nil execution")
	}

	if execution.Status != "awaiting_approval" {
		t.Errorf("Expected status 'awaiting_approval', got '%s'", execution.Status)
	}

	// Verify it's in pending approvals
	pending := ar.GetPendingApprovals()
	if len(pending) != 1 {
		t.Errorf("Expected 1 pending approval, got %d", len(pending))
	}

	if pending[0].Action.ID != "high_risk_action" {
		t.Errorf("Expected pending action 'high_risk_action', got '%s'", pending[0].Action.ID)
	}
}

func TestAutonomousResponse_ApproveAction(t *testing.T) {
	ar := NewAutonomousResponse(nil)

	action := &AutonomousAction{
		ID:          "approveable_action",
		Description: "Action requiring approval",
		Severity:    SeverityMedium,
		CanApply: func(ctx context.Context, d DetectionEvent) bool {
			return true
		},
		Execute: func(ctx context.Context) error {
			return nil
		},
		Rollback: func(ctx context.Context) error {
			return nil
		},
	}

	ar.RegisterAction(action)

	detection := DetectionEvent{
		ID:        "test-3",
		Type:      "medium_issue",
		Severity:  "warning",
		Timestamp: time.Now(),
	}

	// First, create pending approval
	_, _ = ar.HandleDetection(context.Background(), detection)

	// Approve the action
	execution, err := ar.ApproveAction(context.Background(), "approveable_action")
	if err != nil {
		t.Errorf("ApproveAction failed: %v", err)
	}

	if execution.Status != "executed" {
		t.Errorf("Expected status 'executed', got '%s'", execution.Status)
	}

	if !execution.ManualOverride {
		t.Error("Expected ManualOverride to be true for approved action")
	}

	// Verify no pending approvals remain
	pending := ar.GetPendingApprovals()
	if len(pending) != 0 {
		t.Errorf("Expected 0 pending approvals after approval, got %d", len(pending))
	}
}

func TestAutonomousResponse_RejectAction(t *testing.T) {
	ar := NewAutonomousResponse(nil)

	action := &AutonomousAction{
		ID:          "rejectable_action",
		Description: "Action to be rejected",
		Severity:    SeverityMedium,
		CanApply: func(ctx context.Context, d DetectionEvent) bool {
			return true
		},
		Execute: func(ctx context.Context) error {
			return nil
		},
		Rollback: func(ctx context.Context) error {
			return nil
		},
	}

	ar.RegisterAction(action)

	detection := DetectionEvent{
		ID:        "test-4",
		Type:      "rejectable_issue",
		Severity:  "warning",
		Timestamp: time.Now(),
	}

	// Create pending approval
	_, _ = ar.HandleDetection(context.Background(), detection)

	// Reject the action
	err := ar.RejectAction("rejectable_action")
	if err != nil {
		t.Errorf("RejectAction failed: %v", err)
	}

	// Verify it's in history as rejected
	history := ar.GetHistory()
	if len(history) == 0 {
		t.Fatal("Expected non-empty history after rejection")
	}

	if history[len(history)-1].Status != "rejected" {
		t.Errorf("Expected last history entry status 'rejected', got '%s'", history[len(history)-1].Status)
	}

	// Verify no pending approvals remain
	pending := ar.GetPendingApprovals()
	if len(pending) != 0 {
		t.Errorf("Expected 0 pending approvals after rejection, got %d", len(pending))
	}
}

func TestAutonomousResponse_ExecuteFailure(t *testing.T) {
	ar := NewAutonomousResponse(nil)

	executeCalled := false
	action := &AutonomousAction{
		ID:          "failing_action",
		Description: "Action that fails",
		Severity:    SeverityLow,
		CanApply: func(ctx context.Context, d DetectionEvent) bool {
			return true
		},
		Execute: func(ctx context.Context) error {
			executeCalled = true
			return context.DeadlineExceeded
		},
		Rollback: func(ctx context.Context) error {
			return nil
		},
	}

	ar.RegisterAction(action)

	detection := DetectionEvent{
		ID:        "test-5",
		Type:      "failure_test",
		Severity:  "error",
		Timestamp: time.Now(),
	}

	execution, err := ar.HandleDetection(context.Background(), detection)
	if err == nil {
		t.Error("Expected error from HandleDetection, got nil")
	}

	if !executeCalled {
		t.Error("Execute should have been called")
	}

	if execution.Status != "failed" {
		t.Errorf("Expected status 'failed', got '%s'", execution.Status)
	}
}

func TestAutonomousResponse_DisabledIgnoresDetections(t *testing.T) {
	ar := NewAutonomousResponse(nil)
	ar.Disable()

	action := &AutonomousAction{
		ID:          "ignored_action",
		Description: "Action that should be ignored",
		Severity:    SeveritySafe,
		CanApply: func(ctx context.Context, d DetectionEvent) bool {
			return true
		},
		Execute: func(ctx context.Context) error {
			return nil
		},
	}

	ar.RegisterAction(action)

	detection := DetectionEvent{
		ID:        "test-6",
		Type:      "ignored_detection",
		Severity:  "warning",
		Timestamp: time.Now(),
	}

	execution, err := ar.HandleDetection(context.Background(), detection)
	if err != nil {
		t.Errorf("HandleDetection failed: %v", err)
	}

	if execution != nil {
		t.Error("Expected nil execution when disabled")
	}
}

func TestAutonomousResponse_Cooldown(t *testing.T) {
	ar := NewAutonomousResponse(&Config{
		MaxActionsPerSession:   10,
		MinConfidenceThreshold: 0.8,
		ActionCooldown:         100 * time.Millisecond,
	})

	action := &AutonomousAction{
		ID:          "cooldown_action",
		Description: "Action with cooldown",
		Severity:    SeveritySafe,
		CanApply: func(ctx context.Context, d DetectionEvent) bool {
			return true
		},
		Execute: func(ctx context.Context) error {
			return nil
		},
	}

	ar.RegisterAction(action)

	detection := DetectionEvent{
		ID:        "test-7",
		Type:      "cooldown_test",
		Severity:  "warning",
		Timestamp: time.Now(),
	}

	// First execution should succeed
	execution1, _ := ar.HandleDetection(context.Background(), detection)
	if execution1.Status != "executed" {
		t.Errorf("First execution should succeed, got status '%s'", execution1.Status)
	}

	// Immediate second execution should be blocked by cooldown
	execution2, _ := ar.HandleDetection(context.Background(), detection)
	if execution2 != nil {
		t.Error("Second execution should be blocked by cooldown")
	}

	// Wait for cooldown to expire
	time.Sleep(150 * time.Millisecond)

	// Third execution after cooldown should succeed
	execution3, _ := ar.HandleDetection(context.Background(), detection)
	if execution3 == nil || execution3.Status != "executed" {
		t.Error("Third execution after cooldown should succeed")
	}
}

func TestAutonomousResponse_GetSessionStats(t *testing.T) {
	ar := NewAutonomousResponse(nil)

	// Register and execute some actions
	ar.RegisterAction(&AutonomousAction{
		ID:          "stat_action_1",
		Description: "Action for stats",
		Severity:    SeveritySafe,
		CanApply: func(ctx context.Context, d DetectionEvent) bool {
			return d.Type == "stats_test_exec"
		},
		Execute: func(ctx context.Context) error { return nil },
	})

	ar.RegisterAction(&AutonomousAction{
		ID:          "stat_action_2",
		Description: "Action requiring approval",
		Severity:    SeverityMedium,
		CanApply: func(ctx context.Context, d DetectionEvent) bool {
			return d.Type == "stats_test_pending"
		},
		Execute:  func(ctx context.Context) error { return nil },
		Rollback: func(ctx context.Context) error { return nil },
	})

	detection1 := DetectionEvent{
		ID:        "test-8a",
		Type:      "stats_test_exec",
		Severity:  "warning",
		Timestamp: time.Now(),
	}

	detection2 := DetectionEvent{
		ID:        "test-8b",
		Type:      "stats_test_pending",
		Severity:  "warning",
		Timestamp: time.Now(),
	}

	// Execute one action (SAFE)
	_, err1 := ar.HandleDetection(context.Background(), detection1)
	if err1 != nil {
		t.Errorf("First HandleDetection failed: %v", err1)
	}

	// Create one pending approval (MEDIUM)
	_, err2 := ar.HandleDetection(context.Background(), detection2)
	if err2 != nil {
		t.Errorf("Second HandleDetection failed: %v", err2)
	}

	stats := ar.GetSessionStats()

	if stats.TotalExecuted != 1 {
		t.Errorf("Expected TotalExecuted=1, got %d", stats.TotalExecuted)
	}

	if stats.AwaitingApproval != 1 {
		t.Errorf("Expected AwaitingApproval=1, got %d", stats.AwaitingApproval)
	}

	if !stats.Enabled {
		t.Error("Expected Enabled=true")
	}
}

func TestAutonomousResponse_GetHistory(t *testing.T) {
	ar := NewAutonomousResponse(nil)

	action := &AutonomousAction{
		ID:          "history_action",
		Description: "Action for history test",
		Severity:    SeveritySafe,
		CanApply:    func(ctx context.Context, d DetectionEvent) bool { return true },
		Execute:     func(ctx context.Context) error { return nil },
	}

	ar.RegisterAction(action)

	detection := DetectionEvent{
		ID:        "test-9",
		Type:      "history_test",
		Severity:  "warning",
		Timestamp: time.Now(),
	}

	ar.HandleDetection(context.Background(), detection)

	history := ar.GetHistory()

	if len(history) != 1 {
		t.Fatalf("Expected 1 history entry, got %d", len(history))
	}

	if history[0].ActionID != "history_action" {
		t.Errorf("Expected ActionID 'history_action', got '%s'", history[0].ActionID)
	}

	if history[0].Status != "executed" {
		t.Errorf("Expected status 'executed', got '%s'", history[0].Status)
	}
}

func TestActionSeverity_String(t *testing.T) {
	tests := []struct {
		severity ActionSeverity
		expected string
	}{
		{SeveritySafe, "SAFE"},
		{SeverityLow, "LOW"},
		{SeverityMedium, "MEDIUM"},
		{SeverityHigh, "HIGH"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.severity.String(); got != tt.expected {
				t.Errorf("ActionSeverity.String() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestActionSeverity_RequiresApproval(t *testing.T) {
	tests := []struct {
		severity         ActionSeverity
		requiresApproval bool
	}{
		{SeveritySafe, false},
		{SeverityLow, false},
		{SeverityMedium, true},
		{SeverityHigh, true},
	}

	for _, tt := range tests {
		t.Run(tt.severity.String(), func(t *testing.T) {
			if got := tt.severity.RequiresApproval(); got != tt.requiresApproval {
				t.Errorf("ActionSeverity.RequiresApproval() = %v, want %v", got, tt.requiresApproval)
			}
		})
	}
}

func TestAutonomousResponse_RegisterStandardActions(t *testing.T) {
	ar := NewAutonomousResponse(nil)
	ar.RegisterStandardActions()

	// Verify standard actions are registered by triggering their detection types
	detections := []DetectionEvent{
		{ID: "1", Type: "context_budget_warning", Severity: "warning", Timestamp: time.Now()},
		{ID: "2", Type: "tool_failure", Severity: "error", Timestamp: time.Now()},
		{ID: "3", Type: "cost_spike", Severity: "warning", Timestamp: time.Now()},
	}

	for _, det := range detections {
		execution, err := ar.HandleDetection(context.Background(), det)
		if err != nil {
			t.Errorf("HandleDetection failed for type %s: %v", det.Type, err)
		}
		if execution == nil {
			t.Errorf("Expected execution for detection type %s", det.Type)
		}
	}
}
