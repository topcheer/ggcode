package agentruntime

import (
	"math/rand"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/memory"
)

// newSeededRand gives tests a deterministic jitter source.
func newSeededRand(seed int64) *rand.Rand { return rand.New(rand.NewSource(seed)) }

// tTempJoin builds a temp-dir path (test helper).
func tTempJoin(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join(t.TempDir(), name)
}

// #5519 acceptance: startup consolidation skip within 6h, weekly sweep
// jitter bounds, single slot winner across 12 same-instant instances.

// ---- 1. startup consolidation skip ---------------------------------------

func TestShouldSkipStartupConsolidation(t *testing.T) {
	now := time.Date(2026, 11, 12, 22, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		last time.Time
		skip bool
	}{
		{"fresh run 2h ago skips", now.Add(-2 * time.Hour), true},
		{"just under window 5h59m skips", now.Add(-5*time.Hour - 59*time.Minute), true},
		{"past window 7h rescans", now.Add(-7 * time.Hour), false},
		{"zero (never ran) rescans", time.Time{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			skip, since := shouldSkipStartupConsolidation(tc.last, now)
			if skip != tc.skip {
				t.Fatalf("skip=%v want %v", skip, tc.skip)
			}
			if !tc.last.IsZero() && since < 0 {
				t.Fatalf("since must be non-negative, got %v", since)
			}
		})
	}
}

// end-to-end: Consolidate persists LastRun; LastConsolidationRun surfaces it.
func TestLastConsolidationRun_Persisted(t *testing.T) {
	dir := t.TempDir()
	am := memory.NewAutoMemoryAt(dir)
	if last := am.LastConsolidationRun(); !last.IsZero() {
		t.Fatalf("fresh store must report zero LastRun, got %v", last)
	}
	am.Consolidate(dir)
	last := am.LastConsolidationRun()
	if last.IsZero() {
		t.Fatal("after Consolidate, LastConsolidationRun must be non-zero")
	}
	if age := time.Since(last); age > time.Minute {
		t.Fatalf("LastRun should be ~now, age %v", age)
	}
	// the startup gate consumes exactly this value
	if skip, _ := shouldSkipStartupConsolidation(last, time.Now()); !skip {
		t.Fatal("a run recorded now must gate a same-instant boot to skip")
	}
}

// ---- 2. jitter bounds -------------------------------------------------------

func TestSweepJitterDuration_Bounds(t *testing.T) {
	restoreWindow := sweepJitterWindow
	restoreRand := sweepJitterRand
	defer func() {
		sweepJitterWindow = restoreWindow
		sweepJitterRand = restoreRand
	}()
	sweepJitterWindow = 30 * time.Minute
	sweepJitterRand = newSeededRand(5519)

	sawNonZero, sawNearCeiling := false, false
	for i := 0; i < 2000; i++ {
		d := SweepJitterDuration()
		if d < 0 || d > sweepJitterWindow {
			t.Fatalf("jitter %v out of [0,%v]", d, sweepJitterWindow)
		}
		if d > 0 {
			sawNonZero = true
		}
		if d > 25*time.Minute {
			sawNearCeiling = true
		}
	}
	if !sawNonZero {
		t.Fatal("jitter must not collapse to constant zero")
	}
	if !sawNearCeiling {
		t.Fatal("jitter ceiling must be reachable (uniform, not half-window)")
	}
}

// ---- 3. single slot winner across 12 same-instant instances ------------------

func TestSweepSlot_SingleWinnerAcross12Instances(t *testing.T) {
	statePath := tTempJoin(t, "sweep-state.json")
	now := time.Now()

	var mu sync.Mutex
	var releaseWinner func()
	winners, losers := 0, 0
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, held, err := acquireSweepSlot(statePath, func() time.Time { return now })
			if err != nil {
				t.Errorf("acquire: %v", err)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			if held {
				losers++
				return
			}
			winners++
			releaseWinner = release // hold the slot until all 12 contended
		}()
	}
	wg.Wait()
	if winners != 1 {
		t.Fatalf("exactly 1 instance must win the slot, got %d (losers %d)", winners, losers)
	}
	if losers != 11 {
		t.Fatalf("11 instances must defer silently, got %d", losers)
	}
	// release the winner, then the slot must be claimable again
	if releaseWinner != nil {
		releaseWinner()
	}
	if _, held, err := acquireSweepSlot(statePath, func() time.Time { return now.Add(time.Minute) }); err != nil || held {
		t.Fatalf("post-release acquire must proceed: held=%v err=%v", held, err)
	}
}
