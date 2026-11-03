package agent

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	prematureRefactorThreshold = 2

	prematureRefactorSizeRatio = 0.3

	prematureRefactorMinEditLen = 100
)

type prematureRefactorState struct {
	refactorEdits int
	hasVerified   bool
	warned        bool
}

func newPrematureRefactorState() *prematureRefactorState {
	return &prematureRefactorState{}
}

func (s *prematureRefactorState) reset() {
	s.refactorEdits = 0
	s.hasVerified = false
	s.warned = false
}

var refactoringKeywords = []string{
	"refactor", "rename", "extract", "simplify", "clean up",
	"reorganize", "optimize", "abstract", "generalize", "consolidate",
}

func classifyRefactorEdit(toolName string, args json.RawMessage) bool {
	if len(args) == 0 {
		return false
	}

	switch toolName {
	case "edit_file":
		var p struct {
			OldText string `json:"old_text"`
			NewText string `json:"new_text"`
		}
		if json.Unmarshal(args, &p) != nil {
			return false
		}
		return isRefactoringContent(p.OldText, p.NewText)

	case "multi_edit_file":
		var p struct {
			Edits []struct {
				OldText string `json:"old_text"`
				NewText string `json:"new_text"`
			} `json:"edits"`
		}
		if json.Unmarshal(args, &p) != nil {
			return false
		}
		for _, e := range p.Edits {
			if isRefactoringContent(e.OldText, e.NewText) {
				return true
			}
		}
		return false

	case "multi_file_edit":
		var p struct {
			Files []struct {
				Edits []struct {
					OldText string `json:"old_text"`
					NewText string `json:"new_text"`
				} `json:"edits"`
			} `json:"files"`
		}
		if json.Unmarshal(args, &p) != nil {
			return false
		}
		for _, f := range p.Files {
			for _, e := range f.Edits {
				if isRefactoringContent(e.OldText, e.NewText) {
					return true
				}
			}
		}
		return false

	case "batch_replace":

		return true

	default:
		return false
	}
}

func isRefactoringContent(oldText, newText string) bool {
	if len(oldText) < prematureRefactorMinEditLen {
		return false
	}

	newLower := strings.ToLower(prStripCommentLines(newText))
	for _, kw := range refactoringKeywords {
		if prContainsWord(newLower, kw) {
			return true
		}
	}

	prefix := prCommonPrefixLen(oldText, newText)
	suffix := prCommonSuffix(oldText[prefix:], newText[prefix:])
	coreOld := oldText[prefix : len(oldText)-len(suffix)]
	coreNew := newText[prefix : len(newText)-len(suffix)]
	if prCoreIsLocalizedFix(coreOld, coreNew) {
		return false
	}

	oldLen := len(oldText)
	newLen := len(newText)
	maxLen := math.Max(float64(oldLen), float64(newLen))
	if maxLen == 0 {
		return false
	}
	delta := math.Abs(float64(newLen - oldLen))
	ratio := delta / maxLen

	return ratio < prematureRefactorSizeRatio
}

const prematureRefactorMinCoreDelta = 32

func prCoreIsLocalizedFix(coreOld, coreNew string) bool {
	lo := strings.Split(coreOld, "\n")
	ln := strings.Split(coreNew, "\n")
	if len(lo) == len(ln) {
		diffLines, diffBytes := 0, 0
		for i := range lo {
			if lo[i] == ln[i] {
				continue
			}
			diffLines++
			p := prCommonPrefixLen(lo[i], ln[i])
			s := prCommonSuffix(lo[i][p:], ln[i][p:])
			diffBytes += (len(lo[i]) - p - len(s)) + (len(ln[i]) - p - len(s))
		}
		return diffLines <= 2 && diffBytes < prematureRefactorMinCoreDelta
	}

	if len(coreOld) < 4 && len(coreNew) < 4*prematureRefactorMinCoreDelta {
		return true
	}
	if len(coreNew) < 4 && len(coreOld) < 4*prematureRefactorMinCoreDelta {
		return true
	}
	return false
}

func prCommonPrefixLen(a, b string) int {
	n := 0
	maxN := len(a)
	if len(b) < maxN {
		maxN = len(b)
	}
	for n < maxN && a[n] == b[n] {
		n++
	}
	return n
}

func prCommonSuffix(a, b string) string {
	n := 0
	for n < len(a) && n < len(b) && a[len(a)-1-n] == b[len(b)-1-n] {
		n++
	}
	return a[len(a)-n:]
}

func prStripCommentLines(s string) string {
	lines := strings.Split(s, "\n")
	var keep []string
	inBlock := false
	for _, ln := range lines {
		trimmed := strings.TrimSpace(ln)
		if inBlock {
			if strings.Contains(trimmed, "*/") {
				inBlock = false
			}
			continue
		}
		if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, "/*") {
			if !strings.Contains(trimmed, "*/") {
				inBlock = true
			}
			continue
		}
		keep = append(keep, ln)
	}
	return strings.Join(keep, "\n")
}

func prContainsWord(s, kw string) bool {
	if kw == "" {
		return false
	}
	for i := 0; i+len(kw) <= len(s); i++ {
		if s[i:i+len(kw)] != kw {
			continue
		}

		if i > 0 && (prIsWordByte(s[i-1]) || s[i-1] == '.') {
			continue
		}
		end := i + len(kw)

		if end < len(s) && (prIsWordByte(s[end]) || s[end] == '(') {
			continue
		}
		return true
	}
	return false
}

func prIsWordByte(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

func (a *Agent) prematureRefactorRecordEdit(toolName string, args []byte) {
	if a.prematureRefactor == nil {
		return
	}
	if !productiveEditTools[toolName] {
		return
	}
	if a.prematureRefactor.hasVerified {
		return
	}
	if classifyRefactorEdit(toolName, json.RawMessage(args)) {
		a.prematureRefactor.refactorEdits++
		debug.Log("premature_refactor", "refactoring-type edit detected: tool=%s, count=%d, verified=%v",
			toolName, a.prematureRefactor.refactorEdits, a.prematureRefactor.hasVerified)
	}
}

func (a *Agent) prematureRefactorRecordVerify() {
	if a.prematureRefactor == nil {
		return
	}
	a.prematureRefactor.hasVerified = true
}

func (a *Agent) prematureRefactorRecordVerifyForTool(args json.RawMessage) {
	if a.prematureRefactor == nil {
		return
	}
	if !psIsVerifyCommand(extractCommandFromArgs(args)) {
		return
	}
	a.prematureRefactor.hasVerified = true
}

func (a *Agent) prematureRefactorCheck() string {
	if a.prematureRefactor == nil {
		return ""
	}
	s := a.prematureRefactor
	if s.warned {
		return ""
	}
	if s.hasVerified {
		return ""
	}
	if s.refactorEdits < prematureRefactorThreshold {
		return ""
	}

	s.warned = true
	debug.Log("premature_refactor", "premature refactoring detected: %d refactoring edits without any build/test",
		s.refactorEdits)

	return fmt.Sprintf(
		"%s%d%s",
		"Premature refactoring: You have made ", s.refactorEdits,
		" restructuring edits (renaming, extracting, reorganizing, or optimizing) "+
			"without running a single build or test to verify correctness. "+
			"This is premature optimization -- the #1 software engineering anti-pattern. "+
			"If the build reveals errors, your refactored structure may need to change entirely, "+
			"wasting all restructuring effort. "+
			"Run a build or test NOW to establish a correctness baseline before continuing to refactor.")
}

func (a *Agent) resetPrematureRefactor() {
	if a.prematureRefactor != nil {
		a.prematureRefactor.reset()
	}
}
