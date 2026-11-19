package plugin

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/mcp"
	"github.com/topcheer/ggcode/internal/safego"
)

// #5814 proactive health probing for long-lived instances.
//
// HTTP/SSE transports self-heal via the notification-stream reconnect loop and
// websocket transports already had a 60s ListTools probe; stdio servers had
// nothing: a dead or half-dead child (process alive, pipe stalled) stayed
// invisible until a tool call tripped the breaker or hung to the request
// timeout. This file adds, for stdio servers:
//
//   - a periodic lightweight liveness probe (ping when the server answers it,
//     else a count-only tools/list), default 10m, gated by config/env;
//   - breaker OPEN -> auto re-init (respawn the process) with capped
//     retry/backoff and a debug.Log audit trail;
//   - a pre-call liveness lease so calls against half-dead servers fail fast
//     with a "server likely dead, probing" notice instead of hanging;
//   - per-server health state (healthy/stale/dead/reiniting) surfaced in
//     MCPServerInfo and list_mcp_capabilities for fleet audits.

// MCPHealth is the coarse per-server health state for fleet observability.
type MCPHealth string

const (
	MCPHealthUnknown   MCPHealth = "unknown"
	MCPHealthHealthy   MCPHealth = "healthy"
	MCPHealthStale     MCPHealth = "stale"
	MCPHealthDead      MCPHealth = "dead"
	MCPHealthReiniting MCPHealth = "reiniting"
)

const (
	// defaultStdioHealthProbeInterval is the ~10m default the issue asks for.
	defaultStdioHealthProbeInterval = 10 * time.Minute
)

var (
	// stdioProbeTimeout bounds a single liveness probe (var for tests).
	stdioProbeTimeout = 30 * time.Second
	// stdioProbeKickMinInterval rate-limits lease-failure probe kicks.
	stdioProbeKickMinInterval = 5 * time.Second
	// stdioReinitConnectTimeout bounds one re-init Connect attempt (var for
	// tests).
	stdioReinitConnectTimeout = 2 * time.Minute
)

// stdioReinitBackoff is the capped auto re-init schedule; after the last entry
// the loop gives up (the periodic probe stays as the long-stop retry).
var stdioReinitBackoff = []time.Duration{2 * time.Second, 5 * time.Second, 15 * time.Second, 30 * time.Second, 60 * time.Second}

const (
	envHealthProbe         = "GGCODE_MCP_HEALTH_PROBE"
	envHealthProbeInterval = "GGCODE_MCP_HEALTH_PROBE_INTERVAL"
)

// pluginTransportOf mirrors the transport inference in mcp.NewClientFromConfig.
func pluginTransportOf(cfg config.MCPServerConfig) string {
	t := strings.ToLower(strings.TrimSpace(cfg.Type))
	if t == "" {
		if cfg.Command == "" && strings.TrimSpace(cfg.URL) != "" {
			return "http"
		}
		return "stdio"
	}
	if t == "sse" {
		return "http"
	}
	return t
}

func envFlagDisabled(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "0", "off", "false", "disabled", "no":
		return true
	}
	return false
}

func parseEnvInterval(value string) (time.Duration, bool) {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return 0, false
	}
	if secs, err := strconv.Atoi(raw); err == nil {
		return time.Duration(secs) * time.Second, true
	}
	if d, err := time.ParseDuration(raw); err == nil {
		return d, true
	}
	return 0, false
}

