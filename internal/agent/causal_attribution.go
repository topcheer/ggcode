package agent

// Causal Failure Attribution Detector — CausalFlow-inspired
//
// Research: Bonagiri et al., "CausalFlow: Causal Attribution and Counterfactual
// Repair for LLM Agent Failures" (arXiv:2605.25338, May 2026).
//
// Key insight: LLM agents frequently fail on multi-step tasks. While such
// failures are typically logged or retried heuristically, they contain
// structured signals about WHERE execution broke down. CausalFlow models
// execution traces as sequential chains of dependent steps and computes
// Causal Responsibility Scores (CRS) via step-level counterfactual
// intervention to identify failure-inducing steps.
//
// THE GAP IN GGCODE:
// When a build/test failure occurs, the agent has many detectors for the
// failure ITSELF (error_classifier, recurring_error, fix_cascade, etc.) but
// NO mechanism to trace backward through the execution trace and identify
// WHICH specific edit step most likely CAUSED the failure. The agent
// blindly tries fixes without knowing the root cause step.
//
// WHAT THIS DETECTOR DOES:
// 1. Maintains a chronological log of edit steps with their target files.
// 2. When a verification step (build/test/go test) fails, it traces
//    backward to attribute the failure to the most likely causal edit.
// 3. Computes a Causal Responsibility Score (CRS) for each recent edit:
//    - Higher for edits to files that appear in the error output.
//    - Higher for more recent edits (recency bias in causality).
//    - Higher for edits to files in the same package/directory.
// 4. Injects the top suspect step as guidance to review that specific
//    change first, preventing random/blind fix attempts.
//
// Design constraints:
//   - Zero LLM cost (deterministic matching + scoring).
//   - Fires at most 3 times per run (enough to guide, not spam).
//   - Non-blocking: guidance appended to result, execution proceeds.
//   - Caps edit log at 30 entries to bound memory.
//   - Only fires on actual failures (IsError or FAIL/error in output).

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/topcheer/ggcode/internal/provider"
)

const (
	// Max edit steps to keep in the causal trace
	causalMaxEdits = 30

	// Max warnings per run
	causalMaxWarnings = 3

	// How many recent edits to consider for attribution
	causalLookback = 10

	// CRS weights
	causalWtErrorFileMatch = 50 // edit target appears in error output
	causalWtRecency        = 10 // per-step recency (most recent = highest)
	causalWtSameDir        = 5  // same directory as error file
	causalWtSamePackage    = 8  // same Go package as error file

	// CRS threshold for "likely cause"
	causalSuspectThreshold = 25
)

// causalEditStep records a single mutation step in the agent trajectory.
type causalEditStep struct {
	iteration int    // which iteration in the agent loop
	toolName  string // edit_file, write_file, etc.
	filePath  string // primary target file
	dirPath   string // directory of target file
}

// causalAttributionState tracks edit steps and attributes failures to them.
type causalAttributionState struct {
	edits    []causalEditStep
	warnings int
}

func newCausalAttributionState() *causalAttributionState {
	return &causalAttributionState{
		edits: make([]causalEditStep, 0, causalMaxEdits),
	}
}

// causalEditTools identifies which tool calls constitute "edit steps".
// Aliased to the canonical sourceMutatingTools superset (#738); file_ops
// move/delete remains included via the canonical set.
var causalEditTools = sourceMutatingTools

// verifyToolPatterns identifies build/test verification commands.
var causalVerifyRe = regexp.MustCompile(`(?i)(go\s+(build|test|vet)|make\s+\w+|npm\s+(test|run)|cargo\s+(build|test)|pytest|jest|\.\/gradlew)`)

// errorFileRe extracts file paths from common build/test error output.
// #2099: causalVerifyRe deliberately covers non-Go toolchains (npm,
// cargo, pytest, jest, gradlew) but the capture group matched ONLY .go
// - errorFiles stayed empty on TS/JS/Python/Rust failures, every CRS
// scoring path (file-match/same-package/same-dir) sat inside the
// empty-range loop, and the detector never fired once outside Go
// projects: a silent dead zone for every toolchain it claims to cover.
// #2171: the class lacked '\' and ':' entirely, so Windows-native
// output (C:\src\api.rs:12:5, src\main.rs:3:9, even go's .\pkg\file.go
// on Windows) matched NOTHING - a platform-wide dead zone older than
// the #2099 widening. The class now admits both separators and drive
// letters, and the tsc paren form `path(12,3):` is accepted alongside
// `path:12:`.
var causalErrorFileRe = regexp.MustCompile("(?:^|\\s)((?:[\\w\\-./\\\\:]+)\\.(?:go|ts|tsx|js|jsx|mjs|py|rs|java|rb|kt|swift|c|cc|cpp|h|hpp))(?::(?:\\d+)?:|\\(\\d+,\\d+\\):)")

