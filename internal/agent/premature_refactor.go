package agent

// Premature Refactoring Detector -- Unverified Code Restructuring Awareness
//
// Research basis:
//   - "Premature Optimization" anti-pattern (minware engineering guide, 2025):
//     "Improving performance before validating product need or correctness,
//     leading to wasted effort, fragile code, and delivery delays." Ranked
//     as the #1 software engineering anti-pattern.
//   - SICA: A Self-Improving Coding Agent (arXiv:2504.15228, 2025):
//     Identifies trajectory waste from over-engineering -- agents restructure
//     code that hasn't been verified, then discover the refactored code needs
//     to change entirely once actual errors surface.
//   - KISS/YAGNI principles: implement the simplest version first, verify,
//     then refactor only after correctness is established.
//   - "Agent Last Mile Failure" (arXiv:2602.16666, 2026): compounding errors
//     from unverified foundations are the #1 differentiator between high/
//     low-performing agents.
//
// What it detects: When the agent restructures existing code (renaming,
// extracting functions, adding abstractions, reorganizing) WITHOUT having
// run a single build/test to verify the current code compiles or works.
// This is premature optimization — polishing code whose correctness is
// unknown. Once a real build surfaces errors, the refactored structure may
// need to be torn apart, wasting all the restructuring effort.
//
// How it differs from existing detectors:
//   - verify_debt: fires at 7+ ALL edits since green build (quantity-based).
//     This fires at 2+ REFACTORING edits (type-based, earlier threshold).
//   - postEditVerify: fires every 3 edits regardless of edit type. This
//     fires only for restructuring edits with type-specific guidance.
//   - convergence_lock: fires when editing AFTER verification passes. This
//     fires when refactoring BEFORE any verification.
//   - file_churn_detect: fires when same file is edited 3+ times (re-editing
//     due to wrong assumptions). This detects the FIRST structural changes
//     before any wrong assumption has been discovered.
//   - diminishing_edit: detects polish-spiral (progressively smaller edits).
//     This detects premature structural work (regardless of edit size trend).
//
// Approach:
//   Classify each successful edit as "refactoring-type" using deterministic
//   heuristics:
//     1. Edit to an EXISTING file (not newly created) where old_text and
//        new_text are similar in size (|delta|/max < 30%) — suggests
//        restructuring rather than adding new logic.
//     2. Edit content contains refactoring keywords ("refactor", "rename",
//        "extract", "simplify", "clean up", "reorganize", "optimize").
//   When 2+ refactoring-type edits accumulate without any build/test command,
//   inject guidance to verify first.
//
// Zero LLM cost. Non-blocking. Fires at most once per run.

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	// prematureRefactorThreshold: minimum refactoring-type edits before warning.
	prematureRefactorThreshold = 2

	// prematureRefactorSizeRatio: if |len(new)-len(old)|/max(len(new),len(old))
	// is below this ratio, the edit is classified as "restructuring" (similar
	// sizes suggest rename/reorganize rather than adding/removing logic).
	prematureRefactorSizeRatio = 0.3

	// prematureRefactorMinEditLen: edits below this byte count in old_text are
	// too small to classify as refactoring (could be trivial fixes).
	prematureRefactorMinEditLen = 100
)

// prematureRefactorState tracks refactoring-type edits without verification.
type prematureRefactorState struct {
	refactorEdits int  // refactoring-type edits since last verify command
	hasVerified   bool // any build/test command executed this run
	warned        bool // warning already issued
}

func newPrematureRefactorState() *prematureRefactorState {
	return &prematureRefactorState{}
}

func (s *prematureRefactorState) reset() {
	s.refactorEdits = 0
	s.hasVerified = false
	s.warned = false
}

// refactoringKeywords are words/phrases that signal a refactoring edit.
var refactoringKeywords = []string{
	"refactor", "rename", "extract", "simplify", "clean up",
	"reorganize", "optimize", "abstract", "generalize", "consolidate",
}

// classifyRefactorEdit determines whether an edit is "refactoring-type":
// restructuring existing code rather than implementing new functionality.
// Returns true if the edit looks like refactoring/optimization.
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
		// batch_replace is inherently a refactoring tool (rename/codemod).
		return true

	default:
		return false
	}
}

