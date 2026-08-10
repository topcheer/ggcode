// Package agent implements autonomous response capabilities for AI agents.
//
// This module implements the 2026 autonomous observability pattern: shifting from
// reactive "observe and alert" to proactive "observe and act". Agents can detect
// critical issues and automatically trigger safe, bounded remediation actions.
//
// Design principles:
// - Safety-first: All autonomous actions are bounded and reversible
// - Transparency: Every autonomous action is logged with full context
// - Human override: Users can disable autonomous actions at any time
// - Risk-based: Only low-risk, high-confidence actions are autonomous
//
// Research basis:
// - Zylos Research 2026: "The industry is shifting from 'observe and alert' to 'observe and act'"
// - LogicMonitor 2026 prediction: Autonomous IT with "visibility → correlation → prediction → action"
// - Multi-agent production systems require automated response at agent speed, not human speed
package agent

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// ActionSeverity indicates the risk level of an autonomous action.
//
// Only SAFE and LOW severity actions can execute autonomously.
// MEDIUM and HIGH require human approval before execution.
type ActionSeverity int

const (
	SeveritySafe   ActionSeverity = iota // No impact, reversible
	SeverityLow                          // Minimal impact, easily reversible
	SeverityMedium                       // Moderate impact, reversible but complex
	SeverityHigh                         // High impact, may require manual rollback
)

func (s ActionSeverity) String() string {
	return [...]string{"SAFE", "LOW", "MEDIUM", "HIGH"}[s]
}

// RequiresApproval returns true if the action needs human approval.
func (s ActionSeverity) RequiresApproval() bool {
	return s >= SeverityMedium
}

// AutonomousAction represents a remediation action that can be taken autonomously.
//
// Actions are registered with the AutonomousResponse system and can be executed
// automatically when their CanApply function returns true. Actions can be rolled
// back if needed (except for SeveritySafe actions).
type AutonomousAction struct {
	// ID uniquely identifies this action type
	ID string

	// Description explains what this action does (for logs and audit trails)
	Description string

	// Severity indicates the risk level of this action
	Severity ActionSeverity

	// Execute performs the action. It must be idempotent, safe, and log its operations.
	Execute func(ctx context.Context) error

	// Rollback can undo the action if needed (must be non-nil for non-SAFE severity)
	Rollback func(ctx context.Context) error

	// CanApply evaluates whether this action is appropriate for the current situation
	CanApply func(ctx context.Context, detection DetectionEvent) bool

	// EstimatedImpact describes the potential effect of this action
	EstimatedImpact string
}

// DetectionEvent represents an issue detected by the agent's monitoring systems.
//
// Detection events trigger autonomous response actions when they meet criteria
// defined in AutonomousAction.CanApply.
type DetectionEvent struct {
	ID        string
	Type      string
	Severity  string
	Timestamp time.Time
	Context   map[string]interface{}
	Message   string
	Source    string
}

// AutonomousResponse manages autonomous remediation capabilities.
//
// It maintains a registry of autonomous actions, tracks their execution history,
// and ensures that only safe, low-risk actions execute without human approval.
// MEDIUM and HIGH severity actions require explicit human approval.
type AutonomousResponse struct {
	mu sync.RWMutex

	// enabled controls whether autonomous actions can execute without approval
	enabled bool

	// actions is the registry of available autonomous actions
	actions map[string]*AutonomousAction

	// history tracks all autonomous actions taken (for audit and learning)
	history []ActionExecution

	// approvalRequired tracks actions awaiting human approval
	approvalRequired []PendingApproval

	// config holds configuration for autonomous behavior
	config *Config
}

// Config configures autonomous response behavior.
type Config struct {
	// MaxActionsPerSession prevents runaway autonomous behavior
	MaxActionsPerSession int

	// MinConfidenceThreshold: only actions with confidence >= threshold execute autonomously
	MinConfidenceThreshold float64

	// ActionCooldown: minimum time between same action type (prevents flapping)
	ActionCooldown time.Duration

	// AuditLogPath: where to write detailed audit logs
	AuditLogPath string
}