// recordEdit logs a mutation step.
func (s *causalAttributionState) recordEdit(toolName, filePath string, iteration int) {
	if !causalEditTools[toolName] {
		return
	}
	filePath = strings.TrimSpace(filePath)
	if filePath == "" {
		return
	}

	step := causalEditStep{
		iteration: iteration,
		toolName:  toolName,
		filePath:  filePath,
		dirPath:   dirOfFile(filePath),
	}

	if len(s.edits) >= causalMaxEdits {
		// Drop oldest — sliding window
		s.edits = s.edits[1:]
	}
	s.edits = append(s.edits, step)
}

// dirOfFile extracts the directory portion of a file path.
func dirOfFile(path string) string {
	idx := strings.LastIndex(path, "/")
	if idx < 0 {
		return ""
	}
	return path[:idx]
}

// extractErrorFiles finds file paths referenced in build/test error output.
func extractErrorFiles(output string) []string {
	matches := causalErrorFileRe.FindAllStringSubmatch(output, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(matches))
	var files []string
	for _, m := range matches {
		// Normalize: strip leading ./ for consistent comparison
		f := strings.TrimSpace(m[1])
		// #2171: Windows-native output uses backslashes - fold them to
		// '/' FIRST (a `.\pkg\file.go` only becomes `./pkg/file.go` after
		// folding) so the ./ strip and the same-dir/same-package
		// attribution work uniformly on every platform.
		f = strings.ReplaceAll(f, "\\", "/")
		f = strings.TrimPrefix(f, "./")
		// #2179: URL-shaped text satisfies the class wholesale (both
		// separators plus ':') and was captured as an "error file" - it
		// never matches any edited file, but a NON-EMPTY errorFiles list
		// alone earned the recency weight, handing URL-bearing outputs a
		// free attribution score. Skip http(s) captures outright.
		if strings.HasPrefix(f, "http://") || strings.HasPrefix(f, "https://") {
			continue
		}
		if !seen[f] {
			seen[f] = true
			files = append(files, f)
		}
	}
	return files
}

// goPackageOf extracts the Go package path from a .go file path.
// e.g. "internal/agent/foo.go" → "internal/agent"
func goPackageOf(path string) string {
	if !strings.HasSuffix(path, ".go") {
		return ""
	}
	idx := strings.LastIndex(path, "/")
	if idx < 0 {
		return ""
	}
	return path[:idx]
}

// normalizeCausalPath canonicalizes a path for comparison (#1771 case 1):
// slash form + cleaned, so absolute vs relative spellings of the same
// tree compare equal in the package/dir tiers instead of silently
// losing those weights to recency.
func normalizeCausalPath(p string) string {
	return filepath.ToSlash(filepath.Clean(p))
}

// computeCRS computes the Causal Responsibility Score for an edit step
// given the error files extracted from the failure output.
func computeCRS(edit causalEditStep, errorFiles []string, recencyRank int) int {
	score, _ := computeCRSDetail(edit, errorFiles, recencyRank, nil)
	return score
}

// computeCRSDetail also reports whether the edit target actually MATCHED
// an error file - the wording branch must not infer that from the total
// score (#1771: recency alone reaches 10x10=100 >= 50, so a total-score
// gate asserted "error output references this file" for edits with ZERO
// file-match evidence, sending the agent to fix an unrelated recent edit).
func computeCRSDetail(edit causalEditStep, errorFiles []string, recencyRank int, bareAmbiguous map[string]bool) (int, bool) {
	score := 0
	fileMatch := false

	for _, ef := range errorFiles {
		efN := normalizeCausalPath(ef)
		editN := normalizeCausalPath(edit.filePath)
		// Exact file match — strongest signal
		if efN == editN {
			score += causalWtErrorFileMatch
			fileMatch = true
			continue
		}
		// #2171 gap 2: tier the suffix match by path evidence. A suffix
		// match where the SHORT side carries directory structure is a
		// genuine path-aware match. A BARE basename from the error output
		// is strong evidence only when it is UNambiguous - probe 2 kept
		// same-directory bare references at full weight. When multiple
		// edits share that basename across directories (bareAmbiguous,
		// precomputed by the caller), a bare hit is weak evidence - any
		// same-suffix file qualifies - so it takes the same-dir weight and
		// must NOT set fileMatch. The symmetric arm gets the same boundary.
		suffixTier := func(long, short string) (hit, strong bool) {
			if strings.Contains(short, "/") {
				// #2218 case A: anchored but possibly not unique - the same
				// ambiguity probe as bare names applies (precomputed above).
				return strings.HasSuffix(long, "/"+short), !bareAmbiguous[short]
			}
			if filepath.Base(long) == short {
				return true, !bareAmbiguous[short]
			}
			return false, false
		}
		if hit, strong := suffixTier(editN, efN); hit {
			if strong {
				score += causalWtErrorFileMatch
				fileMatch = true
			} else {
				score += causalWtSameDir
			}
			continue
		}
		if hit, strong := suffixTier(efN, editN); hit {
			if strong {
				score += causalWtErrorFileMatch
				fileMatch = true
			} else {
				score += causalWtSameDir
			}
			continue
		}

		// Same Go package (path-form normalized: absolute vs relative
		// spellings of the same tree must not silently lose this weight)
		ep := goPackageOf(efN)
		tp := goPackageOf(editN)
		if ep != "" && tp != "" && ep == tp {
			score += causalWtSamePackage
			continue
		}

		// Same directory
		ed := dirOfFile(efN)
		if ed != "" && ed == normalizeCausalPath(edit.dirPath) {
			score += causalWtSameDir
		}
	}

	// Recency bonus: most recent edit gets the highest bonus.
	// #1528: recency alone must NEVER clear the suspect threshold - with
	// no error-file evidence there is no causal signal at all ("no
	// meaningful causal signal, skip"), yet rank*10 (up to 100) crossed
	// both the 25 threshold and the 50 file-match wording branch, firing
	// "error output references this file" for outputs containing NO file
	// (git push rejections, missing tools) and blaming innocent recent
	// edits. Zero evidence -> zero score.
	if len(errorFiles) > 0 {
		score += recencyRank * causalWtRecency
	}

	return score, fileMatch
}