// healthProbePlan resolves the probe interval for a transport:
// per-server config (health_probe_interval seconds; 0 disables) wins, then
// GGCODE_MCP_HEALTH_PROBE_INTERVAL, then GGCODE_MCP_HEALTH_PROBE=off disables,
// then the transport default (ws: 60s, stdio: 10m).
func (m *MCPPlugin) healthProbePlan(transport string) (time.Duration, bool) {
	var def time.Duration
	switch transport {
	case "stdio":
		def = defaultStdioHealthProbeInterval
	case "ws", "websocket":
		def = wsHealthProbeInterval
	default:
		return 0, false
	}
	if m.cfg.HealthProbeIntervalSeconds != nil {
		secs := *m.cfg.HealthProbeIntervalSeconds
		if secs <= 0 {
			return 0, false
		}
		return time.Duration(secs) * time.Second, true
	}
	if envFlagDisabled(os.Getenv(envHealthProbe)) {
		return 0, false
	}
	if d, ok := parseEnvInterval(os.Getenv(envHealthProbeInterval)); ok {
		if d <= 0 {
			return 0, false
		}
		return d, true
	}
	return def, true
}

// startHealthMonitor wires and runs the per-server health machinery. It is
// called from Connect with m.mu held; the monitor goroutine itself never takes
// m.mu while the caller holds it (it only uses atomics and its own cancel).
func (m *MCPPlugin) startHealthMonitor(client *mcp.Client) {
	transport := pluginTransportOf(m.cfg)
	interval, enabled := m.healthProbePlan(transport)
	m.healthInterval = interval

	// Connect just completed a full discovery round-trip, so the server is
	// alive right now (#5814). Caller holds m.mu (Connect). With the probe
	// disabled there is no evidence to claim beyond "unknown".
	if enabled {
		m.setHealthLocked(MCPHealthHealthy, "connected")
	} else {
		m.setHealthLocked(MCPHealthUnknown, "probe disabled")
	}

	if transport == "stdio" && enabled && m.adapter != nil {
		m.wireStdioHealthHooks(client, 2*interval)
	}
	if !enabled {
		debug.Log("mcp-health", "server=%s transport=%s periodic health probe disabled by config/env", m.cfg.Name, transport)
		return
	}

	if m.healthCancel != nil {
		m.healthCancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.healthCancel = cancel

	debug.Log("mcp-health", "server=%s health monitor started transport=%s interval=%s", m.cfg.Name, transport, interval)

	safego.Go("plugin.mcp.healthMonitor", func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			if transport == "ws" || transport == "websocket" {
				if !m.probeWSOnce(ctx, client) {
					return
				}
				continue
			}
			probeCtx, probeCancel := context.WithTimeout(ctx, stdioProbeTimeout)
			m.probeStdioOnce(probeCtx, client, "periodic")
			probeCancel()
		}
	})
}

// wireStdioHealthHooks installs the pre-call liveness lease fast-fail and the
// breaker OPEN -> auto re-init callback on the adapter (called with m.mu held).
func (m *MCPPlugin) wireStdioHealthHooks(client *mcp.Client, lease time.Duration) {
	client.SetLivenessLease(lease)
	adapter := m.adapter
	adapter.SetLivenessGuard(func() (string, bool) {
		return m.leaseFailFast(client, lease)
	})
	adapter.SetBreakerOnOpen(func() {
		debug.Log("mcp-health", "server=%s breaker OPEN after consecutive infra failures -> stdio auto re-init", m.cfg.Name)
		m.startAutoReinit("breaker-open")
	})
	debug.Log("mcp-health", "server=%s liveness lease armed (%s); breaker OPEN now triggers auto re-init", m.cfg.Name, lease)
}

// leaseFailFast implements the #5814 half-dead watchdog: when no round-trip
// succeeded within the lease, fail the call immediately with a "likely dead,
// probing" notice and kick a health probe in the background.
func (m *MCPPlugin) leaseFailFast(client *mcp.Client, lease time.Duration) (string, bool) {
	last, _, expired := client.LivenessLeaseState()
	if !expired {
		return "", false
	}
	age := time.Since(last).Round(time.Second)
	m.kickHealthProbe(client)
	msg := fmt.Sprintf(
		"mcp[%s]: server likely dead - no successful round-trip in %s (liveness lease %s). "+
			"A health probe has been triggered; this call fast-failed instead of hanging until the request timeout. "+
			"Retry after the server health returns healthy (see list_mcp_capabilities), or use other tools now.",
		m.cfg.Name, age, lease.Round(time.Second))
	return msg, true
}

