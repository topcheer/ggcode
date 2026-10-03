package agent

// Attribution Experiment State - Dov-inspired intervention-driven
// validation of causal attribution hypotheses (r405).
//
// Research: "Dov: Intervention-driven Auto Debugging for LLM-agent Systems"
// (2026, openreview 99fe1321). Key critique: log-only debugging ATTRIBUTES a
// failure to a step but never VALIDATES the attribution - the agent may keep
// fixing a non-root-cause and burn repair iterations.
//
// THE GAP IN GGCODE (r405 grep-confirmed):
// causal_attribution.go computes a CRS-scored top suspect and injects "review
// that change / consider revert and re-verify" as free text - nothing tracks
// whether the revert+rerun experiment ever ran, and nothing interprets the
// rerun outcome back into a CONFIRMED/REFUTED verdict on the attribution.
//
// WHAT THIS STATE MACHINE DOES (guidance-injection pattern, isomorphic to the
// reproducer lifecycle - but the validated object is the ATTRIBUTION, not the
// bug fix):
//  1. arm(suspect, verifyCmd): when causal attribution names a top suspect,
//     remember the suspect file and the exact failing command, and inject the
//     minimal-intervention protocol (stash suspect -> rerun -> restore).
//  2. observeCommand: watch subsequent command-channel executions:
//     - an intervention (git stash push / git restore / git checkout -- /
//       undo_edit) flips the experiment to intervened.
//     - a rerun of the verify command while intervened is the experimental
//       read-out: still-failing => attribution REFUTED (stop fixing the
//       suspect), now-passing => attribution CONFIRMED.
//  3. The verdict carries a restore reminder unless a git stash pop/apply
//     was already observed.
//
// Design constraints:
//   - Zero LLM cost (string matching only).
//   - At most 1 active experiment, 2 arms, 2 verdicts per run.
//   - Gives up after attrExpGiveUpSteps command-channel steps without a
//     read-out, so a stale hypothesis never nags the loop.
//   - Non-blocking: guidance appended to the tool result.

import (
	"fmt"
	"path"
	"strings"
)

const (
	attrExpMaxArms      = 2  // total experiments armed per run
	attrExpMaxVerdicts  = 2  // total verdicts injected per run
	attrExpGiveUpSteps  = 25 // command-channel steps before abandoning
	attrExpMinSubstring = 12 // min normalized length for substring rerun match
)

type attributionExperimentState struct {
	active    bool
	suspect   string // file path of the attributed top suspect
	verifyCmd string // normalized failing command to rerun

	intervened bool // a revert of the suspect was observed
	restored   bool // git stash pop/apply observed after the intervention
	concluded  bool // verdict already injected for this experiment

	steps     int  // command-channel steps observed since arm
	arms      int  // total arms this run
	verdicts  int  // total verdicts this run
	armHinted bool // protocol hint injected for the current experiment
}

func newAttributionExperimentState() *attributionExperimentState {
	return &attributionExperimentState{}
}

// reset clears state for a new run.
func (s *attributionExperimentState) reset() {
	*s = attributionExperimentState{}
}

// arm records a fresh causal attribution as a testable hypothesis and
// returns the experiment-protocol guidance, or "" when the experiment must
// not be armed (budget exhausted, duplicate hypothesis, empty suspect).
func (s *attributionExperimentState) arm(suspect, verifyCmd string) string {
	suspect = strings.TrimSpace(suspect)
	verifyCmd = strings.TrimSpace(verifyCmd)
	if suspect == "" || verifyCmd == "" {
		return ""
	}
	if s.active && s.suspect == suspect && normalizeVerifyCmd(s.verifyCmd) == normalizeVerifyCmd(verifyCmd) {
		return "" // same hypothesis re-armed: no repeat nagging
	}
	if s.arms >= attrExpMaxArms || s.verdicts >= attrExpMaxVerdicts {
		return ""
	}
	s.active = true
	s.suspect = suspect
	s.verifyCmd = verifyCmd
	s.intervened = false
	s.restored = false
	s.concluded = false
	s.steps = 0
	s.armHinted = true
	s.arms++
	return fmt.Sprintf(
		"[attribution-experiment] The attribution above is a hypothesis, not a verdict. "+
			"Minimal intervention to validate it: (1) revert the suspect only - `git stash push -- %s` (or undo_edit), "+
			"(2) rerun the exact failing command `%s`, (3) restore with `git stash pop`. "+
			"Rerun passes => that change IS the root cause; still fails => the attribution is wrong, look elsewhere.",
		suspect, verifyCmd)
}

// observeCommand inspects a command-channel tool execution for experiment
// events (intervention, restore, rerun read-out) and returns verdict
// guidance or "".
func (s *attributionExperimentState) observeCommand(toolName, cmd string, errored bool, content string) string {
	if !s.active || s.concluded {
		// Still track restore even when idle: a stray pop after a concluded
		// experiment must not re-trigger the restore reminder later.
		if isRestoreIntervention(toolName, cmd) {
			s.restored = true
		}
		return ""
	}
	s.steps++
	if s.steps > attrExpGiveUpSteps {
		s.active = false
		return ""
	}
	if isRestoreIntervention(toolName, cmd) {
		s.restored = true
		return ""
	}
	if !s.intervened && isRevertIntervention(toolName, cmd, s.suspect) {
		s.intervened = true
		return ""
	}
	if !s.intervened || !isRerunOf(cmd, s.verifyCmd) {
		return ""
	}
	return s.readOut(errored, content)
}

