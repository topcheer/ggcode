package lanchat

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

func newHealthTestHub(t *testing.T) *Hub {
	t.Helper()
	store := NewStore(t.TempDir())
	return NewHub("node-health", "tui", "http://localhost:0", "", store, WorkspaceMeta{Workspace: "/tmp"})
}

func TestReportAgentFailure_QuotaImmediate(t *testing.T) {
	hub := newHealthTestHub(t)
	hub.ReportAgentFailure(provider.FailureQuota)
	if got := hub.AgentStatus(); got != AgentStatusQuota {
		t.Errorf("status = %q, want %q", got, AgentStatusQuota)
	}
	self := hub.SelfParticipant()
	if self.AgentStatus != AgentStatusQuota || self.AgentStatusSince == 0 {
		t.Errorf("SelfParticipant missing status: %+v", self)
	}
}

func TestReportAgentFailure_RateLimitHysteresis(t *testing.T) {
	hub := newHealthTestHub(t)
	hub.ReportAgentFailure(provider.FailureRateLimit)
	if got := hub.AgentStatus(); got != AgentStatusOK {
		t.Errorf("after 1st rate-limit failure status = %q, want healthy", got)
	}
	hub.ReportAgentFailure(provider.FailureRateLimit)
	if got := hub.AgentStatus(); got != AgentStatusRateLimited {
		t.Errorf("after 2nd consecutive rate-limit failure status = %q, want %q", got, AgentStatusRateLimited)
	}
}

func TestReportAgentFailure_NetworkNeverDegrades(t *testing.T) {
	hub := newHealthTestHub(t)
	for i := 0; i < 5; i++ {
		hub.ReportAgentFailure(provider.FailureNetwork)
		hub.ReportAgentFailure(provider.FailureTransient)
	}
	if got := hub.AgentStatus(); got != AgentStatusOK {
		t.Errorf("network/transient failures must not degrade, got %q", got)
	}
}

func TestReportAgentSuccess_ClearsAndResets(t *testing.T) {
	hub := newHealthTestHub(t)
	hub.ReportAgentFailure(provider.FailureRateLimit)
	hub.ReportAgentSuccess() // resets streak before threshold
	hub.ReportAgentFailure(provider.FailureRateLimit)
	if got := hub.AgentStatus(); got != AgentStatusOK {
		t.Errorf("streak should have been reset by success, got %q", got)
	}

	hub.ReportAgentFailure(provider.FailureQuota)
	hub.ReportAgentSuccess()
	if got := hub.AgentStatus(); got != AgentStatusOK {
		t.Errorf("success should clear degraded status, got %q", got)
	}
	if self := hub.SelfParticipant(); self.AgentStatusSince != 0 {
		t.Errorf("AgentStatusSince should reset to 0, got %d", self.AgentStatusSince)
	}
}

func TestSetModel_ClearsDegraded(t *testing.T) {
	hub := newHealthTestHub(t)
	hub.SetModel("k3")
	hub.ReportAgentFailure(provider.FailureQuota)
	if hub.AgentStatus() != AgentStatusQuota {
		t.Fatal("precondition: degraded")
	}
	hub.SetModel("other-model") // switching models = new quota pool
	if got := hub.AgentStatus(); got != AgentStatusOK {
		t.Errorf("model switch should clear degraded status, got %q", got)
	}
	if self := hub.SelfParticipant(); self.Model != "other-model" {
		t.Errorf("SelfParticipant.Model = %q", self.Model)
	}
}

func TestHealthProbeLoop_RecoversAfterSuccesses(t *testing.T) {
	// Shorten backoff for the test (package vars follow ageOffline pattern).
	origInit, origMax := healthProbeInitialSticky, healthProbeMax
	healthProbeInitialSticky = 10 * time.Millisecond
	healthProbeMax = 20 * time.Millisecond
	defer func() { healthProbeInitialSticky, healthProbeMax = origInit, origMax }()

	hub := newHealthTestHub(t)
	defer hub.Close()

	var calls atomic.Int32
	hub.SetHealthProber(func(ctx context.Context) error {
		calls.Add(1)
		return nil // model healthy again
	})
	hub.ReportAgentFailure(provider.FailureQuota) // starts probe loop

	deadline := time.Now().Add(3 * time.Second)
	for hub.AgentStatus() != AgentStatusOK && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := hub.AgentStatus(); got != AgentStatusOK {
		t.Fatalf("probe loop should clear degraded status after %d successes, still %q", healthProbeSuccessThreshold, got)
	}
	if calls.Load() < healthProbeSuccessThreshold {
		t.Errorf("expected at least %d probe calls, got %d", healthProbeSuccessThreshold, calls.Load())
	}
}

func TestHealthProbeLoop_FailureClassEscalation(t *testing.T) {
	origInit, origMax := healthProbeInitialRateLimit, healthProbeMax
	healthProbeInitialRateLimit = 10 * time.Millisecond
	healthProbeMax = 20 * time.Millisecond
	defer func() { healthProbeInitialRateLimit, healthProbeMax = origInit, origMax }()

	hub := newHealthTestHub(t)
	defer hub.Close()

	// Probe keeps failing, and the error reveals it's actually quota
	// exhaustion, not transient rate limiting.
	hub.SetHealthProber(func(ctx context.Context) error {
		return errors.New("429: exceeded your current quota")
	})
	hub.ReportAgentFailure(provider.FailureRateLimit)
	hub.ReportAgentFailure(provider.FailureRateLimit) // → rate_limited, probe starts

	deadline := time.Now().Add(3 * time.Second)
	for hub.AgentStatus() != AgentStatusQuota && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := hub.AgentStatus(); got != AgentStatusQuota {
		t.Errorf("probe should escalate rate_limited → quota, got %q", got)
	}
}

func TestHealthProbeLoop_StopsOnClose(t *testing.T) {
	origInit := healthProbeInitialSticky
	healthProbeInitialSticky = 5 * time.Millisecond
	defer func() { healthProbeInitialSticky = origInit }()

	hub := newHealthTestHub(t)
	var calls atomic.Int32
	hub.SetHealthProber(func(ctx context.Context) error {
		calls.Add(1)
		return errors.New("still broken")
	})
	hub.ReportAgentFailure(provider.FailureQuota)
	time.Sleep(50 * time.Millisecond) // let a few probes run
	hub.Close()
	stopped := calls.Load()
	time.Sleep(60 * time.Millisecond) // several probe intervals
	if got := calls.Load(); got != stopped {
		t.Errorf("probe loop should stop on Close: calls grew %d → %d", stopped, got)
	}
}

func TestSetAgentStatus_Idempotent(t *testing.T) {
	hub := newHealthTestHub(t)
	hub.ReportAgentFailure(provider.FailureQuota)
	since := hub.SelfParticipant().AgentStatusSince
	time.Sleep(1100 * time.Millisecond)  // cross a unix-second boundary
	hub.setAgentStatus(AgentStatusQuota) // same value → no-op
	if got := hub.SelfParticipant().AgentStatusSince; got != since {
		t.Errorf("idempotent set should not refresh timestamp: %d → %d", since, got)
	}
}
