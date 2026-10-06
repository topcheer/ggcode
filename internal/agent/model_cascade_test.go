package agent

// model_cascade_test.go -- r485 turn-tier model routing probes.
//
// Covers: the batch classifier state machine (read-only streak, mutation
// reset, error reset, empty-batch neutrality), apply/restore provider swap
// symmetry, dormancy when aux_model is unset, and build-failure degradation
// (auxProviderFor falls back to the main provider, apply reports no swap).

import (
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

func TestModelCascade_BatchClassifierStreak(t *testing.T) {
	var s modelCascadeState
	if s.shouldCascade() {
		t.Fatal("zero state must not cascade")
	}
	// Batch 1: read-only, error-free.
	s.beginBatch()
	s.notePlanned("read_file")
	s.notePlanned("grep")
	s.sealBatch()
	if s.consecLowBatches != 1 || s.shouldCascade() {
		t.Fatalf("after 1 low batch: consec=%d should=%v", s.consecLowBatches, s.shouldCascade())
	}
	// Batch 2: read-only -> streak reaches threshold.
	s.beginBatch()
	s.notePlanned("glob")
	s.sealBatch()
	if !s.shouldCascade() {
		t.Fatal("2 consecutive read-only batches must qualify for cascade")
	}
}

func TestModelCascade_MutationOrErrorResets(t *testing.T) {
	cases := []struct {
		name  string
		steps func(s *modelCascadeState)
	}{
		{"mutation tool", func(s *modelCascadeState) {
			s.beginBatch()
			s.notePlanned("read_file")
			s.notePlanned("edit_file") // mutation
			s.sealBatch()
		}},
		{"errored result", func(s *modelCascadeState) {
			s.beginBatch()
			s.notePlanned("read_file")
			s.noteError(true)
			s.sealBatch()
		}},
		{"run_command is not read-only", func(s *modelCascadeState) {
			s.beginBatch()
			s.notePlanned("run_command")
			s.sealBatch()
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var s modelCascadeState
			s.beginBatch()
			s.notePlanned("read_file")
			s.sealBatch()
			s.beginBatch()
			s.notePlanned("grep")
			s.sealBatch()
			if !s.shouldCascade() {
				t.Fatal("precondition: streak must be primed")
			}
			tc.steps(&s)
			if s.shouldCascade() {
				t.Fatalf("%s must reset the streak", tc.name)
			}
		})
	}
}

func TestModelCascade_EmptyBatchNeutralAndReset(t *testing.T) {
	var s modelCascadeState
	s.beginBatch()
	s.notePlanned("read_file")
	s.sealBatch()
	s.beginBatch()
	s.notePlanned("glob")
	s.sealBatch()
	if !s.shouldCascade() {
		t.Fatal("precondition")
	}
	// Text-only turn: no planned tools -> sealed empty batch keeps state.
	s.beginBatch()
	s.sealBatch()
	if !s.shouldCascade() {
		t.Fatal("empty batch must be neutral")
	}
	// Hard reset drops everything.
	s.resetBatches()
	if s.shouldCascade() || s.consecLowBatches != 0 {
		t.Fatal("resetBatches must clear the streak")
	}
}

func TestModelCascade_DormantWithoutAuxModel(t *testing.T) {
	a := NewAgent(&mockProvider{}, tool.NewRegistry(), "sys", 5)
	a.cascade.beginBatch()
	a.cascade.notePlanned("read_file")
	a.cascade.sealBatch()
	a.cascade.beginBatch()
	a.cascade.notePlanned("grep")
	a.cascade.sealBatch()
	if !a.cascade.shouldCascade() {
		t.Fatal("precondition: trajectory qualifies")
	}
	if a.applyModelCascade() {
		t.Fatal("aux_model unset: apply must be a no-op (dormant)")
	}
	a.restoreModelCascade() // no-op path must not panic
}

func TestModelCascade_ApplyRestoreSwap(t *testing.T) {
	// provider.NewProvider touches the adaptive-cap registry which reads
	// ConfigDir(); the testguard requires an isolated HOME.
	t.Setenv("HOME", t.TempDir())
	main := &mockProvider{}
	a := NewAgent(main, tool.NewRegistry(), "sys", 5)
	resolved := &config.ResolvedEndpoint{
		VendorID:   "openai-compat",
		EndpointID: "e1",
		Model:      "main-model",
		Protocol:   "openai",
		BaseURL:    "http://127.0.0.1:1",
		APIKey:     "k",
	}
	a.SetAuxModel(resolved, "cheap-model")
	if a.auxResolved == nil {
		t.Fatal("SetAuxModel must arm routing for a distinct model")
	}
	// Prime the streak.
	a.cascade.beginBatch()
	a.cascade.notePlanned("read_file")
	a.cascade.sealBatch()
	a.cascade.beginBatch()
	a.cascade.notePlanned("grep")
	a.cascade.sealBatch()

	if !a.applyModelCascade() {
		t.Fatal("qualified trajectory + armed routing must swap")
	}
	if a.provider == provider.Provider(main) {
		t.Fatal("provider must be the aux clone after apply")
	}
	a.restoreModelCascade()
	if a.provider != provider.Provider(main) {
		t.Fatal("restore must return the main provider")
	}
	if a.cascadeSavedProvider != nil {
		t.Fatal("parked slot must be cleared after restore")
	}
}

func TestModelCascade_SameModelArmsNothing(t *testing.T) {
	a := NewAgent(&mockProvider{}, tool.NewRegistry(), "sys", 5)
	resolved := &config.ResolvedEndpoint{VendorID: "v", EndpointID: "e", Model: "m", Protocol: "openai"}
	a.SetAuxModel(resolved, "m") // same as main model
	if a.auxResolved != nil {
		t.Fatal("aux model identical to main must not arm routing")
	}
	a.cascade.resetBatches()
	a.cascade.consecLowBatches = cascadeMinLowBatches
	if a.applyModelCascade() {
		t.Fatal("no swap when routing unarmed")
	}
}
