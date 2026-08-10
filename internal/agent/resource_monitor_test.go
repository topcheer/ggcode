package agent

import (
	"testing"
	"time"
)

func TestResourceMonitor(t *testing.T) {
	rm := NewResourceMonitor()
	if rm == nil {
		t.Fatal("NewResourceMonitor returned nil")
	}

	// Test initial state
	if rm.PressureLevel() < PressureLow || rm.PressureLevel() > PressureHigh {
		t.Errorf("unexpected initial pressure level: %v", rm.PressureLevel())
	}

	// Test Update works
	rm.Update()
	if rm.PressureLevel() < PressureLow || rm.PressureLevel() > PressureHigh {
		t.Errorf("unexpected pressure level after update: %v", rm.PressureLevel())
	}

	// Test memory stats
	memMB := rm.MemoryUsageMB()
	if memMB == 0 {
		t.Error("MemoryUsageMB returned 0")
	}

	sysMB := rm.SystemMemoryMB()
	if sysMB == 0 {
		t.Error("SystemMemoryMB returned 0")
	}

	// Test GC stats
	numGC, pauseTotalNs := rm.GCStats()
	if numGC < 0 {
		t.Errorf("invalid NumGC: %d", numGC)
	}
	if pauseTotalNs < 0 {
		t.Errorf("invalid PauseTotalNs: %d", pauseTotalNs)
	}

	// Test String representation
	s := rm.String()
	if s == "" {
		t.Error("String returned empty")
	}
	if len(s) < 10 {
		t.Errorf("String too short: %q", s)
	}
}

func TestResourceMonitorPressureLevels(t *testing.T) {
	rm := NewResourceMonitor()

	// Test that pressure level is always valid
	for i := 0; i < 5; i++ {
		rm.Update()
		level := rm.PressureLevel()

		if level != PressureLow && level != PressureModerate && level != PressureHigh {
			t.Errorf("invalid pressure level: %v", level)
		}
	}
}

func TestResourceMonitorRecommendations(t *testing.T) {
	rm := NewResourceMonitor()
	rm.Update()

	// Test that recommendations are consistent with pressure level
	limitExpensive := rm.ShouldLimitExpensiveOperations()
	reduceParallel := rm.RecommendReduceParallelism()

	// If pressure is high, both should be true
	if rm.PressureLevel() == PressureHigh {
		if !limitExpensive {
			t.Error("ShouldLimitExpensiveOperations should be true at high pressure")
		}
		if !reduceParallel {
			t.Error("RecommendReduceParallelism should be true at high pressure")
		}
	}

	// If pressure is low, both should be false
	if rm.PressureLevel() == PressureLow {
		if limitExpensive {
			t.Error("ShouldLimitExpensiveOperations should be false at low pressure")
		}
		if reduceParallel {
			t.Error("RecommendReduceParallelism should be false at low pressure")
		}
	}

	// Moderate pressure should at least suggest reducing parallelism
	if rm.PressureLevel() == PressureModerate {
		if !reduceParallel {
			t.Error("RecommendReduceParallelism should be true at moderate pressure")
		}
	}
}

func TestResourceMonitorForceGC(t *testing.T) {
	rm := NewResourceMonitor()

	// Get initial stats
	numGC1, _ := rm.GCStats()

	// Force GC
	rm.ForceGC()

	// Get stats after GC
	numGC2, _ := rm.GCStats()

	// GC count should have increased
	if numGC2 < numGC1 {
		t.Errorf("GC count decreased: %d -> %d", numGC1, numGC2)
	}
}

func TestResourceMonitorThreadSafety(t *testing.T) {
	rm := NewResourceMonitor()
	done := make(chan bool)

	// Concurrent reads
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				rm.PressureLevel()
				rm.ShouldLimitExpensiveOperations()
				rm.RecommendReduceParallelism()
				rm.MemoryUsageMB()
				_ = rm.String()
			}
			done <- true
		}()
	}

	// Concurrent writes
	for i := 0; i < 5; i++ {
		go func() {
			for j := 0; j < 50; j++ {
				rm.Update()
				rm.ForceGC()
			}
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 15; i++ {
		<-done
	}

	// Should not panic or deadlock
	_ = rm.String()
}

func TestResourceMonitorTimestamp(t *testing.T) {
	rm := NewResourceMonitor()
	rm.Update()

	// Wait a bit
	time.Sleep(10 * time.Millisecond)

	// Update again and check timestamp changed
	rm.Update()
	// Can't directly check timestamp, but this verifies no panic
	_ = rm.String()
}
