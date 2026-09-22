package agent

import (
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

// Adaptive Sampling Controller
//
// Research basis: Temperature is one of the most underutilized inference
// parameters in coding agents. Claude Code, Cursor, Aider, and Codex CLI all
// use a fixed temperature (or provider default) throughout an entire session.
// However, the optimal temperature varies significantly by task phase:
//
//   - Exploration/planning: slightly higher temperature (0.3-0.5) encourages
//     creative exploration and diverse hypotheses about the codebase.
//   - Code editing: low temperature (0.0-0.2) for deterministic, precise edits
//     that match existing patterns exactly.
//   - Error recovery: very low temperature (0.0) to avoid compounding errors
//     with creative but wrong guesses.
//   - Creative writing (docs, commit messages): moderate temperature (0.4-0.6)
//     for natural language fluency.
//
// This controller uses the same sliding-window approach as adaptive_effort.go
// but targets the SamplingConfigProvider interface (temperature + top_p).
// It is complementary to adaptive effort: effort controls reasoning depth,
// sampling controls output diversity.
//
// Key design decisions:
//  1. Only activates when the user has NOT explicitly set temperature.
//  2. Reads the SHARED unified task-phase monitor (task_phase.go) — the
//     same window and signals as adaptive effort — and maps phases to
//     temperature values rather than reasoning budgets.
//  3. Applies temperature for exactly one LLM turn, then restores the
//     previous value — same ephemeral pattern as adaptive effort.
//  4. Uses conservative temperature values to avoid degrading code quality.

// Window sizing lives in the unified task-phase monitor (task_phase.go:
// taskPhaseWindowSize) — effort and sampling share one window so their
// phase views cannot diverge.

const (
	// Temperature presets by task phase. These are conservative values that
	// improve output quality without introducing randomness in code edits.
	tempExploration  = 0.4 // diverse exploration, brainstorming
	tempCodeEdit     = 0.1 // precise, deterministic edits
	tempErrorRecover = 0.0 // maximum determinism for error recovery
	tempCreative     = 0.5 // docs, commit messages, natural language
)

// samplingPhase classifies the current task phase for temperature selection.
type samplingPhase int

const (
	phaseNone          samplingPhase = iota // no data — don't adjust
	phaseExploration                        // reads, searches only
	phaseCodeEdit                           // file edits in recent history
	phaseErrorRecovery                      // recent errors, edit retries
	phaseCreative                           // git_commit, write_file for docs
)

// creativeTools are tools that produce natural language content — moderate
// temperature improves fluency without affecting code correctness.
var creativeTools = map[string]bool{
	"git_commit":  true,
	"cron_create": true,
}

// adaptiveSamplingState recommends a temperature for the next LLM turn from
// the shared unified task-phase monitor (task_phase.go).
type adaptiveSamplingState struct {
	mu              sync.Mutex       // guards userOverrideSet
	window          *taskPhaseWindow // unified monitor, shared with adaptive effort
	userOverrideSet bool             // true when user explicitly set temperature
}

func newAdaptiveSamplingState(w *taskPhaseWindow) *adaptiveSamplingState {
	if w == nil {
		w = newTaskPhaseWindow()
	}
	return &adaptiveSamplingState{window: w}
}

// recordToolResult appends a tool interaction to the unified monitor.
func (s *adaptiveSamplingState) recordToolResult(toolName string, isError bool) {
	s.window.record(toolName, isError, "")
}

// recordToolResultErr passes the failed call's error text so the unified
// monitor's param-format retry filter applies to temperature classification
// too (#1436-A/#1836 parity: the sampling-side classifier predated those
// fixes and counted innocuous read-only/param failures as recovery signals,
// forcing max-determinism temperature after harmless misses).
func (s *adaptiveSamplingState) recordToolResultErr(toolName string, isError bool, errText string) {
	s.window.record(toolName, isError, errText)
}

// setUserOverride marks that the user has explicitly set temperature — the
// adapter should stay dormant.
func (s *adaptiveSamplingState) setUserOverride(set bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.userOverrideSet = set
}

// hasUserOverride returns whether the user has explicitly set temperature.
func (s *adaptiveSamplingState) hasUserOverride() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.userOverrideSet
}

// reset clears the unified monitor for a new user turn.
func (s *adaptiveSamplingState) reset() {
	s.window.reset()
}

// classifyPhase analyzes the unified monitor's canonical signals and returns
// the current task phase for temperature selection.
func (s *adaptiveSamplingState) classifyPhase() samplingPhase {
	if s.window == nil {
		return phaseNone
	}
	sig := s.window.signals()
	if sig.Total == 0 {
		return phaseNone
	}

	// Priority: error recovery > code editing > creative > exploration.
	// Thresholds are deliberately more conservative than the effort
	// policy's (2+ filtered errors; ratio-based phase dominance).
	switch {
	case sig.RecentErrors >= 2:
		return phaseErrorRecovery
	case sig.EditCount > 0 && sig.EditCount >= sig.Total/3:
		return phaseCodeEdit
	case sig.CreativeCount > 0 && sig.CreativeCount >= sig.Total/2:
		return phaseCreative
	case sig.ReadOnlyCount > 0 && sig.ReadOnlyCount >= sig.Total/2:
		return phaseExploration
	default:
		return phaseNone
	}
}

// recommendedTemperature returns the recommended temperature for the next
// LLM turn, or -1 if no adaptation is needed.
func (s *adaptiveSamplingState) recommendedTemperature() float64 {
	if s.hasUserOverride() {
		return -1
	}

	phase := s.classifyPhase()
	switch phase {
	case phaseExploration:
		return tempExploration
	case phaseCodeEdit:
		return tempCodeEdit
	case phaseErrorRecovery:
		return tempErrorRecover
	case phaseCreative:
		return tempCreative
	default:
		return -1
	}
}

// applyAdaptiveSampling checks whether adaptive sampling should override the
// provider's current temperature for this turn, and applies it if so.
// Returns the temperature that was applied (or -1 if no change was made) and
// the previous temperature so it can be restored after the call.
//
// This is called before each streamChatResponse in the agent loop, alongside
// applyAdaptiveEffort.
func (a *Agent) applyAdaptiveSampling() (applied float64, previous float64) {
	if a.adaptiveSampling == nil {
		return -1, 0
	}
	if a.adaptiveSampling.hasUserOverride() {
		return -1, 0
	}

	recommended := a.adaptiveSampling.recommendedTemperature()
	if recommended < 0 {
		return -1, 0
	}

	// Get the provider's current temperature so we can restore it.
	p, ok := a.provider.(provider.SamplingConfigProvider)
	if !ok {
		return -1, 0
	}
	previous = p.Temperature()

	// Only apply if the recommendation differs from the current setting.
	// A difference of < 0.05 is not worth the API request overhead.
	diff := recommended - previous
	if diff < 0 {
		diff = -diff
	}
	if diff < 0.05 {
		return -1, previous
	}

	p.SetTemperature(recommended)
	debug.Log("adaptive-sampling", "adjusted temperature: %.2f -> %.2f (phase=%d)", previous, recommended, a.adaptiveSampling.classifyPhase())
	return recommended, previous
}

// restoreSampling restores the provider's temperature to a previous value
// after an adaptive adjustment. A previous value of 0 means "provider default"
// which is the correct restore semantics since 0 = unset.
func (a *Agent) restoreSampling(previous float64) {
	p, ok := a.provider.(provider.SamplingConfigProvider)
	if !ok {
		return
	}
	p.SetTemperature(previous)
	debug.Log("adaptive-sampling", "restored temperature to: %.2f", previous)
}