// isRefactoringContent classifies whether an old_text→new_text pair looks like
// refactoring rather than new feature implementation.
func isRefactoringContent(oldText, newText string) bool {
	if len(oldText) < prematureRefactorMinEditLen {
		return false
	}

	// Check for refactoring keywords in the new text — whole-word only and
	// comment-line-excluded (#487 F2: bare substring matching fired on
	// extractTargets / os.Rename / abstractHandler identifiers, and comment
	// prose like "optimize later" describes intent without restructuring
	// anything).
	newLower := strings.ToLower(prStripCommentLines(newText))
	for _, kw := range refactoringKeywords {
		if prContainsWord(newLower, kw) {
			return true
		}
	}

	// Core-delta gate (#487 F1): trim the shared prefix/suffix; what remains
	// is what actually changed. A localized fix (`>` → `>=`, `,` → `;`)
	// touches a couple of BYTES even though the byte-level core region can
	// be large (the suffix match stops at the first differing byte from the
	// tail, so intermediate unchanged lines land in the core). Compare the
	// core LINE-wise: if most core lines are identical and the differing
	// lines differ by only a few bytes each, this is localized bug fixing,
	// not restructuring — regardless of the size-similarity ratio below.
	prefix := prCommonPrefixLen(oldText, newText)
	suffix := prCommonSuffix(oldText[prefix:], newText[prefix:])
	coreOld := oldText[prefix : len(oldText)-len(suffix)]
	coreNew := newText[prefix : len(newText)-len(suffix)]
	if prCoreIsLocalizedFix(coreOld, coreNew) {
		return false
	}

	// Size similarity heuristic: if old and new are similar in length,
	// the edit is likely restructuring (rename, reorder, reformat)
	// rather than adding/removing substantial logic.
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

// prematureRefactorMinCoreDelta: below this many changed bytes per differing
// core line, the change is a localized fix rather than a restructuring
// move (#487 F1).
const prematureRefactorMinCoreDelta = 32

// prCoreIsLocalizedFix reports whether the core (prefix/suffix-trimmed)
// change region is localized bug fixing / incremental feature work rather
// than restructuring:
//   - same line count, all but ≤2 lines byte-identical, small per-line deltas
//     (e.g. `>`→`>=`, `,`→`;`)
//   - OR the core is a small APPENDED/DELETED tail block (adding a call, a
//     guard clause) — similar sizes fooled the ratio heuristic into calling
//     plain additions "restructuring" (#487 F1 general form).
//
// A rename/extract/reorganize rewrites whole lines across the region.
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
	// Line counts differ: localized iff one side's core is empty (pure tail
	// append or delete) and the other side is short.
	if len(coreOld) < 4 && len(coreNew) < 4*prematureRefactorMinCoreDelta {
		return true
	}
	if len(coreNew) < 4 && len(coreOld) < 4*prematureRefactorMinCoreDelta {
		return true
	}
	return false
}

// prCommonPrefixLen returns the length of the longest common prefix of a and b.
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

// prCommonSuffix returns the longest common suffix of a and b.
func prCommonSuffix(a, b string) string {
	n := 0
	for n < len(a) && n < len(b) && a[len(a)-1-n] == b[len(b)-1-n] {
		n++
	}
	return a[len(a)-n:]
}

// prStripCommentLines removes // comment lines and /* */ blocks so that
// intent-describing prose ("optimize later", "TODO: refactor after tests")
// cannot satisfy the keyword heuristic — only code being written counts.
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

// prContainsWord reports whether s contains kw as a whole word: the bytes
// adjacent to an occurrence must not be word characters. "extractTargets"
// does not contain the word "extract"; "extract the helper" does (#487 F2).
func prContainsWord(s, kw string) bool {
	if kw == "" {
		return false
	}
	for i := 0; i+len(kw) <= len(s); i++ {
		if s[i:i+len(kw)] != kw {
			continue
		}
		if i > 0 && prIsWordByte(s[i-1]) {
			continue
		}
		if i+len(kw) < len(s) && prIsWordByte(s[i+len(kw)]) {
			continue
		}
		return true
	}
	return false
}

func prIsWordByte(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// --- Agent integration ---

// prematureRefactorRecordEdit classifies and records an edit.
func (a *Agent) prematureRefactorRecordEdit(toolName string, args []byte) {
	if a.prematureRefactor == nil {
		return
	}
	if !productiveEditTools[toolName] {
		return
	}
	if a.prematureRefactor.hasVerified {
		return // already verified, no longer premature
	}
	if classifyRefactorEdit(toolName, json.RawMessage(args)) {
		a.prematureRefactor.refactorEdits++
		debug.Log("premature_refactor", "refactoring-type edit detected: tool=%s, count=%d, verified=%v",
			toolName, a.prematureRefactor.refactorEdits, a.prematureRefactor.hasVerified)
	}
}

// prematureRefactorRecordVerify marks that a build/test command was executed.
// Raw setter — the production entry is the command-gated variant below.
func (a *Agent) prematureRefactorRecordVerify() {
	if a.prematureRefactor == nil {
		return
	}
	a.prematureRefactor.hasVerified = true
}

// prematureRefactorRecordVerifyForTool is the production entry: it marks
// verification ONLY when the tool call carries a genuine build/test/verify
// command (#487). The previous wiring called the raw setter unconditionally
// on every tool result, so the first read_file already set hasVerified and
// permanently silenced the detector — 100% dead in production despite a
// green unit suite that never simulated the real wiring order
// (recordEdit → same-pass recordVerify → check).
func (a *Agent) prematureRefactorRecordVerifyForTool(args json.RawMessage) {
	if a.prematureRefactor == nil {
		return
	}
	if !psIsVerifyCommand(extractCommandFromArgs(args)) {
		return
	}
	a.prematureRefactor.hasVerified = true
}

// prematureRefactorCheck returns guidance if premature refactoring is detected.
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

// resetPrematureRefactor clears state for a new run.
func (a *Agent) resetPrematureRefactor() {
	if a.prematureRefactor != nil {
		a.prematureRefactor.reset()
	}
}
