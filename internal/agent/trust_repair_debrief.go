package agent

// Trust Repair Debrief — deterministic, user-facing failure debrief.
//
// Research basis (sa-70 gap ruling, 2026-10-07):
//   - ICIS 2025 trust-repair experiments (via zylos.ai, "Agent-Human Trust
//     Calibration", 2026-05-27): after an agent error, local explanation and
//     COUNTERFACTUAL options ("if X had been different, the outcome would have
//     been Y") restore user trust as effectively as apologies — users care
//     about understanding the failure mode and its boundary, not social
//     contrition. Clarification questions alone did NOT repair trust.
//   - Anthropic autonomy measurement (late 2025): on complex tasks agents ask
//     for clarification in 16.4% of turns vs humans interrupting in 7.1% —
//     the post-failure moment is exactly when a legible failure narrative
//     matters most, yet that is when model-narrated summaries are least
//     credible (the model just failed; its self-summary is the least
//     trustworthy signal in the system).
//
// Gap (NEW_GAP per sa-70 ruling): every existing failure-narrative mechanism
// in ggcode is MODEL-facing guidance injected via contextManager:
//   - attempt_brief.go — "summarising what was tried and why it failed — so
//     the agent pivots" (model-facing by its own doc comment)
//   - causal_attribution.go — model-facing CRS guidance
//   - phantom_verify.go / read_validity_check.go — model-facing detectors
// None of these signals reach the USER as a structured counterfactual +
// failure-boundary debrief. This file composes that debrief from ledgers the
// agent already maintains, with zero LLM cost — mirroring the deterministic
// detector philosophy (autonomyDial.suggest / oversightTriage.digest).
//
// Design:
//   - trustRepairState records every tool result (tool, path, error) during
//     the run, fed from the tool-result loop next to autonomyDial.record.
//   - maybeEmitTrustRepairDebrief fires ONCE per run on the user-facing
//     StreamEventSystem channel (same defer block as autonomyDial.suggest)
//     when the run had >=2 failed tool calls or an error streak >=5.
//   - Three sections (each deterministic):
//     1. Root cause — where the dominant error streak started.
//     2. Counterfactual — same-path tool contrast: had the failed step used
//        the tool/step that later succeeded on the same path, the outcome
//        would have differed.
//     3. Boundary — compounded trajectory reliability (0.85^N from the
//     compounded-uncertainty ledger) framing how much to trust conclusions.
//   - Reset per user turn, same discipline as every other detector.

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

const (
	// trustRepairMinFailures: a run needs at least this many failed tool
	// calls before a debrief is worth the user's attention.
	trustRepairMinFailures = 2

	// trustRepairStreakTrigger: an error streak of this length alone
	// justifies a debrief even with fewer total failures (repeated failure
	// on the same approach is the classic trust-damaging pattern).
	trustRepairStreakTrigger = 5

	// trustRepairErrBriefMax caps the quoted error snippet length.
	trustRepairErrBriefMax = 120
)

// trustAttempt is one observed tool outcome relevant to failure narration.
type trustAttempt struct {
	Step     int // 1-based iteration number
	Tool     string
	Path     string // file_path / path / pattern argument, when present
	IsError  bool
	ErrBrief string
}

// trustRepairState accumulates the failure ledger for one run.
type trustRepairState struct {
	attempts       []trustAttempt
	failedCalls    int
	errStreak      int
	maxErrStreak   int
	maxStreakStart int // Step where the dominant error streak began
	fired          bool
}

func newTrustRepairState() *trustRepairState {
	return &trustRepairState{}
}

func (s *trustRepairState) reset() {
	s.attempts = nil
	s.failedCalls = 0
	s.errStreak = 0
	s.maxErrStreak = 0
	s.maxStreakStart = 0
	s.fired = false
}

