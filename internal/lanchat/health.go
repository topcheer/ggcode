package lanchat

import (
	"context"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/safego"
)

// Model health reporting: when the local LLM becomes unusable (quota
// exhausted, auth failure, sustained rate limiting), the node broadcasts a
// degraded status in its presence so peers can make informed decisions when
// choosing collaboration targets. Advisory only — peers still deliver DMs;
// the status helps orchestrating agents skip or deprioritize degraded nodes.

// Agent status values broadcast in Participant.AgentStatus.
const (
	AgentStatusOK          = ""             // model healthy
	AgentStatusQuota       = "quota"        // permanent quota/billing exhaustion
	AgentStatusRateLimited = "rate_limited" // sustained transient rate limiting
	AgentStatusAuth        = "auth"         // authentication/authorization failure
)

// rateLimitFailThreshold is how many consecutive run-level rate-limit
// failures mark the node degraded. A single 429 often recovers inside the
// provider's own retry loop; requiring consecutive failures avoids flapping.
const rateLimitFailThreshold = 2

// healthProbeSuccessThreshold is how many consecutive probe successes clear
// the degraded status. Guards against single lucky responses during flapping.
const healthProbeSuccessThreshold = 2

// Probe backoff schedule. Package vars (not consts) so tests can shorten them,
// following the ageOffline/presenceHeartbeat pattern in types.go.
var (
	// healthProbeInitialRateLimit is the first probe delay for transient
	// rate limiting — expected to recover within minutes.
	healthProbeInitialRateLimit = 1 * time.Minute
	// healthProbeInitialSticky is the first probe delay for quota/auth
	// failures — these usually require user action (top-up, new key, plan
	// reset), so probe less aggressively.
	healthProbeInitialSticky = 5 * time.Minute
	// healthProbeMax caps the exponential backoff.
	healthProbeMax = 15 * time.Minute
)

// HealthProber performs a minimal LLM call (e.g. max_tokens=1 completion) to
// test whether the current model is usable again. Returns nil on success.
// Registered by the frontend (TUI/desktop/daemon) which owns the provider.
type HealthProber func(ctx context.Context) error

// SetHealthProber registers the callback used by the recovery probe loop.
// If the node is currently degraded, starts probing immediately.
func (h *Hub) SetHealthProber(fn HealthProber) {
	h.mu.Lock()
	h.healthProber = fn
	degraded := h.agentStatus != AgentStatusOK
	probing := h.probeCancel != nil
	if degraded && !probing && fn != nil {
		h.startHealthProbeLocked()
	}
	h.mu.Unlock()
}

// SetModel records the currently configured model name and clears any
// degraded status — switching models means a different quota pool / credential,
// so previous degradation no longer applies. Broadcasts presence when the
// model (or cleared status) is visible to peers.
func (h *Hub) SetModel(model string) {
	h.mu.Lock()
	changed := h.model != model
	h.model = model
	h.mu.Unlock()
	if !changed {
		return
	}
	h.setAgentStatus(AgentStatusOK) // clears + broadcasts if was degraded
	h.broadcastPresence()           // propagate new model name regardless
}

// ReportAgentFailure classifies a run-level LLM failure and updates the
// degraded status with hysteresis:
//   - quota/auth: mark immediately (retry provably cannot succeed)
//   - rate limit: mark after rateLimitFailThreshold consecutive failures
//   - network/transient: never mark (says nothing about model health)
func (h *Hub) ReportAgentFailure(class provider.FailureClass) {
	switch class {
	case provider.FailureQuota:
		h.setAgentStatus(AgentStatusQuota)
	case provider.FailureAuth:
		h.setAgentStatus(AgentStatusAuth)
	case provider.FailureRateLimit:
		h.mu.Lock()
		h.consecRateLimitFails++
		n := h.consecRateLimitFails
		h.mu.Unlock()
		if n >= rateLimitFailThreshold {
			h.setAgentStatus(AgentStatusRateLimited)
		}
	default:
		// FailureNetwork/FailureTransient/FailureNone: not a model health signal.
	}
}