// bareAmbiguousNames (#2171 gap 2, #2218 case A) precomputes which
// normalized error-file references are ambiguous across the recorded
// edits: the reference matches two or more DISTINCT edit paths, so a
// hit cannot tell them apart and must be tiered down to weak evidence at
// scoring time. Bare basenames match via filepath.Base; directory-
// carrying references (api/types.go) match via the same "/"+ref suffix
// anchor the suffix tier uses - anchored is NOT unique, so both shapes
// get the ambiguity probe.
func bareAmbiguousNames(edits []causalEditStep, errorFiles []string) map[string]bool {
	ambiguous := map[string]bool{}
	for _, ef := range errorFiles {
		efN := normalizeCausalPath(ef)
		if efN == "" {
			continue
		}
		seen := map[string]bool{}
		for _, e := range edits {
			editN := normalizeCausalPath(e.filePath)
			var hit bool
			if strings.Contains(efN, "/") {
				// #2218: same anchor as the suffix tier - two edits ending in
				// "/api/types.go" (pkg/api + internal/api) both match a
				// relative "api/types.go" compile error at full weight without
				// this probe (recency broke the tie with max-authority wording).
				hit = strings.HasSuffix(editN, "/"+efN)
			} else {
				hit = filepath.Base(editN) == efN
			}
			if hit {
				seen[editN] = true
			}
		}
		if len(seen) >= 2 {
			ambiguous[efN] = true
		}
	}
	return ambiguous
}

// readCmdPrefixes lists read-only listing tools whose OUTPUT mimics
// failure evidence (#1528 case C): run_command("grep -rn FAIL ./...")
// exits 0, yet its output carries the literal FAIL plus path.go:line:
// lines - character-for-character causalErrorFileRe's shape - so every
// content gate passed and an innocent recent edit got blamed. Layer 1
// (the tool-name filter at the call site) cannot see through the shell.
var readCmdPrefixes = []string{"grep", "rg", "cat ", "head", "tail", "awk", "sed -n", "find ", "less", "bat "}

// causalCmdForGate resolves the command text the causal gate probes.
// Direct command tools carry "command" in their args; the job polling
// channels (wait_command/read_command_output) only carry job_id, so the
// gate falls back to the "Command: " header the tool layer emits in
// every job snapshot (#2218 case B).
func causalCmdForGate(tc provider.ToolCallDelta, content string) string {
	if cmd := extractStringField(tc.Arguments, "command"); cmd != "" {
		return cmd
	}
	if tc.Name == "wait_command" || tc.Name == "read_command_output" {
		for _, ln := range strings.Split(content, "\n") {
			if v, ok := strings.CutPrefix(ln, "Command: "); ok {
				return strings.TrimSpace(v)
			}
		}
	}
	return ""
}

// looksLikeReadCommand reports whether the command text begins with a
// read-only listing tool.
func looksLikeReadCommand(cmd string) bool {
	c := strings.TrimSpace(cmd)
	for _, p := range readCmdPrefixes {
		if strings.HasPrefix(c, p) || strings.HasPrefix(c, "./"+p) {
			return true
		}
	}
	// Common compound prefixes: cd dir && grep ...
	if i := strings.LastIndex(c, "&&"); i >= 0 {
		return looksLikeReadCommand(c[i+2:])
	}
	if i := strings.LastIndex(c, "|"); i >= 0 {
		return looksLikeReadCommand(c[i+1:])
	}
	return false
}