// readOut concludes the experiment from the rerun outcome and formats the
// verdict guidance.
func (s *attributionExperimentState) readOut(errored bool, content string) string {
	s.active = false
	s.concluded = true
	s.verdicts++
	stillFailing := errored || looksLikeFailure(content)
	var sb strings.Builder
	sb.WriteString("[attribution-experiment] ")
	if stillFailing {
		sb.WriteString(fmt.Sprintf(
			"Rerun of `%s` STILL FAILS with %s reverted: the attribution is REFUTED - the root cause is elsewhere. "+
				"Do not keep editing that file; re-attribute (other recent edits, environment, deps).",
			s.verifyCmd, s.suspect))
	} else {
		sb.WriteString(fmt.Sprintf(
			"Rerun of `%s` now PASSES with %s reverted: the attribution is CONFIRMED - that change is the root cause. "+
				"Fix forward there.",
			s.verifyCmd, s.suspect))
	}
	if !s.restored {
		sb.WriteString(" Remember to restore the reverted change (`git stash pop` / redo the edit).")
	}
	return sb.String()
}

// isRevertIntervention reports whether the tool execution removes the
// suspect's change from the working tree: a stash push (bare `git stash`
// included), git restore, git checkout --, or the undo_edit tool.
// #3150 V1: the intervention must be suspect-scoped. A revert of an
// UNRELATED file (e.g. `git restore other.go`, or `git stash push --
// other.go`) previously flipped `intervened` and a subsequent verify
// rerun pass emitted a false CONFIRMED pointing at the wrong file.
// Rules: restore/checkout require the suspect path in the command;
// `git stash push -- <paths>` requires the suspect among the pathspecs
// (a bare stash still counts - it stashes everything, suspect included);
// undo_edit carries no path signal in its args (checkpoint_id only), so it
// can no longer count: a missed detection only leaves the experiment
// inconclusive, while a false CONFIRMED actively misdirects the fix.
func isRevertIntervention(toolName, cmd, suspect string) bool {
	if toolName == "undo_edit" {
		return false // #3150: no path signal - cannot scope to the suspect
	}
	c := strings.ToLower(cmd)
	if strings.Contains(c, "git stash") {
		for _, drop := range []string{"pop", "apply", "list", "show", "drop", "clear"} {
			if strings.Contains(c, "git stash "+drop) {
				return false // stash-management, not a push
			}
		}
		// `git stash push -- <paths>` (or -m msg -- paths) scopes the stash
		// to the pathspecs; the suspect must be among them. A bare push
		// (no pathspec) stashes everything - suspect included - and counts.
		if i := strings.LastIndex(c, "--"); i >= 0 {
			return cmdMentionsPath(c[i:], suspect)
		}
		return true
	}
	if strings.Contains(c, "git restore ") || strings.Contains(c, "git checkout --") {
		return cmdMentionsPath(c, suspect)
	}
	return false
}

// cmdMentionsPath reports whether the command text references the suspect
// path. Full-path containment covers repo-root invocations; basename
// containment covers cd-prefixed/shell-cwd-relative invocations (the
// basename alone is loose, but only pairs with an actual revert verb,
// and the false-positive surface is same-named files elsewhere - far
// smaller than the bug it fixes).

func cmdMentionsPath(cmdLower, suspect string) bool {
	suspect = strings.ToLower(strings.TrimSpace(suspect))
	if suspect == "" {
		return false
	}
	if strings.Contains(cmdLower, suspect) {
		return true
	}
	if base := path.Base(suspect); base != suspect && base != "." && base != "/" {
		return strings.Contains(cmdLower, base)
	}
	return false
}

// isRestoreIntervention reports whether the command restores a stashed/
// reverted change.
func isRestoreIntervention(toolName, cmd string) bool {
	if toolName == "undo_edit" {
		return false
	}
	c := strings.ToLower(cmd)
	return strings.Contains(c, "git stash pop") ||
		strings.Contains(c, "git stash apply")
}

// isRerunOf reports whether cmd is an execution of the stored verify
// command: normalized equality, or containment when the shorter side is
// long enough to be meaningful (cd-prefixes and trailing redirects differ).
func isRerunOf(cmd, verifyCmd string) bool {
	a := normalizeVerifyCmd(cmd)
	b := normalizeVerifyCmd(verifyCmd)
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	if len(a) >= len(b) {
		return len(b) >= attrExpMinSubstring && strings.Contains(a, b)
	}
	return len(a) >= attrExpMinSubstring && strings.Contains(b, a)
}

// normalizeVerifyCmd canonicalizes a command for rerun comparison:
// lowercase, whitespace-collapsed, trailing redirect noise stripped.
func normalizeVerifyCmd(cmd string) string {
	c := strings.Join(strings.Fields(strings.ToLower(cmd)), " ")
	c = strings.TrimSuffix(c, "2>&1")
	return strings.TrimSpace(c)
}
