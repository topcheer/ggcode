package agent

import (
	"strings"
	"testing"
)

func TestVerbosityDrift_Reset(t *testing.T) {
	v := newVerbosityDriftState()
	// Record some data
	v.recordIteration(1000, 0)
	v.recordIteration(2000, 1)
	v.warnCount = 1
	v.fired = true

	v.reset()

	if len(v.records) != 0 {
		t.Errorf("records not cleared: %d", len(v.records))
	}
	if v.warnCount != 0 {
		t.Errorf("warnCount not reset: %d", v.warnCount)
	}
	if v.fired {
		t.Error("fired not reset")
	}
	if v.initialized {
		t.Error("initialized not reset")
	}
}

func TestVerbosityDrift_NoDrift_ShortWindow(t *testing.T) {
	v := newVerbosityDriftState()
	// Only a few iterations - not enough data
	v.recordIteration(1000, 0)
	v.recordIteration(2000, 1)
	v.recordIteration(3000, 2)

	msg := v.maybeWarn(3)
	if msg != "" {
		t.Errorf("should not fire with insufficient data: %s", msg)
	}
}

func TestVerbosityDrift_NoDrift_ProductiveAgent(t *testing.T) {
	v := newVerbosityDriftState()
	v.windowSize = 4 // smaller window for test

	// Agent is productive: tokens grow but edits also grow
	v.recordIteration(1000, 0)  // init
	v.recordIteration(3000, 1)  // +2000 tokens, +1 edit
	v.recordIteration(6000, 2)  // +3000 tokens, +1 edit
	v.recordIteration(10000, 4) // +4000 tokens, +2 edits
	v.recordIteration(15000, 6) // +5000 tokens, +2 edits

	msg := v.maybeWarn(5)
	// Productivity is increasing, should not fire
	if msg != "" {
		t.Errorf("should not fire when productivity is increasing: %s", msg)
	}
}

func TestVerbosityDrift_Detected(t *testing.T) {
	v := newVerbosityDriftState()
	v.windowSize = 8

	// Simulate verbosity drift: token consumption grows each iteration
	// but edits plateau after the first couple
	v.recordIteration(1000, 0)  // init
	v.recordIteration(3000, 1)  // +2000 tokens, +1 edit (early)
	v.recordIteration(5000, 2)  // +2000 tokens, +1 edit (early)
	v.recordIteration(6000, 2)  // +1000 tokens, +0 edits (early)
	v.recordIteration(7000, 2)  // +1000 tokens, +0 edits (early)
	v.recordIteration(11000, 3) // +4000 tokens, +1 edit (late)
	v.recordIteration(17000, 3) // +6000 tokens, +0 edits (late)
	v.recordIteration(25000, 3) // +8000 tokens, +0 edits (late)
	v.recordIteration(35000, 3) // +10000 tokens, +0 edits (late)

	msg := v.maybeWarn(9)
	if msg == "" {
		t.Error("should detect verbosity drift")
	}
	if !strings.Contains(msg, "verbosity-drift") {
		t.Errorf("message should contain tag: %s", msg)
	}
	if !strings.Contains(msg, "token") {
		t.Errorf("message should mention tokens: %s", msg)
	}
}

func TestVerbosityDrift_NoDrift_TokenGrowth(t *testing.T) {
	v := newVerbosityDriftState()
	v.windowSize = 4

	// Token consumption grows but edits also grow consistently
	v.recordIteration(1000, 0)  // init
	v.recordIteration(3000, 1)  // +2000 tokens, +1 edit
	v.recordIteration(7000, 2)  // +4000 tokens, +1 edit
	v.recordIteration(12000, 3) // +5000 tokens, +1 edit
	v.recordIteration(18000, 4) // +6000 tokens, +1 edit

	// Edits are constant (1 per iter) in both halves, but token growth < 1.5x
	// Actually let's check: first half avg = 3000, second half avg = 5500
	// ratio = 1.83 > 1.5, but edits are equal (1 == 1), so it SHOULD fire
	// Let me adjust: make edits increase to prevent firing
	v2 := newVerbosityDriftState()
	v2.windowSize = 4
	v2.recordIteration(1000, 0)  // init
	v2.recordIteration(3000, 1)  // +2000 tokens, +1 edit
	v2.recordIteration(7000, 2)  // +4000 tokens, +1 edit
	v2.recordIteration(12000, 4) // +5000 tokens, +2 edits
	v2.recordIteration(18000, 7) // +6000 tokens, +3 edits

	msg := v2.maybeWarn(5)
	// Second half has more edits (2.5 avg vs 1.5), should NOT fire
	if msg != "" {
		t.Errorf("should not fire when edits increasing: %s", msg)
	}
}

func TestVerbosityDrift_MaxWarnings(t *testing.T) {
	v := newVerbosityDriftState()
	v.windowSize = 8
	v.maxWarnings = 1

	// Set up drift conditions
	v.recordIteration(1000, 0)
	v.recordIteration(3000, 1)
	v.recordIteration(5000, 2)
	v.recordIteration(6000, 2)
	v.recordIteration(7000, 2)
	v.recordIteration(12000, 2)
	v.recordIteration(19000, 2)
	v.recordIteration(28000, 2)
	v.recordIteration(39000, 2)

	msg1 := v.maybeWarn(9)
	if msg1 == "" {
		t.Fatal("first warning should fire")
	}

	// Should not fire again (maxWarnings reached)
	msg2 := v.maybeWarn(10)
	if msg2 != "" {
		t.Errorf("should not fire after maxWarnings: %s", msg2)
	}
}

func TestVerbosityDrift_NilSafe(t *testing.T) {
	var v *verbosityDriftState
	v.reset()
	v.recordIteration(1000, 0)
	msg := v.maybeWarn(1)
	if msg != "" {
		t.Errorf("nil state should not produce message: %s", msg)
	}
}

func TestVerbosityDrift_CompactionReset(t *testing.T) {
	v := newVerbosityDriftState()
	v.windowSize = 4

	// Simulate compaction: token count drops
	v.recordIteration(1000, 0) // init
	v.recordIteration(3000, 1) // +2000 tokens
	v.recordIteration(5000, 2) // +2000 tokens
	v.recordIteration(2000, 2) // compaction: token count dropped, delta should be 0
	v.recordIteration(4000, 3) // +2000 tokens after compaction

	// Should not crash or produce spurious delta from compaction
	if v.records[2].tokenDelta != 0 {
		t.Errorf("compaction should produce 0 delta, got %d", v.records[2].tokenDelta)
	}
}
