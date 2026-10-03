package agent

// RETRACE bidirectional verification gate (r440).
//
// Research: "Independent Patch Verification for Coding Agents with a
// Bidirectional Reconstruct-and-Verify Framework" (RETRACE, arXiv 2608.08950;
// SWE-bench Verified +7.0pp on mini-SWE-agent, both stages contribute,
// reconciliation adds further gains).
//
// THE GAP (r440 grep-confirmed): every existing stop gate checks the run
// FORWARD - fulfillment checklist vs userPrompt (r352), per-item constraint
// audit (r392), verification receipt (r357). All of them review the diff
// UNDER THE INTERPRETATION OF THE ORIGINAL TASK, which is exactly the
// anchoring bias RETRACE removes. No mechanism anywhere infers, from the
// diff alone, what problem the changes actually address, and compares that
// reconstruction against the task. Scope creep, partial implementation, and
// fixing-a-related-but-different-problem all pass every forward gate.
//
// WHAT THIS GATE DOES (at most once per run, AFTER the r357 evidence gate
// has already demanded a build/test receipt):
//  1. Backward reconstruction (anchoring-free by construction): a completion
//     request whose message list contains ONLY the uncommitted diff - the
//     original task text is never in the prompt - infers what problem the
//     changes appear to solve, their scope, and side effects.
//  2. Forward reconciliation: a second completion gets task + reconstruction
//     + diff and must output `RETRACE: aligned | partial | misaligned` with
//     a mismatch source and targeted revision guidance.
//
// Non-interference (same doctrine as r357): misaligned/partial injects the
// revision guidance once and continues; aligned, unparseable, LLM error,
// empty diff, or non-repo all pass through unchanged.
//
// Cost: two no-tool completion calls, once per run, only after real source
// edits AND a real build/test receipt exist.

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

const (
	retraceDiffCap    = 12000 // chars of diff fed to the verifier (enough for scope, not full context)
	retraceParseLines = 6     // verdict-line scan window from end of output
	retraceMsgCap     = 2400  // chars cap for the injected gate message
)

// retraceGateState is run-lifetime, same lifecycle slot as
// finalGateFiredThisRun (verify_hint.go postEditVerifyState).
type retraceGateState struct {
	firedThisRun bool
}

// retraceShouldCheck: source edits + a REAL verification receipt (r357 has
// already fired or was satisfied) + not fired yet + not research mode
// (research runs have no diff to reconstruct).
func retraceShouldCheck(pv postEditVerifyState, researchMode, fired bool) bool {
	if fired || researchMode {
		return false
	}
	return pv.sourceEditsThisRun > 0 && pv.realBuildOrTestRunThisRun
}

// uncommittedDiff returns `git diff HEAD` output capped to retraceDiffCap
// chars. Empty string on any error (non-repo, git missing) - the gate then
// passes through (non-interference).
func uncommittedDiff(workingDir string) string {
	out, err := exec.Command("git", "-C", workingDir, "diff", "HEAD").Output()
	if err != nil {
		debug.Log("agent", "retrace: git diff failed (%v), passing through", err)
		return ""
	}
	s := strings.TrimSpace(string(out))
	if len(s) > retraceDiffCap {
		s = s[:retraceDiffCap] + "\n... (diff truncated)"
	}
	return s
}

// buildBackwardMessages constructs the anchoring-free completion request.
// The task text must NEVER appear here - that isolation is the whole point
// of the backward stage (RETRACE withholds I from B(p, tau_b)).
func buildBackwardMessages(diff string) []provider.Message {
	prompt := "You are verifying a coding agent's finished work WITHOUT seeing its task.\n\n" +
		"Below is the uncommitted diff of the changes. Infer, from the diff alone:\n" +
		"1. PROBLEM: the problem these changes appear to solve (one paragraph, as if writing the issue report they answer).\n" +
		"2. SCOPE: what is in vs deliberately out of the change.\n" +
		"3. SIDE_EFFECTS: behavior that may change beyond the stated problem.\n\n" +
		"Output those three labeled sections, nothing else.\n\nDIFF:\n" + diff
	return []provider.Message{{
		Role: "user",
		Content: []provider.ContentBlock{{
			Type: "text",
			Text: prompt,
		}},
	}}
}

