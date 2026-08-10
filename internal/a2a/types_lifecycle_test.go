package a2a

import (
	"testing"
	"time"
)

func TestAgentLifecycleState_IsDeprecatedOrWorse(t *testing.T) {
	tests := []struct {
		name  string
		state AgentLifecycleState
		want  bool
	}{
		{"active should not be deprecated", AgentLifecycleActive, false},
		{"deprecated should be deprecated", AgentLifecycleDeprecated, true},
		{"retired should be deprecated", AgentLifecycleRetired, true},
		{"revoked should be deprecated", AgentLifecycleRevoked, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.state.IsDeprecatedOrWorse(); got != tt.want {
				t.Errorf("AgentLifecycleState.IsDeprecatedOrWorse() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAgentLifecycleState_IsActive(t *testing.T) {
	tests := []struct {
		name  string
		state AgentLifecycleState
		want  bool
	}{
		{"active should be active", AgentLifecycleActive, true},
		{"deprecated should not be active", AgentLifecycleDeprecated, false},
		{"retired should not be active", AgentLifecycleRetired, false},
		{"revoked should not be active", AgentLifecycleRevoked, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.state.IsActive(); got != tt.want {
				t.Errorf("AgentLifecycleState.IsActive() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAgentLifecycleState_IsTerminal(t *testing.T) {
	tests := []struct {
		name  string
		state AgentLifecycleState
		want  bool
	}{
		{"active should not be terminal", AgentLifecycleActive, false},
		{"deprecated should not be terminal", AgentLifecycleDeprecated, false},
		{"retired should be terminal", AgentLifecycleRetired, true},
		{"revoked should be terminal", AgentLifecycleRevoked, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.state.IsTerminal(); got != tt.want {
				t.Errorf("AgentLifecycleState.IsTerminal() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAgentCard_LifecycleIntegration(t *testing.T) {
	now := time.Now()
	card := AgentCard{
		Name:        "test-agent",
		Description: "Test agent with lifecycle",
		URL:         "https://example.com",
		Lifecycle: &AgentLifecycleInfo{
			State:     AgentLifecycleActive,
			Timestamp: now,
			Rationale: "Initial release",
		},
	}

	// Verify lifecycle is properly set
	if card.Lifecycle == nil {
		t.Fatal("Expected Lifecycle to be set")
	}
	if card.Lifecycle.State != AgentLifecycleActive {
		t.Errorf("Expected state %v, got %v", AgentLifecycleActive, card.Lifecycle.State)
	}

	// Test deprecated state warning
	card.Lifecycle.State = AgentLifecycleDeprecated
	if !card.Lifecycle.State.IsDeprecatedOrWorse() {
		t.Error("Expected deprecated state to trigger warning")
	}

	// Test terminal state
	card.Lifecycle.State = AgentLifecycleRevoked
	if !card.Lifecycle.State.IsTerminal() {
		t.Error("Expected revoked state to be terminal")
	}
}
