// Package agent provides the core agent implementation.
//
// Resource-Aware Agent Orchestration (2025-2026 concept):
// Dynamically adapt agent behavior based on system resource availability.
// This extends beyond token budget to include memory, CPU, and disk constraints.
// Research basis: AgentOrchestra (arXiv:2506.12508), AI Agent Index 2025.
package agent

import (
	"runtime"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

// ResourceMonitor tracks system resource usage and provides guidance
// for resource-aware agent orchestration.
type ResourceMonitor struct {
	mu              sync.Mutex
	lastCheck       time.Time
	memStats        runtime.MemStats
	availableMemory uint64
	totalMemory     uint64
	memoryPressure  ResourcePressureLevel
}

// ResourcePressureLevel indicates the current system resource stress.
type ResourcePressureLevel int

const (
	// PressureLow indicates normal resource availability.
	PressureLow ResourcePressureLevel = iota
	// PressureModerate indicates resources are under moderate stress.
	PressureModerate
	// PressureHigh indicates critical resource shortage.
	PressureHigh
)

// NewResourceMonitor creates a new resource monitor.
func NewResourceMonitor() *ResourceMonitor {
	rm := &ResourceMonitor{
		lastCheck: time.Now(),
	}
	rm.update()
	return rm
}

// Update refreshes resource statistics. Should be called periodically
// (e.g., before expensive operations or at regular intervals).
func (rm *ResourceMonitor) Update() {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	rm.update()
}

// update is the internal implementation, must be called with lock held.
func (rm *ResourceMonitor) update() {
	rm.lastCheck = time.Now()
	runtime.ReadMemStats(&rm.memStats)

	// Get system memory limits (Linux/macOS)
	rm.totalMemory = rm.memStats.Sys
	// Estimate available memory as heap allocable + spare
	rm.availableMemory = rm.memStats.HeapAlloc + rm.memStats.HeapIdle

	// Calculate memory pressure
	if rm.totalMemory == 0 {
		rm.memoryPressure = PressureLow
		return
	}

	// Memory usage ratio (avoid division by zero)
	usageRatio := float64(rm.memStats.Alloc) / float64(rm.totalMemory)

	// Pressure thresholds (conservative)
	if usageRatio < 0.50 {
		rm.memoryPressure = PressureLow
	} else if usageRatio < 0.75 {
		rm.memoryPressure = PressureModerate
	} else {
		rm.memoryPressure = PressureHigh
	}

	debug.Log("resource", "ResourceMonitor: pressure=%v alloc=%dMB sys=%dMB usage=%.2f",
		rm.memoryPressure, rm.memStats.Alloc>>20, rm.totalMemory>>20, usageRatio)
}

// PressureLevel returns the current resource pressure level.
func (rm *ResourceMonitor) PressureLevel() ResourcePressureLevel {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	return rm.memoryPressure
}

// ShouldLimitExpensiveOperations returns true if current resource pressure
// suggests avoiding expensive operations (e.g., large file reads, spawning
// subagents, heavy LLM calls with large context).
func (rm *ResourceMonitor) ShouldLimitExpensiveOperations() bool {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	return rm.memoryPressure == PressureHigh
}

// RecommendReduceParallelism returns true if resource pressure suggests
// reducing parallelism (e.g., tool execution, file reads).
func (rm *ResourceMonitor) RecommendReduceParallelism() bool {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	return rm.memoryPressure >= PressureModerate
}

// MemoryUsageMB returns current memory usage in MB.
func (rm *ResourceMonitor) MemoryUsageMB() uint64 {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	return rm.memStats.Alloc >> 20
}

// SystemMemoryMB returns total system memory in MB.
func (rm *ResourceMonitor) SystemMemoryMB() uint64 {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	return rm.totalMemory >> 20
}

// GCStats returns garbage collection statistics.
func (rm *ResourceMonitor) GCStats() (numGC uint32, pauseTotalNs uint64) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	return rm.memStats.NumGC, rm.memStats.PauseTotalNs
}

// ForceGC triggers an explicit garbage collection. Use sparingly.
func (rm *ResourceMonitor) ForceGC() {
	debug.Log("resource", "ResourceMonitor: forcing GC")
	runtime.GC()
	rm.update()
}

// String returns a human-readable description of current resource state.
func (rm *ResourceMonitor) String() string {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	pressureLabel := "LOW"
	switch rm.memoryPressure {
	case PressureModerate:
		pressureLabel = "MODERATE"
	case PressureHigh:
		pressureLabel = "HIGH"
	}

	return "ResourceMonitor: pressure=" + pressureLabel +
		" mem=" + formatMB(rm.memStats.Alloc) +
		"/" + formatMB(rm.totalMemory) +
		" gc=" + formatGC(rm.memStats.NumGC)
}

func formatMB(b uint64) string {
	mb := b >> 20
	if mb < 1024 {
		return string(rune('0'+mb/100)) + string(rune('0'+(mb/10)%10)) + string(rune('0'+mb%10)) + "MB"
	}
	gb := float64(mb) / 1024.0
	return string(rune('0'+int(gb))) + "." + string(rune('0'+int(gb*10)%10)) + "GB"
}

func formatGC(n uint32) string {
	if n < 1000 {
		return string(rune('0'+n/100)) + string(rune('0'+(n/10)%10)) + string(rune('0'+n%10))
	}
	return "1k+"
}
