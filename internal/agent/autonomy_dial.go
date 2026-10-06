package agent

import (
	"strconv"
	"strings"
)

// Progressive autonomy dial (r407).
//
// Frontier basis (online, pre-fetched by the main session):
//   - Anthropic five-level agent autonomy scale (2026-02 study + Knight
//     Columbia framework): autonomy should be a governed level, not a toggle.
//   - agentpatterns.ai "Progressive Autonomy": autonomy is a dial you turn up
//     based on DEMONSTRATED reliability - each stage has promotion criteria
//     and demotion triggers.
//   - zylos.ai trust calibration (2026-05): overtrust lets confidently-wrong
//     actions through; undertrust wastes verification effort. Both directions
//     need calibrating.
//   - CCSIL (curvelabs 2026-03): calibrated confidence feeds monitorability
//     gates that earn long-horizon deployment rights.
//
// Gap confirmed by the sa-154 audit: ggcode has rich reliability signals
// (result.IsError, permission denials, 74 trajectory-intelligence files) but
// NONE of them feed back into the permission level. All SetMode call sites
// are human/model-initiated switches; approval-memory is a per-key dial that
// even resets on mode change (EnsureModeScope). The reliability-to-permission
// feedback loop did not exist.
//
// V1 scope: ADVISORY ONLY. The dial never switches modes itself - it emits a
// one-shot suggestion the human acts on via the existing switch_mode surface.
// Deliberately conservative per the zylos overtrust warning: promotion
// requires a long clean streak with zero denials, and any mode change resets
// the observation window (same anti-contamination semantics as
// EnsureModeScope - trust earned in one mode must not carry into another).

const (
	// autonomyPromoteStreak: consecutive clean tool calls (no error, no
	// denial) before suggesting a promotion. High bar on purpose - the cost
	// of a premature promotion suggestion is user overtrust.
	autonomyPromoteStreak = 30
	// autonomyDemoteDenies: permission denials observed in a run before
	// suggesting demotion (each denial is the gate actively stopping the
	// agent - repeated stops mean the mode outruns demonstrated reliability).
	autonomyDemoteDenies = 3
	// autonomyDemoteErrStreak: consecutive failed tool calls before
	// suggesting demotion (flailing, not operating).
	autonomyDemoteErrStreak = 5
)

// autonomyDialState tracks per-run tool reliability for the advisory dial.
type autonomyDialState struct {
	consecutiveOK int
	errStreak     int
	denyCount     int
	suggestedUp   bool // emitted gates - one suggestion per direction per session
	suggestedDown bool
}

func newAutonomyDialState() *autonomyDialState { return &autonomyDialState{} }

// reset re-opens the observation window. Called at run start AND on every
// permission-mode change: reliability evidence must not survive a mode
// transition in either direction. Suggestion gates are window-scoped too -
// one suggestion per direction per window (a fresh window earning a fresh
// streak is legitimately new evidence).
func (s *autonomyDialState) reset() {
	s.consecutiveOK = 0
	s.errStreak = 0
	s.denyCount = 0
	s.suggestedUp = false
	s.suggestedDown = false
}

// autonomyDenialMarkers identify permission/approval rejections inside tool
// result text. The exact wording varies by gate; these cover the permission
// package's deny messages and the approval-deny paths. Case-insensitive.
var autonomyDenialMarkers = []string{
	"permission denied",
	"denied by permission",
	"rejected by permission",
	"approval denied",
	"approval rejected",
	"user rejected",
	"user denied",
	"was blocked by",
}

// looksLikeDenial reports whether a (failed) tool result content reads as a
// permission/approval rejection rather than an ordinary execution error.
func looksLikeDenial(content string) bool {
	if content == "" {
		return false
	}
	lc := strings.ToLower(content)
	for _, m := range autonomyDenialMarkers {
		if strings.Contains(lc, m) {
			return true
		}
	}
	return false
}

// record observes one tool result. deny detection only runs on failed calls:
// a clean success containing the word "denied" (e.g. a grep result about
// permission code) is not a denial signal.
func (s *autonomyDialState) record(isErr bool, content string) {
	if isErr {
		if looksLikeDenial(content) {
			s.denyCount++
		} else {
			s.errStreak++
		}
		s.consecutiveOK = 0
		return
	}
	s.consecutiveOK++
	s.errStreak = 0
}

// suggest returns an advisory dial message, or "". Non-empty at most once
// per direction per observation window (emitted gates reset with the
// window), evaluated at digest time.
func (s *autonomyDialState) suggest() string {
	if !s.suggestedUp && s.consecutiveOK >= autonomyPromoteStreak && s.denyCount == 0 && s.errStreak == 0 {
		s.suggestedUp = true
		return "[autonomy-dial] " + strconv.Itoa(s.consecutiveOK) + " consecutive clean tool calls with zero denials this run - " +
			"demonstrated reliability supports more autonomy. Consider switching to a less supervised mode " +
			"(switch_mode / /mode) if this matches your trust level. Advisory only; you stay in control."
	}
	if !s.suggestedDown && (s.denyCount >= autonomyDemoteDenies || s.errStreak >= autonomyDemoteErrStreak) {
		s.suggestedDown = true
		reason := strconv.Itoa(s.denyCount) + " permission denials"
		if s.errStreak >= autonomyDemoteErrStreak {
			reason = strconv.Itoa(s.errStreak) + " consecutive failed tool calls"
		}
		return "[autonomy-dial] " + reason + " observed - the current mode may be outrunning demonstrated reliability. " +
			"Consider a more supervised mode (switch_mode / /mode) or course-correcting the approach. Advisory only."
	}
	return ""
}