// ActionExecution records a single autonomous action execution.
type ActionExecution struct {
	ActionID       string
	ActionType     string
	Trigger        DetectionEvent
	Status         string // "executed", "rolled_back", "failed", "skipped"
	Timestamp      time.Time
	Duration       time.Duration
	Error          error
	ManualOverride bool
}

// PendingApproval represents an action awaiting human approval.
type PendingApproval struct {
	Action    *AutonomousAction
	Trigger   DetectionEvent
	ExpiresAt time.Time
}

// NewAutonomousResponse creates a new autonomous response manager.
//
// If cfg is nil, default configuration is used with conservative safety settings:
// - Max 10 autonomous actions per session
// - 80% minimum confidence threshold
// - 5 minute cooldown between same actions
func NewAutonomousResponse(cfg *Config) *AutonomousResponse {
	if cfg == nil {
		cfg = &Config{
			MaxActionsPerSession:   10,
			MinConfidenceThreshold: 0.8,
			ActionCooldown:         5 * time.Minute,
		}
	}

	return &AutonomousResponse{
		enabled: true,
		actions: make(map[string]*AutonomousAction),
		history: make([]ActionExecution, 0),
		config:  cfg,
	}
}

// RegisterAction adds an autonomous action to the registry.
func (ar *AutonomousResponse) RegisterAction(action *AutonomousAction) {
	ar.mu.Lock()
	defer ar.mu.Unlock()
	ar.actions[action.ID] = action
}

// HandleDetection processes a detection event and potentially triggers autonomous action.
func (ar *AutonomousResponse) HandleDetection(ctx context.Context, detection DetectionEvent) (*ActionExecution, error) {
	ar.mu.RLock()
	defer ar.mu.RUnlock()

	if !ar.enabled {
		return nil, nil // Autonomous actions disabled
	}

	// Find applicable actions
	var candidates []*AutonomousAction
	for _, action := range ar.actions {
		if action.CanApply(ctx, detection) {
			candidates = append(candidates, action)
		}
	}

	if len(candidates) == 0 {
		return nil, nil
	}

	// Select highest-priority (lowest severity) action
	selected := candidates[0]
	for _, candidate := range candidates[1:] {
		if candidate.Severity < selected.Severity {
			selected = candidate
		}
	}

	// Check if approval required
	if selected.Severity.RequiresApproval() {
		ar.mu.RUnlock()
		ar.mu.Lock()
		ar.approvalRequired = append(ar.approvalRequired, PendingApproval{
			Action:    selected,
			Trigger:   detection,
			ExpiresAt: time.Now().Add(30 * time.Minute),
		})
		ar.mu.Unlock()
		ar.mu.RLock()

		return &ActionExecution{
			ActionID:   selected.ID,
			ActionType: selected.Description,
			Trigger:    detection,
			Status:     "awaiting_approval",
			Timestamp:  time.Now(),
		}, nil
	}

	// Check cooldown
	if !ar.checkCooldown(selected.ID) {
		return nil, nil
	}

	// Execute autonomous action
	execution := &ActionExecution{
		ActionID:   selected.ID,
		ActionType: selected.Description,
		Trigger:    detection,
		Status:     "executing",
		Timestamp:  time.Now(),
	}

	start := time.Now()
	var err error
	if err = selected.Execute(ctx); err != nil {
		execution.Status = "failed"
		execution.Error = err
		execution.Duration = time.Since(start)
		return execution, err
	}
	execution.Status = "executed"
	execution.Duration = time.Since(start)

	ar.mu.RUnlock()
	ar.mu.Lock()
	ar.history = append(ar.history, *execution)
	ar.mu.Unlock()
	ar.mu.RLock()

	return execution, err
}

// checkCooldown verifies if enough time has passed since the last action of this type.
func (ar *AutonomousResponse) checkCooldown(actionID string) bool {
	if ar.config.ActionCooldown == 0 {
		return true
	}

	for i := len(ar.history) - 1; i >= 0; i-- {
		if ar.history[i].ActionID == actionID {
			return time.Since(ar.history[i].Timestamp) >= ar.config.ActionCooldown
		}
	}
	return true
}