// kickHealthProbe fires a rate-limited immediate probe off the request path.
func (m *MCPPlugin) kickHealthProbe(client *mcp.Client) {
	now := time.Now()
	m.probeKickMu.Lock()
	if now.Sub(m.probeKickAt) < stdioProbeKickMinInterval {
		m.probeKickMu.Unlock()
		return
	}
	m.probeKickAt = now
	m.probeKickMu.Unlock()
	safego.Go("plugin.mcp.probeKick", func() {
		ctx, cancel := context.WithTimeout(context.Background(), stdioProbeTimeout)
		defer cancel()
		m.probeStdioOnce(ctx, client, "lease-kick")
	})
}

// probeWSOnce keeps the pre-#5814 websocket probe semantics: a ListTools
// round-trip every tick; only connection-class errors trigger a reconnect
// (then the monitor stops - the fresh Connect starts a new one). Returns false
// when the monitor should stop.
func (m *MCPPlugin) probeWSOnce(ctx context.Context, client *mcp.Client) bool {
	m.mu.RLock()
	connected := m.connected && m.client == client
	m.mu.RUnlock()
	if !connected {
		return true
	}
	probeCtx, probeCancel := context.WithTimeout(ctx, 30*time.Second)
	_, err := client.ListTools(probeCtx)
	probeCancel()
	if err == nil {
		m.mu.Lock()
		m.lastProbeAt = time.Now()
		m.lastProbeErr = ""
		m.setHealthLocked(MCPHealthHealthy, "probe ok")
		m.mu.Unlock()
		return true
	}
	if !isWSConnError(err) {
		m.mu.Lock()
		m.lastProbeAt = time.Now()
		m.lastProbeErr = err.Error()
		m.mu.Unlock()
		return true
	}
	debug.Log("mcp-health", "server=%s transport dropped (%v), reconnecting", m.cfg.Name, err)
	m.attemptReconnect(ctx)
	return false
}

// probeStdioOnce probes a stdio server and drives the dead -> auto re-init
// transition. trigger names the cause for the audit trail.
func (m *MCPPlugin) probeStdioOnce(ctx context.Context, client *mcp.Client, trigger string) {
	m.mu.RLock()
	current := m.client
	connected := m.connected
	closedFlag := m.closed
	m.mu.RUnlock()
	if !connected || current != client || closedFlag {
		return
	}
	err := client.Probe(ctx)
	now := time.Now()
	m.mu.Lock()
	m.lastProbeAt = now
	if err == nil {
		m.lastProbeErr = ""
		m.setHealthLocked(MCPHealthHealthy, "probe ok ("+trigger+")")
	} else {
		m.lastProbeErr = err.Error()
		m.setHealthLocked(MCPHealthDead, "probe failed ("+trigger+")")
	}
	m.mu.Unlock()
	if err == nil {
		debug.Log("mcp-health", "server=%s probe ok (trigger=%s)", m.cfg.Name, trigger)
		return
	}
	debug.Log("mcp-health", "server=%s probe FAILED (trigger=%s): %v - server considered dead, starting auto re-init", m.cfg.Name, trigger, err)
	m.startAutoReinit(fmt.Sprintf("probe-failed(%s): %v", trigger, err))
}

