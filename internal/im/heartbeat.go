package im

import (
	"fmt"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/safego"
)

// imHeartbeatInterval is how long the emitter tolerates outbound silence
// while a long-running tool is in flight before posting a heartbeat status.
// Users on IM channels (QQ, Telegram, Feishu, ...) otherwise stare at a
// silent channel for the whole duration of a slow build, test run, or
// remote-agent call after the one-shot start notification.
const imHeartbeatInterval = 3 * time.Minute

// imActivity tracks an in-flight long-running tool call for heartbeat
// purposes. It is registered by EmitToolStatus (TUI path) and TrackActivity
// (daemon path) and cleared by the next outbound Text or ToolResult event.
type imActivity struct {
	summary   string
	startedAt time.Time
}

// TrackActivity registers an in-flight long-running tool so the heartbeat
// watchdog can post periodic "still working" updates while nothing else is
// emitted. It computes the same localized summary as the one-shot start
// notification but does not send anything by itself.
func (e *IMEmitter) TrackActivity(toolName, rawArgs string) {
	if e == nil {
		return
	}
	if !imLongRunningTools[toolName] {
		return // short tools finish before the first heartbeat could fire
	}
	lang := ToolLanguage(e.language)
	present := DescribeTool(lang, toolName, rawArgs)
	summary := strings.TrimSpace(firstNonEmptyStr(present.Activity, present.DisplayName))
	if summary == "" {
		return
	}
	e.trackActivity(summary)
}

// trackActivity registers summary as the pending activity and lazily starts
// the heartbeat watchdog goroutine. Safe to call repeatedly: an existing
// watchdog is reused, only the activity is replaced.
func (e *IMEmitter) trackActivity(summary string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.activity = &imActivity{summary: summary, startedAt: time.Now()}
	if e.hbStop == nil {
		stop := make(chan struct{})
		e.hbStop = stop
		safego.Go("im.emitter.heartbeat", func() { e.heartbeatLoop(stop) })
	}
}

// clearActivityLocked drops the pending activity and stops the watchdog.
// Caller must hold e.mu.
func (e *IMEmitter) clearActivityLocked() {
	e.activity = nil
	if e.hbStop != nil {
		close(e.hbStop)
		e.hbStop = nil
	}
}

// heartbeatLoop posts heartbeat statuses once per interval while an activity
// is pending and the outbound stream has been silent for a full interval.
// Exits when stop is closed (activity finished or emitter closed).
func (e *IMEmitter) heartbeatLoop(stop <-chan struct{}) {
	interval := imHeartbeatInterval
	e.mu.Lock()
	if e.heartbeatInterval > 0 {
		interval = e.heartbeatInterval
	}
	e.mu.Unlock()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			if !e.fireHeartbeat(interval) {
				return
			}
		}
	}
}

// fireHeartbeat posts one heartbeat status when the silence condition holds.
// Returns false when the watchdog should exit because the pending activity
// is already gone.
func (e *IMEmitter) fireHeartbeat(interval time.Duration) bool {
	e.mu.Lock()
	act := e.activity
	if act == nil {
		e.mu.Unlock()
		return false
	}
	// Real outbound traffic (streamed text, tool results, status updates)
	// refreshes lastEventAt; only fire when the channel has been silent.
	if time.Since(e.lastEventAt) < interval {
		e.mu.Unlock()
		return true
	}
	if !e.HasTargets() {
		// No IM channel bound (local-only session): skip the message but
		// keep the watchdog alive in case a channel gets bound mid-run.
		e.mu.Unlock()
		return true
	}
	summary, elapsed := act.summary, time.Since(act.startedAt)
	// Self-schedule: refresh lastEventAt so the next heartbeat lands a full
	// interval after this one instead of stacking on the next tick.
	e.lastEventAt = time.Now()
	e.mu.Unlock()

	e.EmitEvent(OutboundEvent{
		Kind:   OutboundEventStatus,
		Status: formatHeartbeat(e.language, summary, elapsed),
	})
	return true
}

// FinishActivity clears any pending long-running tool activity. Needed by
// quiet/summary output modes, where tool results are buffered locally and
// never flow through EmitEvent, so the watchdog would keep heartbeating for
// an already-finished tool.
func (e *IMEmitter) FinishActivity() {
	if e == nil {
		return
	}
	e.mu.Lock()
	e.clearActivityLocked()
	e.mu.Unlock()
}

// formatHeartbeat renders the periodic "still working" digest shown when a
// long-running tool has produced no outbound events for a while.
func formatHeartbeat(lang string, summary string, elapsed time.Duration) string {
	mins := int(elapsed.Minutes())
	if mins < 1 {
		mins = 1
	}
	if lang == "zh-CN" {
		return fmt.Sprintf("⏳ 仍在执行：%s（已进行约 %d 分钟，暂无新输出）", summary, mins)
	}
	return fmt.Sprintf("⏳ Still working: %s (~%d min elapsed, no new output)", summary, mins)
}