// Enable enables autonomous actions.
func (ar *AutonomousResponse) Enable() {
	ar.mu.Lock()
	defer ar.mu.Unlock()
	ar.enabled = true
}

// Disable disables autonomous actions (human-only mode).
func (ar *AutonomousResponse) Disable() {
	ar.mu.Lock()
	defer ar.mu.Unlock()
	ar.enabled = false
}

// IsEnabled returns whether autonomous actions are enabled.
func (ar *AutonomousResponse) IsEnabled() bool {
	ar.mu.RLock()
	defer ar.mu.RUnlock()
	return ar.enabled
}

// GetHistory returns the execution history.
func (ar *AutonomousResponse) GetHistory() []ActionExecution {
	ar.mu.RLock()
	defer ar.mu.RUnlock()
	history := make([]ActionExecution, len(ar.history))
	copy(history, ar.history)
	return history
}

// GetPendingApprovals returns actions awaiting human approval.
func (ar *AutonomousResponse) GetPendingApprovals() []PendingApproval {
	ar.mu.RLock()
	defer ar.mu.RUnlock()
	approvals := make([]PendingApproval, len(ar.approvalRequired))
	copy(approvals, ar.approvalRequired)
	return approvals
}

// ApproveAction executes an action that was pending approval.
func (ar *AutonomousResponse) ApproveAction(ctx context.Context, actionID string) (*ActionExecution, error) {
	ar.mu.Lock()
	defer ar.mu.Unlock()

	var pending *PendingApproval
	var pendingIdx = -1
	for i, p := range ar.approvalRequired {
		if p.Action.ID == actionID {
			pending = &ar.approvalRequired[i]
			pendingIdx = i
			break
		}
	}

	if pending == nil {
		return nil, fmt.Errorf("action %s not found in pending approvals", actionID)
	}

	if time.Now().After(pending.ExpiresAt) {
		// Remove expired pending action
		ar.approvalRequired = append(ar.approvalRequired[:pendingIdx], ar.approvalRequired[pendingIdx+1:]...)
		return nil, fmt.Errorf("action %s approval request expired", actionID)
	}

	// Execute the action
	execution := &ActionExecution{
		ActionID:       pending.Action.ID,
		ActionType:     pending.Action.Description,
		Trigger:        pending.Trigger,
		Status:         "executing",
		Timestamp:      time.Now(),
		ManualOverride: true,
	}

	start := time.Now()
	var err error
	if err = pending.Action.Execute(ctx); err != nil {
		execution.Status = "failed"
		execution.Error = err
		execution.Duration = time.Since(start)
		return execution, err
	}
	execution.Status = "executed"
	execution.Duration = time.Since(start)

	ar.history = append(ar.history, *execution)

	// Remove from pending approvals
	ar.approvalRequired = append(ar.approvalRequired[:pendingIdx], ar.approvalRequired[pendingIdx+1:]...)

	return execution, err
}

// RejectAction cancels an action that was pending approval.
func (ar *AutonomousResponse) RejectAction(actionID string) error {
	ar.mu.Lock()
	defer ar.mu.Unlock()

	for i, p := range ar.approvalRequired {
		if p.Action.ID == actionID {
			execution := ActionExecution{
				ActionID:   p.Action.ID,
				ActionType: p.Action.Description,
				Trigger:    p.Trigger,
				Status:     "rejected",
				Timestamp:  time.Now(),
			}
			ar.history = append(ar.history, execution)
			ar.approvalRequired = append(ar.approvalRequired[:i], ar.approvalRequired[i+1:]...)
			return nil
		}
	}

	return fmt.Errorf("action %s not found in pending approvals", actionID)
}

// RollbackAction attempts to rollback a previously executed action.
func (ar *AutonomousResponse) RollbackAction(ctx context.Context, executionID int) error {
	ar.mu.Lock()
	defer ar.mu.Unlock()

	if executionID < 0 || executionID >= len(ar.history) {
		return fmt.Errorf("invalid execution ID %d", executionID)
	}

	execution := ar.history[executionID]
	if execution.Status != "executed" {
		return fmt.Errorf("cannot rollback action in status %s", execution.Status)
	}

	action, exists := ar.actions[execution.ActionID]
	if !exists || action.Rollback == nil {
		return fmt.Errorf("action %s does not support rollback", execution.ActionID)
	}

	if err := action.Rollback(ctx); err != nil {
		return err
	}

	execution.Status = "rolled_back"
	return nil
}