// startAutoReinit respawns a dead stdio server with a capped retry/backoff
// schedule. Single-flight: concurrent triggers (breaker open + probe failure)
// collapse into one loop. When the schedule is exhausted the server stays dead
// until the next periodic probe or a manual /mcp reconnect.
func (m *MCPPlugin) startAutoReinit(reason string) {
	m.reinitMu.Lock()
	if m.reinitActive {
		m.reinitMu.Unlock()
		debug.Log("mcp-health", "server=%s auto re-init already running; trigger ignored (%s)", m.cfg.Name, reason)
		return
	}
	m.reinitActive = true
	m.reinitMu.Unlock()

	safego.Go("plugin.mcp.autoReinit", func() {
		defer func() {
			m.reinitMu.Lock()
			m.reinitActive = false
			m.reinitMu.Unlock()
		}()

		m.mu.RLock()
		closedFlag := m.closed
		deadClient := m.client
		m.mu.RUnlock()
		if closedFlag {
			return
		}
		m.mu.Lock()
		m.setHealthLocked(MCPHealthReiniting, reason)
		m.mu.Unlock()
		m.notifyStateChange()
		debug.Log("mcp-health", "server=%s auto re-init start (%s): %d attempts, backoff %v", m.cfg.Name, reason, len(stdioReinitBackoff), stdioReinitBackoff)

		for attempt, delay := range stdioReinitBackoff {
			timer := time.NewTimer(delay)
			select {
			case <-timer.C:
			case <-m.reinitDone():
				timer.Stop()
				return
			}
			m.mu.RLock()
			healed := m.connected && m.client != nil && m.client != deadClient
			closedFlag = m.closed
			m.mu.RUnlock()
			if closedFlag {
				debug.Log("mcp-health", "server=%s auto re-init aborted (plugin closed)", m.cfg.Name)
				return
			}
			if healed {
				// Another path (process-exit reconnect watcher, manual /mcp
				// reconnect) already respawned the server; a redundant respawn
				// would tear the fresh connection down again.
				debug.Log("mcp-health", "server=%s auto re-init skipped: server reconnected by another path", m.cfg.Name)
				m.mu.Lock()
				m.setHealthLocked(MCPHealthHealthy, "healed by reconnect path")
				m.mu.Unlock()
				m.notifyStateChange()
				return
			}
			debug.Log("mcp-health", "server=%s auto re-init attempt %d/%d", m.cfg.Name, attempt+1, len(stdioReinitBackoff))
			reinitCtx, cancel := context.WithTimeout(context.Background(), stdioReinitConnectTimeout)
			ok := m.attemptReconnect(reinitCtx)
			cancel()
			if ok {
				debug.Log("mcp-health", "server=%s auto re-init SUCCEEDED on attempt %d", m.cfg.Name, attempt+1)
				m.mu.Lock()
				m.setHealthLocked(MCPHealthHealthy, "re-init ok")
				m.mu.Unlock()
				m.notifyStateChange()
				return
			}
		}
		debug.Log("mcp-health", "server=%s auto re-init EXHAUSTED (%d attempts); staying dead until next periodic probe or manual reconnect", m.cfg.Name, len(stdioReinitBackoff))
		m.mu.Lock()
		m.setHealthLocked(MCPHealthDead, "re-init exhausted")
		m.mu.Unlock()
		m.notifyStateChange()
	})
}

// reinitDone returns a channel closed when the plugin is closed; nil-safe.
func (m *MCPPlugin) reinitDone() <-chan struct{} {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.closedCh
}

// setHealthLocked records a health transition with an audit log line. Caller
// holds m.mu. "stale" is derived at read time, never stored.
func (m *MCPPlugin) setHealthLocked(next MCPHealth, detail string) {
	prev := m.health
	if prev == "" {
		prev = MCPHealthUnknown
	}
	m.health = next
	m.healthSince = time.Now()
	if prev != next {
		debug.Log("mcp-health", "server=%s health %s -> %s (%s)", m.cfg.Name, prev, next, detail)
	}
}

// healthForReport returns the effective health, deriving the transient
// "stale" state (connected + last round-trip older than the probe interval)
// without persisting it. Caller holds m.mu (at least RLock).
func (m *MCPPlugin) healthForReport() MCPHealth {
	state := m.health
	if state == "" {
		state = MCPHealthUnknown
	}
	if state == MCPHealthHealthy && m.client != nil && m.healthInterval > 0 {
		if last := m.client.LastHealthyAt(); !last.IsZero() && time.Since(last) > m.healthInterval {
			return MCPHealthStale
		}
	}
	return state
}
