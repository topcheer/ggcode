package agent

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/cost"
)

func TestCacheEffGate_NilTracker(t *testing.T) {
	g := newCacheEffGateState(nil)
	if s := g.maybeWarnCacheThrashing(); s != "" {
		t.Fatalf("expected empty for nil tracker, got %q", s)
	}
}

func TestCacheEffGate_NilGate(t *testing.T) {
	var g *cacheEffGateState
	if s := g.maybeWarnCacheThrashing(); s != "" {
		t.Fatalf("expected empty for nil gate, got %q", s)
	}
}

func TestCacheEffGate_NoCacheActivity(t *testing.T) {
	pricing := cost.PricingTable{
		"test": {"model": {Type: cost.PricingPerToken, InputPerM: 3.0, OutputPerM: 15.0}},
	}
	tr := cost.NewTracker("test", "model", pricing)
	g := newCacheEffGateState(tr)

	// No cache tokens recorded yet.
	if s := g.maybeWarnCacheThrashing(); s != "" {
		t.Fatalf("expected empty for no cache activity, got %q", s)
	}
}

func TestCacheEffGate_BelowThreshold(t *testing.T) {
	pricing := cost.PricingTable{
		"test": {"model": {Type: cost.PricingPerToken, InputPerM: 3.0, OutputPerM: 15.0}},
	}
	tr := cost.NewTracker("test", "model", pricing)
	g := newCacheEffGateState(tr)

	// Below minimum thresholds (5000 writes, 1000 reads).
	tr.Record(cost.TokenUsage{CacheRead: 500, CacheWrite: 6000})
	if s := g.maybeWarnCacheThrashing(); s != "" {
		t.Fatalf("expected empty below min thresholds, got %q", s)
	}
}

func TestCacheEffGate_GoodRatio(t *testing.T) {
	pricing := cost.PricingTable{
		"test": {"model": {Type: cost.PricingPerToken, InputPerM: 3.0, OutputPerM: 15.0}},
	}
	tr := cost.NewTracker("test", "model", pricing)
	g := newCacheEffGateState(tr)

	// Good ratio: more reads than writes (0.5x).
	tr.Record(cost.TokenUsage{CacheRead: 100000, CacheWrite: 50000})
	if s := g.maybeWarnCacheThrashing(); s != "" {
		t.Fatalf("expected empty for good ratio, got %q", s)
	}
}

func TestCacheEffGate_ThrashingDetected(t *testing.T) {
	pricing := cost.PricingTable{
		"test": {"model": {Type: cost.PricingPerToken, InputPerM: 3.0, OutputPerM: 15.0}},
	}
	tr := cost.NewTracker("test", "model", pricing)
	g := newCacheEffGateState(tr)

	// Severe thrashing: 15x writes vs reads, above min thresholds.
	tr.Record(cost.TokenUsage{CacheRead: 5000, CacheWrite: 75000})

	s := g.maybeWarnCacheThrashing()
	if s == "" {
		t.Fatal("expected thrashing warning, got empty")
	}
	if !strings.Contains(s, "Cache Thrashing") {
		t.Fatalf("expected 'Cache Thrashing' in warning, got %q", s)
	}
	if !strings.Contains(s, "Minimize changes") {
		t.Fatalf("expected guidance about minimizing changes, got %q", s)
	}
}

func TestCacheEffGate_WarnsOnce(t *testing.T) {
	pricing := cost.PricingTable{
		"test": {"model": {Type: cost.PricingPerToken, InputPerM: 3.0, OutputPerM: 15.0}},
	}
	tr := cost.NewTracker("test", "model", pricing)
	g := newCacheEffGateState(tr)

	tr.Record(cost.TokenUsage{CacheRead: 5000, CacheWrite: 75000})

	first := g.maybeWarnCacheThrashing()
	second := g.maybeWarnCacheThrashing()

	if first == "" {
		t.Fatal("expected first warning to be non-empty")
	}
	if second != "" {
		t.Fatal("expected second warning to be empty (warns once)")
	}
}

func TestCacheEffGate_Reset(t *testing.T) {
	pricing := cost.PricingTable{
		"test": {"model": {Type: cost.PricingPerToken, InputPerM: 3.0, OutputPerM: 15.0}},
	}
	tr := cost.NewTracker("test", "model", pricing)
	g := newCacheEffGateState(tr)

	tr.Record(cost.TokenUsage{CacheRead: 5000, CacheWrite: 75000})
	g.maybeWarnCacheThrashing() // Sets warned=true

	g.reset()

	// After reset, warning should fire again (but deltas are also reset,
	// so it uses cumulative ratio which is still > 10).
	s := g.maybeWarnCacheThrashing()
	if s == "" {
		t.Fatal("expected warning after reset")
	}
}

func TestCacheEffGate_IncrementalRatio(t *testing.T) {
	pricing := cost.PricingTable{
		"test": {"model": {Type: cost.PricingPerToken, InputPerM: 3.0, OutputPerM: 15.0}},
	}
	tr := cost.NewTracker("test", "model", pricing)
	g := newCacheEffGateState(tr)

	// First call: good ratio, above thresholds (establishes baseline).
	// After Record: CacheRead=10000, CacheWrite=5000
	tr.Record(cost.TokenUsage{CacheRead: 10000, CacheWrite: 5000})
	g.maybeWarnCacheThrashing() // No warning, sets lastRead=10000/lastWrite=5000

	// Second call: sudden thrashing. Record is additive, so we add
	// +1000 read and +20000 write. Cumulative: read=11000, write=25000.
	// Incremental delta: readDelta=1000, writeDelta=20000 -> ratio 20.
	tr.Record(cost.TokenUsage{CacheRead: 1000, CacheWrite: 20000})

	s := g.maybeWarnCacheThrashing()
	if s == "" {
		t.Fatal("expected incremental thrashing warning")
	}
	if !strings.Contains(s, "recent") {
		t.Fatalf("expected 'recent' label in warning, got %q", s)
	}
}
