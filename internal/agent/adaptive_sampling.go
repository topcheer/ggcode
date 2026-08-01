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
//  2. Uses the same tool trajectory classification as adaptive effort,
//     but maps phases to temperature values rather than reasoning budgets.
//  3. Applies temperature for exactly one LLM turn, then restores the
//     previous value — same ephemeral pattern as adaptive effort.
//  4. Uses conservative temperature values to avoid degrading code quality.

const (
	// adaptiveSamplingWindow controls how many recent tool interactions to
	// consider when classifying the current sampling context.
	adaptiveSamplingWindow = 6

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

// adaptiveSamplingState tracks recent tool interactions and recommends a
// temperature for the next LLM turn.
type adaptiveSamplingState struct {
	mu              sync.Mutex
	entries         []effortEntry // reuse effortEntry from adaptive_effort.go
	userOverrideSet bool          // true when user explicitly set temperature
}

func newAdaptiveSamplingState() *adaptiveSamplingState {
	return &adaptiveSamplingState{}
}

// recordToolResult appends a tool interaction to the sliding window.
// Reuses the same entry type as adaptive effort for consistency.
func (s *adaptiveSamplingState) recordToolResult(toolName string, isError bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = append(s.entries, effortEntry{toolName: toolName, isError: isError})
	if len(s.entries) > adaptiveSamplingWindow {
		s.entries = s.entries[len(s.entries)-adaptiveSamplingWindow:]
	}
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

// reset clears the window for a new user turn.
func (s *adaptiveSamplingState) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = s.entries[:0]
}

// classifyPhase analyzes recent tool interactions and returns the current
// task phase for temperature selection.
func (s *adaptiveSamplingState) classifyPhase() samplingPhase {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.entries) == 0 {
		return phaseNone
	}

	recentErrors := 0
	editCount := 0
	readOnlyCount := 0
	creativeCount := 0

	for _, e := range s.entries {
		if e.isError {
			recentErrors++
			continue
		}
		if editTools[e.toolName] {
			editCount++
		} else if creativeTools[e.toolName] {
			creativeCount++
		} else if effortReadOnlyTools[e.toolName] {
			readOnlyCount++
		}
	}

	// Priority: error recovery > code editing > creative > exploration
	total := len(s.entries)
	switch {
	case recentErrors >= 2:
		return phaseErrorRecovery
	case editCount > 0 && editCount >= total/3:
		return phaseCodeEdit
	case creativeCount > 0 && creativeCount >= total/2:
		return phaseCreative
	case readOnlyCount > 0 && readOnlyCount >= total/2:
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