// trustRepairPathOf extracts a human-meaningful target path from common tool
// argument shapes. Returns "" when the call has no path-like argument.
func trustRepairPathOf(arguments string) string {
	if arguments == "" {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(arguments), &m); err != nil {
		return ""
	}
	for _, key := range []string{"file_path", "path", "pattern", "directory"} {
		if v, ok := m[key].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

// trustRepairBriefOf condenses an error result to a single quotable line.
func trustRepairBriefOf(content string) string {
	line := strings.TrimSpace(content)
	if idx := strings.IndexAny(line, "\n\r"); idx >= 0 {
		line = line[:idx]
	}
	line = strings.TrimSpace(line)
	if len(line) > trustRepairErrBriefMax {
		line = line[:trustRepairErrBriefMax] + "..."
	}
	return line
}

// trustRepairEvent groups one tool outcome for recording
// (single-parameter discipline, SonarQube S107).
type trustRepairEvent struct {
	Step      int // 1-based iteration number
	Tool      string
	Arguments string
	IsError   bool
	Content   string // tool result content (error brief extracted inside)
}

// record observes one tool outcome. Called from the tool-result loop next to
// autonomyDial.record.
func (s *trustRepairState) record(e trustRepairEvent) {
	if s == nil {
		return
	}
	a := trustAttempt{Step: e.Step, Tool: e.Tool, Path: trustRepairPathOf(e.Arguments), IsError: e.IsError}
	if e.IsError {
		a.ErrBrief = trustRepairBriefOf(e.Content)
		s.failedCalls++
		s.errStreak++
		if s.errStreak > s.maxErrStreak {
			s.maxErrStreak = s.errStreak
			s.maxStreakStart = e.Step - s.errStreak + 1
		}
	} else {
		s.errStreak = 0
	}
	s.attempts = append(s.attempts, a)
}

// trustRepairCounterfactual finds a same-path contrast: an earlier failed
// attempt on a path that a later attempt on the same path with a DIFFERENT
// tool (or the same tool, if it eventually succeeded) resolved. Returns the
// narration, or "" when no honest contrast exists.
func (s *trustRepairState) counterfactual() string {
	for _, fail := range s.attempts {
		if !fail.IsError || fail.Path == "" {
			continue
		}
		for _, later := range s.attempts {
			if later.Step <= fail.Step || later.Path != fail.Path || later.IsError {
				continue
			}
			if later.Tool == fail.Tool {
				return fmt.Sprintf(
					"Had step %d (%s on %s) been approached the way step %d eventually did — the same tool succeeding after the earlier failure — the detour would have been avoided.",
					fail.Step, fail.Tool, fail.Path, later.Step,
				)
			}
			return fmt.Sprintf(
				"Had step %d used %s on %s (as step %d did), instead of %s, the outcome would likely have differed downstream.",
				fail.Step, later.Tool, fail.Path, later.Step, fail.Tool,
			)
		}
	}
	return ""
}

// maybeEmitTrustRepairDebrief composes the one-shot user-facing debrief.
// Empty string when the run does not warrant one or it already fired.
func (a *Agent) maybeEmitTrustRepairDebrief() string {
	s := a.trustRepair
	if s == nil || s.fired {
		return ""
	}
	if s.failedCalls < trustRepairMinFailures && s.maxErrStreak < trustRepairStreakTrigger {
		return ""
	}
	s.fired = true

	var b strings.Builder
	b.WriteString(fmt.Sprintf(
		"[trust-repair-debrief] This run recorded %d failed tool call(s), including an error streak of %d. A deterministic account of what went wrong, composed from run ledgers (not model self-narration):\n",
		s.failedCalls, s.maxErrStreak,
	))

	// Section 1: root cause — the dominant error streak's origin.
	if s.maxErrStreak > 0 && s.maxStreakStart > 0 && s.maxStreakStart-1 < len(s.attempts) {
		origin := s.attempts[s.maxStreakStart-1]
		line := fmt.Sprintf("1. Root cause: the dominant failure streak began at step %d (%s", origin.Step, origin.Tool)
		if origin.Path != "" {
			line += fmt.Sprintf(" on %s", origin.Path)
		}
		line += ")"
		if origin.ErrBrief != "" {
			line += fmt.Sprintf(" — %q", origin.ErrBrief)
		}
		b.WriteString(line + ".\n")
	}

	// Section 2: counterfactual — only when an honest same-path contrast exists.
	if cf := s.counterfactual(); cf != "" {
		b.WriteString("2. Counterfactual: " + cf + "\n")
	}

	// Section 3: failure boundary — how far to trust this run's conclusions.
	b.WriteString("3. Boundary: ")
	if a.compoundedUncert != nil && a.compoundedUncert.totalWeight > 0 {
		pct := int(math.Pow(perStepReliability, a.compoundedUncert.totalWeight) * 100)
		b.WriteString(fmt.Sprintf(
			"compounded trajectory reliability for this run is ~%d%% (0.85^%.1f epistemic-units from the uncertainty ledger); treat conclusions above as advisory, not verified fact.\n",
			pct, a.compoundedUncert.totalWeight,
		))
	} else {
		b.WriteString("no epistemic-risk events were flagged this run, but the failures above mean conclusions should still be treated as provisional until independently verified.\n")
	}
	b.WriteString("Research basis: counterfactual explanation + failure-boundary disclosure restores trust as effectively as apology (ICIS 2025, via zylos.ai 2026-05 trust-calibration review).")
	return b.String()
}