// ReportAgentSuccess records a successful LLM run: resets the consecutive
// failure counter and clears any degraded status (broadcasting the recovery).
func (h *Hub) ReportAgentSuccess() {
	h.mu.Lock()
	h.consecRateLimitFails = 0
	h.mu.Unlock()
	h.setAgentStatus(AgentStatusOK)
}

// AgentStatus returns the current degraded status ("" when healthy).
func (h *Hub) AgentStatus() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.agentStatus
}

// setAgentStatus transitions the degraded status, broadcasting presence on
// change, and manages the recovery probe loop lifecycle.
func (h *Hub) setAgentStatus(status string) {
	h.mu.Lock()
	if h.agentStatus == status {
		h.mu.Unlock()
		return
	}
	old := h.agentStatus
	h.agentStatus = status
	if status != AgentStatusOK {
		h.agentStatusSince = time.Now().Unix()
	} else {
		h.agentStatusSince = 0
	}
	// Stop any running probe loop; restart below if still degraded.
	if h.probeCancel != nil {
		h.probeCancel()
		h.probeCancel = nil
	}
	if status != AgentStatusOK && h.healthProber != nil {
		h.startHealthProbeLocked()
	}
	h.mu.Unlock()

	debug.Log("lanchat", "agent status changed: %q -> %q", old, status)
	h.broadcastPresence()
}

// broadcastPresence sends this node's current presence to all online peers.
// Used by busy/model/health status transitions.
func (h *Hub) broadcastPresence() {
	h.mu.RLock()
	peers := make([]Participant, 0, len(h.peers))
	for _, p := range h.peers {
		if p.Online {
			peers = append(peers, *p)
		}
	}
	h.mu.RUnlock()

	for _, peer := range peers {
		safego.Go("lanchat.statusPresence", func() { h.sendPresence(peer) })
	}
}

// startHealthProbeLocked starts the recovery probe goroutine. Caller must
// hold h.mu and ensure agentStatus != "" and probeCancel == nil.
func (h *Hub) startHealthProbeLocked() {
	ctx, cancel := context.WithCancel(context.Background())
	h.probeCancel = cancel
	status := h.agentStatus
	prober := h.healthProber
	safego.Go("lanchat.healthProbe", func() { h.healthProbeLoop(ctx, status, prober) })
}

// healthProbeLoop probes the model with exponential backoff until it recovers
// (healthProbeSuccessThreshold consecutive successes), the status changes
// (ctx cancelled by setAgentStatus), or the hub closes.
func (h *Hub) healthProbeLoop(ctx context.Context, status string, prober HealthProber) {
	delay := healthProbeInitialSticky
	if status == AgentStatusRateLimited {
		delay = healthProbeInitialRateLimit
	}
	successes := 0
	for {
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}

		err := prober(ctx)
		if ctx.Err() != nil {
			return // cancelled during probe
		}
		if err == nil {
			successes++
			debug.Log("lanchat", "health probe success %d/%d (status=%s)", successes, healthProbeSuccessThreshold, status)
			if successes >= healthProbeSuccessThreshold {
				// ReportAgentSuccess -> setAgentStatus(OK) cancels this ctx.
				h.ReportAgentSuccess()
				return
			}
		} else {
			successes = 0
			debug.Log("lanchat", "health probe failed (status=%s): %v", status, err)
			// If the failure class changed (e.g. rate limit turned out to be
			// quota exhaustion), update the broadcast reason. setAgentStatus
			// cancels this loop and starts a fresh one with the new backoff.
			if class := provider.ClassifyLLMError(err); class == provider.FailureQuota || class == provider.FailureAuth {
				if class.String() != status {
					h.setAgentStatus(class.String())
					return
				}
			}
		}

		delay *= 2
		if delay > healthProbeMax {
			delay = healthProbeMax
		}
	}
}
