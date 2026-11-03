package agent

import (
	"github.com/topcheer/ggcode/internal/debug"
)

const cascadeMinLowBatches = 2

type modelCascadeState struct {
	consecLowBatches int
	batchTools       []string
	batchHasErr      bool
	batchOpen        bool
}

func (s *modelCascadeState) beginBatch() {
	if s.batchOpen {
		s.sealBatch()
	}
	s.batchTools = s.batchTools[:0]
	s.batchHasErr = false
	s.batchOpen = true
}

func (s *modelCascadeState) notePlanned(name string) {
	if !s.batchOpen {
		return
	}
	s.batchTools = append(s.batchTools, name)
}

func (s *modelCascadeState) noteError(isError bool) {
	if !s.batchOpen || !isError {
		return
	}
	s.batchHasErr = true
}

func (s *modelCascadeState) sealBatch() {
	s.batchOpen = false
	if len(s.batchTools) == 0 {
		return
	}
	if s.batchHasErr {
		s.consecLowBatches = 0
		return
	}
	for _, n := range s.batchTools {
		if !effortReadOnlyTools[n] {
			s.consecLowBatches = 0
			return
		}
	}
	s.consecLowBatches++
}

func (s *modelCascadeState) resetBatches() {
	s.consecLowBatches = 0
	s.batchTools = nil
	s.batchHasErr = false
	s.batchOpen = false
}

func (s *modelCascadeState) shouldCascade() bool {
	return s.consecLowBatches >= cascadeMinLowBatches
}

func (a *Agent) applyModelCascade() bool {
	if a.auxResolved == nil {
		return false
	}
	if !a.cascade.shouldCascade() {
		return false
	}
	aux := a.auxProviderFor()
	if aux == nil || aux == a.provider {
		return false
	}
	a.cascadeSavedProvider = a.provider
	a.provider = aux
	debug.Log("model-cascade", "exploratory turn routed to aux model %q (streak=%d)",
		a.auxModelName, a.cascade.consecLowBatches)
	return true
}

func (a *Agent) restoreModelCascade() {
	if a.cascadeSavedProvider == nil {
		return
	}
	a.provider = a.cascadeSavedProvider
	a.cascadeSavedProvider = nil
	debug.Log("model-cascade", "restored main model provider")
}
