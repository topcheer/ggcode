package agent

import (
	"fmt"
	"strings"
)

const (
	attrExpMaxArms      = 2
	attrExpMaxVerdicts  = 2
	attrExpGiveUpSteps  = 25
	attrExpMinSubstring = 12
)

type attributionExperimentState struct {
	active    bool
	suspect   string
	verifyCmd string

	intervened bool
	restored   bool
	concluded  bool

	steps     int
	arms      int
	verdicts  int
	armHinted bool
}

func newAttributionExperimentState() *attributionExperimentState {
	return &attributionExperimentState{}
}

func (s *attributionExperimentState) reset() {
	*s = attributionExperimentState{}
}

func (s *attributionExperimentState) arm(suspect, verifyCmd string) string {
	suspect = strings.TrimSpace(suspect)
	verifyCmd = strings.TrimSpace(verifyCmd)
	if suspect == "" || verifyCmd == "" {
		return ""
	}
	if s.active && s.suspect == suspect && normalizeVerifyCmd(s.verifyCmd) == normalizeVerifyCmd(verifyCmd) {
		return ""
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

func (s *attributionExperimentState) observeCommand(toolName, cmd string, errored bool, content string) string {
	if !s.active || s.concluded {

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

func isRevertIntervention(toolName, cmd, suspect string) bool {
	c := strings.ToLower(cmd)
	suspectHit := func() bool {
		return suspectNamedIn(c, suspect)
	}
	if toolName == "undo_edit" {
		return suspectHit()
	}
	if strings.Contains(c, "git stash") {
		for _, drop := range []string{"pop", "apply", "list", "show", "drop", "clear"} {
			if strings.Contains(c, "git stash "+drop) {
				return false
			}
		}
		if strings.TrimSpace(lastPathspec(c)) != "" {
			return suspectHit()
		}
		if len(stashPushPathspecs(c)) > 0 {
			return suspectHit()
		}
		return true
	}
	if strings.Contains(c, "git restore ") || strings.Contains(c, "git checkout --") {
		return suspectHit()
	}
	return false
}

func suspectNamedIn(c, suspect string) bool {
	sl := strings.ToLower(strings.TrimSpace(suspect))
	if sl == "" {
		return false
	}
	for _, tok := range strings.Fields(c) {
		tok = strings.TrimSuffix(tok, ",")
		if tok == sl || strings.TrimPrefix(tok, "./") == sl {
			return true
		}

		if strings.Contains(tok, `"`+sl+`"`) {
			return true
		}
	}
	return false
}

func stashPushPathspecs(c string) []string {
	fields := strings.Fields(c)
	start := -1
	for i, f := range fields {
		if f == "push" {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return nil
	}
	var specs []string
	skipValue := false
	inMsg := false
	for _, f := range fields[start:] {
		if inMsg {
			if strings.HasSuffix(f, `"`) {
				inMsg = false
			}
			continue
		}
		if skipValue {
			skipValue = false
			if strings.HasPrefix(f, `"`) && !strings.HasSuffix(f, `"`) {
				inMsg = true
			}
			continue
		}
		switch {
		case f == "-m" || f == "--message":
			skipValue = true
		case strings.HasPrefix(f, "-"):

		default:
			specs = append(specs, f)
		}
	}
	return specs
}

func lastPathspec(c string) string {
	idx := strings.LastIndex(c, "--")
	if idx < 0 {
		return ""
	}
	return c[idx+2:]
}

func isRestoreIntervention(toolName, cmd string) bool {
	if toolName == "undo_edit" {
		return false
	}
	c := strings.ToLower(cmd)
	return strings.Contains(c, "git stash pop") ||
		strings.Contains(c, "git stash apply")
}

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

func normalizeVerifyCmd(cmd string) string {
	c := strings.Join(strings.Fields(strings.ToLower(cmd)), " ")
	c = strings.TrimSuffix(c, "2>&1")
	return strings.TrimSpace(c)
}
