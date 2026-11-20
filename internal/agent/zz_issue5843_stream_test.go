package agent

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

// Fix-ref pin for issue #5843 direction 1: a user-approved execution keeps
// the run's streaming event channel live - it acknowledges execution start
// and emits still-running heartbeats for long tools. References the new
// gate symbols, so it only compiles at the fix (documented compile-pin).

type streamCollector5843 struct {
	mu     sync.Mutex
	events []provider.StreamEvent
}

func (c *streamCollector5843) onEvent(ev provider.StreamEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, ev)
}

func (c *streamCollector5843) systemTexts() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for _, ev := range c.events {
		if ev.Type == provider.StreamEventSystem {
			out = append(out, ev.Text)
		}
	}
	return out
}

func TestIssue5843_ApprovedExecutionStreamsAckAndHeartbeat(t *testing.T) {
	if approvedToolHeartbeatInterval > 200*time.Millisecond {
		old := approvedToolHeartbeatInterval
		approvedToolHeartbeatInterval = 25 * time.Millisecond
		defer func() { approvedToolHeartbeatInterval = old }()
	}
	probe := &probe5843Tool{sleep: 120 * time.Millisecond}
	coll := &streamCollector5843{}
	a := new5843GateAgent(allowAll5843, nil, probe)

	res := a.executeToolWithPermissionGate(context.Background(), tc5843, coll.onEvent)
	if res.IsError {
		t.Fatalf("probe must succeed, got: %s", res.Content)
	}
	if probe.count() != 1 {
		t.Fatalf("probe must run exactly once, got %d", probe.count())
	}
	texts := coll.systemTexts()
	var ack, beat bool
	for _, txt := range texts {
		if strings.Contains(txt, "approved - executing") {
			ack = true
		}
		if strings.Contains(txt, "still running") {
			beat = true
		}
	}
	if !ack {
		t.Fatalf("user-approved execution must acknowledge its start on the stream, got system events: %v", texts)
	}
	if !beat {
		t.Fatalf("long approved execution must emit still-running heartbeats, got system events: %v", texts)
	}
}

func TestIssue5843_HeartbeatSilentWithoutApproval(t *testing.T) {
	if approvedToolHeartbeatInterval > 200*time.Millisecond {
		old := approvedToolHeartbeatInterval
		approvedToolHeartbeatInterval = 25 * time.Millisecond
		defer func() { approvedToolHeartbeatInterval = old }()
	}
	probe := &probe5843Tool{sleep: 120 * time.Millisecond}
	coll := &streamCollector5843{}
	a := new5843GateAgent(allowAll5843, nil, probe)
	// Neutralize the Ask gate: with no policy there is no approval gate,
	// so the execution is NOT user-approved and must stay silent.
	a.policy = nil

	res := a.executeToolWithPermissionGate(context.Background(), tc5843, coll.onEvent)
	if res.IsError {
		t.Fatalf("probe must succeed, got: %s", res.Content)
	}
	if texts := coll.systemTexts(); len(texts) != 0 {
		t.Fatalf("non-approved executions must stay silent on the stream, got: %v", texts)
	}
}
