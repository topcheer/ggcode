package im

import (
	"strings"
	"testing"
	"time"
)

func newHeartbeatTestEmitter(t *testing.T, interval time.Duration) (*IMEmitter, *Manager, *namedCaptureSink) {
	t.Helper()
	mgr := NewManager()
	sink := &namedCaptureSink{name: "qq"}
	mgr.RegisterSink(sink)
	mgr.currentBindings["qq"] = &ChannelBinding{Adapter: "qq", ChannelID: "ch1"}
	e := NewIMEmitter(mgr, "en", "/tmp")
	e.heartbeatInterval = interval
	t.Cleanup(e.Close)
	return e, mgr, sink
}

func heartbeatStatuses(sink *namedCaptureSink) []string {
	var out []string
	for _, ev := range sink.events() {
		if ev.Kind == OutboundEventStatus {
			out = append(out, ev.Status)
		}
	}
	return out
}

// A long-running tool that receives no further outbound events must produce
// periodic heartbeat statuses after the configured interval.
func TestHeartbeatFiresDuringSilence(t *testing.T) {
	e, _, sink := newHeartbeatTestEmitter(t, 30*time.Millisecond)

	e.TrackActivity("run_command", `{"command":"go build ./..."}`)
	deadline := time.Now().Add(3 * time.Second)
	for len(heartbeatStatuses(sink)) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	stats := heartbeatStatuses(sink)
	if len(stats) == 0 {
		t.Fatal("expected at least one heartbeat status during silence")
	}
	if !strings.Contains(stats[0], "Still working") {
		t.Errorf("heartbeat = %q, want it to contain %q", stats[0], "Still working")
	}
}

// A short tool must never register activity, so no heartbeat can fire.
func TestHeartbeatSkipsShortTools(t *testing.T) {
	e, _, sink := newHeartbeatTestEmitter(t, 20*time.Millisecond)

	e.TrackActivity("read_file", `{"path":"/tmp/a.go"}`)
	time.Sleep(120 * time.Millisecond)
	if got := heartbeatStatuses(sink); len(got) != 0 {
		t.Errorf("short tool produced heartbeats: %v", got)
	}
}

// An outbound Text or ToolResult event must clear the pending activity so
// the watchdog stops once real progress happened.
func TestHeartbeatStopsAfterToolResult(t *testing.T) {
	e, _, sink := newHeartbeatTestEmitter(t, 30*time.Millisecond)

	e.TrackActivity("run_command", `{"command":"make test"}`)
	deadline := time.Now().Add(3 * time.Second)
	for len(heartbeatStatuses(sink)) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if len(heartbeatStatuses(sink)) == 0 {
		t.Fatal("expected a heartbeat before the result")
	}

	e.EmitEvent(OutboundEvent{Kind: OutboundEventToolResult, ToolRes: &ToolResultInfo{
		ToolName: "run_command",
		Args:     `{"command":"make test"}`,
		Result:   "ok",
	}})
	time.Sleep(150 * time.Millisecond)
	e.mu.Lock()
	pending := e.activity != nil
	e.mu.Unlock()
	if pending {
		t.Error("activity still pending after tool result event")
	}
}

// FinishActivity must clear the pending activity even when the result is
// buffered (quiet/summary modes) and never flows through EmitEvent.
func TestFinishActivityClearsPending(t *testing.T) {
	e, _, _ := newHeartbeatTestEmitter(t, 20*time.Millisecond)

	e.TrackActivity("run_command", `{"command":"make test"}`)
	e.FinishActivity()
	e.mu.Lock()
	pending := e.activity != nil
	stopped := e.hbStop == nil
	e.mu.Unlock()
	if pending {
		t.Error("activity still pending after FinishActivity")
	}
	if !stopped {
		t.Error("heartbeat watchdog not stopped after FinishActivity")
	}
}

// Elapsed-minute formatting must localize and never report zero minutes.
func TestFormatHeartbeat(t *testing.T) {
	en := formatHeartbeat("en", "Executing make test", 30*time.Second)
	if !strings.Contains(en, "Still working: Executing make test") || !strings.Contains(en, "1 min") {
		t.Errorf("en heartbeat = %q", en)
	}
	zh := formatHeartbeat("zh-CN", "执行 make test", 2*time.Minute)
	if !strings.Contains(zh, "仍在执行") || !strings.Contains(zh, "2 分钟") {
		t.Errorf("zh heartbeat = %q", zh)
	}
}

// Nil-receiver safety, matching the rest of the emitter API.
func TestTrackActivityNilReceiver(t *testing.T) {
	var e *IMEmitter
	e.TrackActivity("run_command", `{}`)
	e.FinishActivity()
}

// TrackActivity must not emit anything on its own; only the watchdog does.
func TestTrackActivityDoesNotEmit(t *testing.T) {
	e, _, sink := newHeartbeatTestEmitter(t, time.Hour)

	e.TrackActivity("run_command", `{"command":"sleep 30"}`)
	time.Sleep(50 * time.Millisecond)
	if got := sink.events(); len(got) != 0 {
		t.Errorf("TrackActivity emitted %d events, want 0", len(got))
	}
}
