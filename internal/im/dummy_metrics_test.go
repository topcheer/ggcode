package im

import (
	"sync"
	"testing"
)

// #1302: EvalMetrics had no locking - agent event goroutines (RecordEvent)
// raced HTTP handlers (Reset/IncUserMessages) and Snapshot readers,
// fatal-crashing on concurrent map access. Run under -race to verify.
func TestEvalMetricsConcurrentAccess(t *testing.T) {
	m := NewEvalMetrics()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				m.RecordEvent(OutboundEvent{Kind: OutboundEventToolResult, ToolRes: &ToolResultInfo{ToolName: "x"}})
				m.IncUserMessages()
				_ = m.Snapshot()
				if j%50 == 0 {
					m.Reset()
				}
			}
		}()
	}
	wg.Wait()
	if m.TotalToolCalls == 0 && m.Snapshot()["user_messages"] == 0 {
		t.Error("unexpected empty metrics")
	}
}
