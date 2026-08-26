package agent

import "testing"

func TestFutileCycle_BasicCycle(t *testing.T) {
	f := newFutileCycleState()

	// Epoch 1: read files A, B, C, D
	f.recordRead("src/a.go")
	f.recordRead("src/b.go")
	f.recordRead("src/c.go")
	f.recordRead("src/d.go")

	// Write finalizes epoch 1
	f.recordWrite()

	// Epoch 2: read the same files again without writing
	f.recordRead("src/a.go")
	f.recordRead("src/b.go")
	f.recordRead("src/c.go")
	f.recordRead("src/d.go")

	msg := f.maybeWarn(5)
	if msg == "" {
		t.Fatal("expected futile cycle warning, got empty")
	}
}

func TestFutileCycle_WriteBreaksCycle(t *testing.T) {
	f := newFutileCycleState()

	// Epoch 1
	f.recordRead("src/a.go")
	f.recordRead("src/b.go")
	f.recordRead("src/c.go")
	f.recordWrite()

	// Epoch 2: read different files
	f.recordRead("src/d.go")
	f.recordRead("src/e.go")
	f.recordRead("src/f.go")
	f.recordWrite()

	// Epoch 3: read epoch 1 files again (but write happened in between)
	f.recordRead("src/a.go")
	f.recordRead("src/b.go")
	f.recordRead("src/c.go")

	// Should still detect overlap since no write in this epoch yet
	msg := f.maybeWarn(8)
	if msg == "" {
		t.Fatal("expected warning for revisiting epoch 1 read set")
	}
}

func TestFutileCycle_NoCycle(t *testing.T) {
	f := newFutileCycleState()

	// Epoch 1
	f.recordRead("src/a.go")
	f.recordRead("src/b.go")
	f.recordRead("src/c.go")
	f.recordWrite()

	// Epoch 2: completely different files
	f.recordRead("src/d.go")
	f.recordRead("src/e.go")
	f.recordRead("src/f.go")

	msg := f.maybeWarn(5)
	if msg != "" {
		t.Fatalf("expected no warning for non-overlapping read sets, got: %s", msg)
	}
}

func TestFutileCycle_MinReadSetNotMet(t *testing.T) {
	f := newFutileCycleState()

	// Only 2 files - below futileMinReadSet threshold
	f.recordRead("src/a.go")
	f.recordRead("src/b.go")
	f.recordWrite()

	f.recordRead("src/a.go")
	f.recordRead("src/b.go")

	msg := f.maybeWarn(5)
	if msg != "" {
		t.Fatalf("expected no warning for small read set, got: %s", msg)
	}
}

func TestFutileCycle_MaxWarnings(t *testing.T) {
	f := newFutileCycleState()

	// Epoch 1
	for i := 0; i < 5; i++ {
		f.recordRead("src/file" + string(rune('a'+i)) + ".go")
	}
	f.recordWrite()

	// Epoch 2: same files
	for i := 0; i < 5; i++ {
		f.recordRead("src/file" + string(rune('a'+i)) + ".go")
	}

	msg1 := f.maybeWarn(5)
	if msg1 == "" {
		t.Fatal("expected first warning")
	}

	// Second epoch: suppressed (1 per run, batch 2 guidance-noise cleanup;
	// fresh warning only after compaction resets the counter).
	f.recordWrite()
	for i := 0; i < 5; i++ {
		f.recordRead("src/file" + string(rune('a'+i)) + ".go")
	}

	msg2 := f.maybeWarn(10)
	if msg2 != "" {
		t.Fatalf("expected second warning to be suppressed, got: %s", msg2)
	}

	// Third should be suppressed (max 2)
	f.recordWrite()
	for i := 0; i < 5; i++ {
		f.recordRead("src/file" + string(rune('a'+i)) + ".go")
	}
	msg3 := f.maybeWarn(15)
	if msg3 != "" {
		t.Fatalf("expected third warning to be suppressed, got: %s", msg3)
	}
}

func TestFutileCycle_PartialOverlap(t *testing.T) {
	f := newFutileCycleState()

	// Epoch 1: 5 files
	f.recordRead("src/a.go")
	f.recordRead("src/b.go")
	f.recordRead("src/c.go")
	f.recordRead("src/d.go")
	f.recordRead("src/e.go")
	f.recordWrite()

	// Epoch 2: 3 of 5 same files, plus 2 new -- 3 shared out of 7 union = 0.43 < 0.7
	f.recordRead("src/a.go")
	f.recordRead("src/b.go")
	f.recordRead("src/c.go")
	f.recordRead("src/x.go")
	f.recordRead("src/y.go")

	msg := f.maybeWarn(5)
	if msg != "" {
		t.Fatalf("expected no warning for partial overlap (0.43 < 0.7), got: %s", msg)
	}
}

func TestFutileCycle_HighOverlap(t *testing.T) {
	f := newFutileCycleState()

	// Epoch 1: 5 files
	f.recordRead("src/a.go")
	f.recordRead("src/b.go")
	f.recordRead("src/c.go")
	f.recordRead("src/d.go")
	f.recordRead("src/e.go")
	f.recordWrite()

	// Epoch 2: same 5 files + 1 new -- Jaccard = 5/6 = 0.83 >= 0.7
	f.recordRead("src/a.go")
	f.recordRead("src/b.go")
	f.recordRead("src/c.go")
	f.recordRead("src/d.go")
	f.recordRead("src/e.go")
	f.recordRead("src/f.go")

	msg := f.maybeWarn(5)
	if msg == "" {
		t.Fatal("expected warning for high overlap (0.83 >= 0.7)")
	}
}

func TestFutileCycle_Reset(t *testing.T) {
	f := newFutileCycleState()

	f.recordRead("src/a.go")
	f.recordRead("src/b.go")
	f.recordRead("src/c.go")
	f.recordWrite()
	f.recordRead("src/a.go")
	f.recordRead("src/b.go")
	f.recordRead("src/c.go")

	_ = f.maybeWarn(5)
	if f.warningsFired != 1 {
		t.Fatalf("expected 1 warning fired, got %d", f.warningsFired)
	}

	f.reset()
	if f.warningsFired != 0 {
		t.Fatal("warningsFired not reset")
	}
	if len(f.currentEpoch) != 0 {
		t.Fatal("currentEpoch not reset")
	}
	if len(f.pastEpochs) != 0 {
		t.Fatal("pastEpochs not reset")
	}
}

func TestFutileCycle_NoPastEpochs(t *testing.T) {
	f := newFutileCycleState()
	f.recordRead("src/a.go")
	f.recordRead("src/b.go")
	f.recordRead("src/c.go")

	msg := f.maybeWarn(3)
	if msg != "" {
		t.Fatalf("expected no warning without past epochs, got: %s", msg)
	}
}

func TestJaccard(t *testing.T) {
	tests := []struct {
		a, b map[string]bool
		want float64
	}{
		{map[string]bool{"a": true, "b": true}, map[string]bool{"a": true, "b": true}, 1.0},
		{map[string]bool{"a": true}, map[string]bool{"b": true}, 0.0},
		{map[string]bool{"a": true, "b": true}, map[string]bool{"a": true}, 0.5},
		{map[string]bool{}, map[string]bool{}, 0.0},
	}
	for _, tt := range tests {
		got := futileJaccard(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("Jaccard(%v, %v) = %.2f, want %.2f", tt.a, tt.b, got, tt.want)
		}
	}
}
