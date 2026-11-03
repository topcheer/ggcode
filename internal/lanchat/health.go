package lanchat

import (
	"context"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/safego"
)

const (
	AgentStatusOK          = ""
	AgentStatusQuota       = "quota"
	AgentStatusRateLimited = "rate_limited"
	AgentStatusAuth        = "auth"
)

const rateLimitFailThreshold = 2

const healthProbeSuccessThreshold = 2

var (
	healthProbeInitialRateLimit = 1 * time.Minute
	healthProbeInitialSticky    = 5 * time.Minute
	healthProbeMax              = 15 * time.Minute
)

type HealthProber func(ctx context.Context) error

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

func (h *Hub) SetModel(model string) {
	h.mu.Lock()
	changed := h.model != model
	h.model = model
	h.mu.Unlock()
	if !changed {
		return
	}
	h.setAgentStatus(AgentStatusOK)
	h.broadcastPresence()
}

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

	}
}

func (h *Hub) ReportAgentSuccess() {
	h.mu.Lock()
	h.consecRateLimitFails = 0
	h.mu.Unlock()
	h.setAgentStatus(AgentStatusOK)
}

func (h *Hub) AgentStatus() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.agentStatus
}

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

func (h *Hub) startHealthProbeLocked() {
	ctx, cancel := context.WithCancel(context.Background())
	h.probeCancel = cancel
	status := h.agentStatus
	prober := h.healthProber
	safego.Go("lanchat.healthProbe", func() { h.healthProbeLoop(ctx, status, prober) })
}

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
			return
		}
		if err == nil {
			successes++
			debug.Log("lanchat", "health probe success %d/%d (status=%s)", successes, healthProbeSuccessThreshold, status)
			if successes >= healthProbeSuccessThreshold {

				h.ReportAgentSuccess()
				return
			}
		} else {
			successes = 0
			debug.Log("lanchat", "health probe failed (status=%s): %v", status, err)

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