// buildReconcileMessages gives the second call the original task plus the
// backward reconstruction and demands a machine-parseable verdict line.
func buildReconcileMessages(task, backwardOut, diff string) []provider.Message {
	prompt := "A coding agent finished a task. An independent verifier reconstructed - from the diff ALONE, without seeing the task - what problem the changes address.\n\n" +
		"Compare that reconstruction against the actual task and decide whether the implemented change really solves the asked problem (not a related one, not part of it).\n\n" +
		"=== ORIGINAL TASK ===\n" + task + "\n\n" +
		"=== BACKWARD RECONSTRUCTION (made without the task) ===\n" + backwardOut + "\n\n" +
		"=== DIFF (excerpt) ===\n" + diff + "\n\n" +
		"End with exactly one line:\nRETRACE: aligned   (or: partial, misaligned)\n" +
		"Before it, name any mismatch (wrong problem / partial implementation / scope spread) and, unless aligned, give one concrete revision instruction."
	return []provider.Message{{
		Role: "user",
		Content: []provider.ContentBlock{{
			Type: "text",
			Text: prompt,
		}},
	}}
}

// parseRetraceVerdict scans the last retraceParseLines lines for the verdict
// marker. Returns verdict (aligned|partial|misaligned) and the full output
// as guidance context; verdict "" when absent/unparseable.
func parseRetraceVerdict(out string) (string, string) {
	lines := strings.Split(strings.TrimRight(out, "\n "), "\n")
	start := len(lines) - retraceParseLines
	if start < 0 {
		start = 0
	}
	for i := len(lines) - 1; i >= start; i-- {
		l := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(l, "RETRACE:") {
			continue
		}
		v := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(l, "RETRACE:")))
		// Tolerate trailing annotations on the verdict line.
		switch {
		case strings.HasPrefix(v, "aligned"):
			return "aligned", out
		case strings.HasPrefix(v, "partial"):
			return "partial", out
		case strings.HasPrefix(v, "misaligned"):
			return "misaligned", out
		}
	}
	return "", ""
}

// retraceInjectMessage formats the gate injection for a non-aligned verdict.
func retraceInjectMessage(verdict, guidance string) string {
	header := map[string]string{
		"partial":    "partially addresses",
		"misaligned": "does NOT address",
	}[verdict]
	if header == "" {
		header = "may not address"
	}
	msg := fmt.Sprintf("[retrace-gate] An independent backward reconstruction of your uncommitted diff suggests the change %s the original task.\n"+
		"Reviewer analysis:\n%s\n\n"+
		"Apply the revision instruction above (or justify why the verdict is wrong), then finish.\n"+
		"This bidirectional check fires at most once per run.", header, strings.TrimSpace(guidance))
	if len(msg) > retraceMsgCap {
		msg = msg[:retraceMsgCap] + "\n... (truncated)"
	}
	return msg
}

// runRetraceVerification executes both stages and returns the gate injection
// message ("" = pass through). Errors never block the stop.
func (a *Agent) runRetraceVerification(ctx context.Context, task, diff string) string {
	_, backward, _, _, _, err := a.streamChatResponse(ctx, buildBackwardMessages(diff), nil, nil)
	if err != nil || strings.TrimSpace(backward) == "" {
		debug.Log("agent", "retrace: backward stage failed (%v), passing through", err)
		return ""
	}
	_, recon, _, _, _, err2 := a.streamChatResponse(ctx, buildReconcileMessages(task, backward, diff), nil, nil)
	if err2 != nil || strings.TrimSpace(recon) == "" {
		debug.Log("agent", "retrace: reconcile stage failed (%v), passing through", err2)
		return ""
	}
	verdict, guidance := parseRetraceVerdict(recon)
	if verdict == "" || verdict == "aligned" {
		debug.Log("agent", "retrace: verdict=%q, passing through", verdict)
		return ""
	}
	return retraceInjectMessage(verdict, guidance)
}