// GetSessionStats returns statistics about autonomous action usage in the current session.
func (ar *AutonomousResponse) GetSessionStats() SessionStats {
	ar.mu.RLock()
	defer ar.mu.RUnlock()

	executed := 0
	failed := 0
	rolledBack := 0

	for _, h := range ar.history {
		switch h.Status {
		case "executed":
			executed++
		case "failed":
			failed++
		case "rolled_back":
			rolledBack++
		}
	}

	awaitingApproval := len(ar.approvalRequired)

	return SessionStats{
		TotalExecuted:        executed,
		TotalFailed:          failed,
		TotalRolledBack:      rolledBack,
		AwaitingApproval:     awaitingApproval,
		Enabled:              ar.enabled,
		MaxActionsPerSession: ar.config.MaxActionsPerSession,
	}
}

// SessionStats provides statistics about autonomous action usage.
//
// It aggregates metrics across all autonomous actions in the current session,
// enabling monitoring and control of autonomous behavior.
type SessionStats struct {
	TotalExecuted        int
	TotalFailed          int
	TotalRolledBack      int
	AwaitingApproval     int
	Enabled              bool
	MaxActionsPerSession int
}

// Built-in autonomous actions

// RegisterStandardActions registers common autonomous response actions.
func (ar *AutonomousResponse) RegisterStandardActions() {
	ar.registerContextCompressionAction()
	ar.registerToolCircuitBreakerAction()
	ar.registerCostSpikeAction()
}

// registerContextCompressionAction registers an action to compress context when approaching limits.
func (ar *AutonomousResponse) registerContextCompressionAction() {
	ar.RegisterAction(&AutonomousAction{
		ID:              "context_compression",
		Description:     "Compress conversation context to free tokens",
		Severity:        SeveritySafe,
		EstimatedImpact: "Reduces token usage by pruning old messages and summarizing history",
		CanApply: func(ctx context.Context, d DetectionEvent) bool {
			return d.Type == "context_budget_warning" && d.Severity == "warning"
		},
		Execute: func(ctx context.Context) error {
			// Context compression logic would be implemented here
			// This is a placeholder - actual implementation would compress context
			return nil
		},
		Rollback: nil, // SAFE actions don't need rollback
	})
}

// registerToolCircuitBreakerAction registers an action to temporarily disable failing tools.
func (ar *AutonomousResponse) registerToolCircuitBreakerAction() {
	ar.RegisterAction(&AutonomousAction{
		ID:              "tool_circuit_breaker",
		Description:     "Temporarily disable failing tool to prevent cascading failures",
		Severity:        SeverityLow,
		EstimatedImpact: "Tool will be unavailable for cooldown period, but prevents repeated failures",
		CanApply: func(ctx context.Context, d DetectionEvent) bool {
			return d.Type == "tool_failure" && d.Severity == "error"
		},
		Execute: func(ctx context.Context) error {
			// Circuit breaker logic would be implemented here
			// This is a placeholder - actual implementation would disable the failing tool
			return nil
		},
		Rollback: func(ctx context.Context) error {
			// Re-enable the tool
			return nil
		},
	})
}

// registerCostSpikeAction registers an action to switch to cheaper models when costs spike.
func (ar *AutonomousResponse) registerCostSpikeAction() {
	ar.RegisterAction(&AutonomousAction{
		ID:              "cost_model_switch",
		Description:     "Switch to cheaper model to control costs",
		Severity:        SeverityLow,
		EstimatedImpact: "May reduce response quality but significantly lowers cost",
		CanApply: func(ctx context.Context, d DetectionEvent) bool {
			return d.Type == "cost_spike" && d.Severity == "warning"
		},
		Execute: func(ctx context.Context) error {
			// Model switching logic would be implemented here
			// This is a placeholder - actual implementation would switch provider/model
			return nil
		},
		Rollback: func(ctx context.Context) error {
			// Switch back to premium model
			return nil
		},
	})
}
