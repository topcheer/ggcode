package agent

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

const (
	retraceDiffCap    = 12000
	retraceParseLines = 6
	retraceMsgCap     = 2400
)

type retraceGateState struct {
	firedThisRun bool
}

func retraceShouldCheck(pv postEditVerifyState, researchMode, fired bool) bool {
	if fired || researchMode {
		return false
	}
	return pv.sourceEditsThisRun > 0 && pv.realBuildOrTestRunThisRun
}

func uncommittedDiff(workingDir string) string {
	out, err := exec.Command("git", "-C", workingDir, "diff", "HEAD").Output()
	if err != nil {
		debug.Log("agent", "retrace: git diff failed (%v), passing through", err)
		return ""
	}
	s := strings.TrimSpace(string(out))

	if extra := untrackedDiff(workingDir); extra != "" {
		if s == "" {
			s = extra
		} else {
			s = s + "\n" + extra
		}
	}
	if len(s) > retraceDiffCap {
		s = s[:retraceDiffCap] + "\n... (diff truncated)"
	}
	return s
}

func untrackedDiff(workingDir string) string {
	ls, err := exec.Command("git", "-C", workingDir, "ls-files", "--others", "--exclude-standard").Output()
	if err != nil {
		return ""
	}
	var sb strings.Builder
	for _, f := range strings.Split(strings.TrimRight(string(ls), "\n"), "\n") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		out, derr := exec.Command("git", "-C", workingDir, "diff", "--no-index", "--", "/dev/null", f).Output()

		if derr != nil {
			if ee, ok := derr.(*exec.ExitError); !ok || ee.ExitCode() != 1 {
				continue
			}
		}
		body := strings.TrimRight(string(out), "\n")
		if body != "" {
			if sb.Len() > 0 {
				sb.WriteByte('\n')
			}
			sb.WriteString(body)
		}
	}
	return sb.String()
}

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

func (a *Agent) runRetraceVerification(ctx context.Context, task, diff string) (msg string) {

	defer func() {
		if r := recover(); r != nil {
			debug.Log("agent", "retrace: recovered from panic in verification stream (%v), passing through", r)
			msg = ""
		}
	}()

	_, backward, _, _, _, _, err := a.streamChatResponse(ctx, buildBackwardMessages(diff), nil, nil)
	if err != nil || strings.TrimSpace(backward) == "" {
		debug.Log("agent", "retrace: backward stage failed (%v), passing through", err)
		return ""
	}
	_, recon, _, _, _, _, err2 := a.streamChatResponse(ctx, buildReconcileMessages(task, backward, diff), nil, nil)
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