// attributeFailureCmd is the call-site entry that knows the command text
// and exit status (#1528 case C): a SUCCEEDED read-only command whose
// output merely contains "FAIL" (grep/cat of logs or sources) must not
// be attributed as a build/test failure.
func (s *causalAttributionState) attributeFailureCmd(output, cmd string, errored bool) string {
	if !errored && looksLikeReadCommand(cmd) {
		return ""
	}
	return s.attributeFailure(output)
}

// attributeFailure traces backward from a failure to identify the most
// likely causal edit step(s). Returns formatted guidance or "".
func (s *causalAttributionState) attributeFailure(output string) string {
	if s.warnings >= causalMaxWarnings {
		return ""
	}
	if len(s.edits) == 0 {
		return ""
	}

	// Only process if this looks like a build/test failure.
	// The bare looksLikeFailure substring gate used to fire on ANY tool's
	// output — a successful read_file/grep of source containing
	// fmt.Errorf("failed to ...") got misattributed as a build failure and
	// burned the 3-warning budget (#379). Restrict substring heuristics to
	// outputs that also look like command/verify output; pure source dumps
	// need the explicit verify-command regex.
	looksVerify := causalVerifyRe.MatchString(output)
	looksFail := looksLikeFailure(output)
	// #1442-A: the bare newline-count arm is gone - a 6-line grep result
	// with a stray "fail" passed it and its path.go:line: lines
	// (character-for-character causalErrorFileRe's shape) named innocent
	// files as error files (probe: CRS=84 blaming an edit on a PASSING
	// test's grep output). Compiler/tester FEATURE WORDS now gate the
	// multi-line arm; the tool-name filter at the call site is the
	// second layer.
	looksLikeCmdOutput := strings.Contains(output, "exit status") ||
		strings.Contains(output, "exit code") ||
		strings.Contains(output, "FAIL") ||
		strings.Contains(output, "cannot use") ||
		strings.Contains(output, "undefined:") ||
		strings.Contains(output, "build failed") ||
		strings.Contains(output, "vet:")
	if !looksVerify && !(looksFail && looksLikeCmdOutput) {
		return ""
	}

	errorFiles := extractErrorFiles(output)

	// Score recent edits
	start := len(s.edits) - causalLookback
	if start < 0 {
		start = 0
	}
	recent := s.edits[start:]

	type scored struct {
		step      causalEditStep
		score     int
		rank      int
		fileMatch bool // #1771: an error file actually matched this edit
	}

	var results []scored
	for i, edit := range recent {
		// Recency rank: most recent edit gets highest rank (i+1)
		recencyRank := i + 1
		score, matched := computeCRSDetail(edit, errorFiles, recencyRank, bareAmbiguousNames(s.edits, errorFiles))
		results = append(results, scored{step: edit, score: score, rank: i, fileMatch: matched})
	}

	if len(results) == 0 {
		return ""
	}

	// Find top suspect
	best := results[0]
	for _, r := range results[1:] {
		if r.score > best.score {
			best = r
		}
	}

	// If no meaningful causal signal, skip
	if best.score < causalSuspectThreshold {
		return ""
	}

	s.warnings++

	// Format guidance
	var sb strings.Builder
	sb.WriteString("[causal-attribution] ")
	if best.fileMatch { // #1771: evidence type, never the total score
		sb.WriteString(fmt.Sprintf("Build/test failure likely caused by your %s to %s (step %d, CRS=%d — error output references this file). ",
			best.step.toolName, best.step.filePath, best.step.iteration, best.score))
	} else {
		sb.WriteString(fmt.Sprintf("Build/test failure most likely originated from your %s to %s (step %d, CRS=%d — same package/directory as error). ",
			best.step.toolName, best.step.filePath, best.step.iteration, best.score))
	}
	sb.WriteString("Review that change first before attempting a blind fix. ")
	sb.WriteString("Consider: git diff that file, check if the error line maps to your edit, or revert and re-verify.")

	return sb.String()
}

// looksLikeFailure detects common failure indicators in tool output.
func looksLikeFailure(output string) bool {
	outputLower := strings.ToLower(output)
	failPatterns := []string{
		"fail", "error:", "panic:", "undefined:", "cannot find",
		"compile error", "compilation error", "build failed", "test failed",
		"fatal error", "syntax error", "type mismatch",
	}
	for _, p := range failPatterns {
		if strings.Contains(outputLower, p) {
			return true
		}
	}
	return false
}

// reset clears state for a new run.
func (s *causalAttributionState) reset() {
	s.edits = s.edits[:0]
	s.warnings = 0
}
