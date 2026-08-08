package agent

// Phantom Verification Detector -- Process Supervision Gap
//
// Research basis:
//   - AgentPro (EMNLP 2025, Ma et al.): "Enhancing LLM Agents with Automated
//     Process Supervision" -- each claimed step in an agent trajectory should be
//     backed by a real verification action, not just an assertion.
//   - StepORLM (arXiv:2509.22558, 2025): step-level supervision ensures each
//     modeling step is verified, mitigating credit-assignment errors.
//   - AgentLiar Detector (2025, Nilofer): false completion claims where agents
//     assert task success without actual verification runs.
//   - Lightman et al., "Let's Verify Step by Step" (2024): process-level rewards
//     catch errors that outcome-only verification misses.
//
// Problem: Coding agents sometimes assert a SPECIFIC verification OUTCOME --
// "the build passes", "all tests pass", "lint reports no errors", "the code
// compiles cleanly" -- without having actually run the corresponding verification
// command. This is distinct from generic overconfidence ("this definitely works"
// caught by unverified_confidence.go) because it makes a falsifiable, testable
// claim tied to a specific verification category (build/test/lint/typecheck).
//
// If the agent claims "tests pass" but no test command was run, or claims "the
// build compiles" but no build/compile command was run, that is a phantom
// verification -- a process-supervision gap. The claim has zero grounding in the
// trajectory's actions.
//
// This detector:
//   1. Scans assistant text for concrete verification-outcome claims, categorized
//      by verification type (build, test, lint, typecheck, compile).
//   2. Tracks which verification command categories were actually run.
//   3. When a claim of category X exists but no verification command of category
//      X was run, injects a targeted reminder.
//
// Design:
//   - Zero LLM cost -- pure deterministic regex matching + tool call tracking.
//   - Non-blocking advisory hint, capped at 2 injections per run.
//   - Only fires when a CATEGORY-SPECIFIC verification claim lacks matching
//     evidence, not on generic confidence language.
//   - Runs of a verification command reset that category's claim window so
//     legitimate "I ran tests, they pass" sequences are not flagged.

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	phantomVerifyMaxWarnings = 2
	phantomVerifyMaxExamples = 3
)

// Verification categories for claim-to-command matching.
const (
	phantomCatBuild     = "build"
	phantomCatTest      = "test"
	phantomCatLint      = "lint"
	phantomCatCompile   = "compile"
	phantomCatTypecheck = "typecheck"
)

// phantomClaimPatterns maps verification category to outcome-claim regexes.
// These match assertions that a SPECIFIC verification type yielded a positive
// result -- not generic "it works" language.
var phantomClaimPatterns = map[string][]*regexp.Regexp{
	phantomCatBuild: {
		regexp.MustCompile(`(?i)\bbuild(s|ing)?\s+(pass(es|ed)?|succeed(s|ed)?|complet(es|ed)?|is (green|ok|clean))\b`),
		regexp.MustCompile(`(?i)\b(make\s+)?build\s+(is\s+)?success(ful)?(ly)?\b`),
		regexp.MustCompile(`(?i)\bbuild\s+(verification|check)\s+(passed|succeeds?|is\s+green)\b`),
	},
	phantomCatTest: {
		regexp.MustCompile(`(?i)\b(all\s+)?tests?\s+(pass(ed|es)?|succeed(ed|s)?|are\s+(green|passing|ok))\b`),
		regexp.MustCompile(`(?i)\btest\s+(suite\s+)?(passed|succeeds?|is\s+green)\b`),
		regexp.MustCompile(`(?i)\bno\s+(failing|failed)\s+tests?\b`),
		regexp.MustCompile(`(?i)\btest\s+results?\s+(are\s+)?(green|passing|clean)\b`),
	},
	phantomCatLint: {
		regexp.MustCompile(`(?i)\blint(ing)?\s+(pass(es|ed)?|is\s+(clean|clear|ok)|report(s|ed)?\s+no\s+(issues|errors|warnings))\b`),
		regexp.MustCompile(`(?i)\bno\s+lint\s+(errors|warnings|issues)\b`),
		regexp.MustCompile(`(?i)\blint(ing)?\s+(check\s+)?(passed|is\s+clean)\b`),
	},
	phantomCatCompile: {
		regexp.MustCompile(`(?i)\bcompil(es|ed|ing)\s+(successfully|clean(ly)?|without\s+(errors|warnings))\b`),
		regexp.MustCompile(`(?i)\bthe\s+code\s+(compiles|builds|runs)\s+(correctly|fine|successfully|cleanly)\b`),
		regexp.MustCompile(`(?i)\bcompilation\s+(succeed(ed|s)?|is\s+(clean|successful))\b`),
	},
	phantomCatTypecheck: {
		regexp.MustCompile(`(?i)\bno\s+(compile|type)\s+errors?\b`),
		regexp.MustCompile(`(?i)\btype\s*check(ing)?\s+(pass(es|ed)?|succeed(s|ed)?|is\s+clean)\b`),
		regexp.MustCompile(`(?i)\bno\s+type\s+errors?\b`),
	},
}

// phantomCommandPatterns maps verification category to command/arg patterns that
// count as ACTUALLY running that verification type.
var phantomCommandPatterns = map[string]*regexp.Regexp{
	phantomCatBuild:     regexp.MustCompile(`(?i)\b(go\s+build|make\s+build|make|npm\s+run\s+build|cargo\s+build|cmake|gcc|clang|tsc\b|typescript|\.build|build\s+command)\b`),
	phantomCatTest:      regexp.MustCompile(`(?i)\b(go\s+test|make\s+test|npm\s+test|yarn\s+test|pytest|cargo\s+test|jest|mocha|\.test\.|test\s+command)\b`),
	phantomCatLint:      regexp.MustCompile(`(?i)\b(go\s+vet|golangci|eslint|flake8|pylint|ruff|rubocop|clang-tidy|shellcheck|lint|make\s+lint)\b`),
	phantomCatCompile:   regexp.MustCompile(`(?i)\b(go\s+build|gcc|clang|cc\b|make\b|cmake|cargo\s+build|npm\s+run\s+build|tsc\b|compile)\b`),
	phantomCatTypecheck: regexp.MustCompile(`(?i)\b(go\s+vet|go\s+build|tsc\b|--noEmit|mypy|pyright|flow\s+check|typecheck)\b`),
}

// phantomVerifyClaim captures a single unverified verification-outcome claim.
type phantomVerifyClaim struct {
	category  string // build, test, lint, compile, typecheck
	statement string // full sentence containing the claim
}

// phantomVerifyState tracks verification commands run and warning count.
type phantomVerifyState struct {
	warnings        int
	categoriesRun   map[string]bool // verification categories actually executed
	recentClaimCats map[string]bool // categories claimed in current assistant text
}

func newPhantomVerifyState() *phantomVerifyState {
	return &phantomVerifyState{
		categoriesRun:   make(map[string]bool),
		recentClaimCats: make(map[string]bool),
	}
}

func (s *phantomVerifyState) reset() {
	s.warnings = 0
	for k := range s.categoriesRun {
		delete(s.categoriesRun, k)
	}
	for k := range s.recentClaimCats {
		delete(s.recentClaimCats, k)
	}
}

// recordToolCall tracks whether a tool call constitutes running a verification
// command of a specific category.
func (s *phantomVerifyState) recordToolCall(toolName, toolInput string) {
	// run_command and similar execution tools carry the verification command
	// in their arguments. We check both tool name and input.
	combined := toolName + " " + toolInput
	for cat, re := range phantomCommandPatterns {
		if re.MatchString(combined) {
			s.categoriesRun[cat] = true
		}
	}
}

// detectPhantomClaims scans assistant text and returns verification-outcome claims
// whose category has NOT been verified by a matching command.
func (s *phantomVerifyState) detectPhantomClaims(text string) []phantomVerifyClaim {
	// Reset per-text claim tracking
	for k := range s.recentClaimCats {
		delete(s.recentClaimCats, k)
	}

	var claims []phantomVerifyClaim
	seen := make(map[string]bool) // dedupe by category+sentence

	for cat, patterns := range phantomClaimPatterns {
		for _, ptn := range patterns {
			matches := ptn.FindAllStringIndex(text, -1)
			for _, idx := range matches {
				statement := extractSentence(text, idx)
				if statement == "" {
					continue
				}
				key := cat + "|" + statement
				if seen[key] {
					continue
				}
				seen[key] = true
				s.recentClaimCats[cat] = true

				// Only flag if this category was NOT actually verified
				if !s.categoriesRun[cat] {
					claims = append(claims, phantomVerifyClaim{
						category:  cat,
						statement: statement,
					})
				}
			}
		}
	}

	return claims
}

// maybeWarnPhantomVerify checks for verification-outcome claims lacking matching
// verification commands and returns a guidance message if a process-supervision
// gap is detected.
func (a *Agent) maybeWarnPhantomVerify(assistantText string) string {
	if a.phantomVerify == nil {
		return ""
	}
	s := a.phantomVerify

	if s.warnings >= phantomVerifyMaxWarnings {
		return ""
	}

	claims := s.detectPhantomClaims(assistantText)
	if len(claims) == 0 {
		return ""
	}

	s.warnings++

	var examples []string
	for i, c := range claims {
		if i >= phantomVerifyMaxExamples {
			break
		}
		ex := c.statement
		if len(ex) > 90 {
			ex = ex[:87] + "..."
		}
		examples = append(examples, fmt.Sprintf("  [%s] \"%s\"", c.category, ex))
	}

	hint := fmt.Sprintf(`[Process Supervision] You asserted verification outcomes without running the matching commands:

%s

Claims like "tests pass" or "the build compiles" are phantom verifications if no test/build command was executed (AgentPro, EMNLP 2025). Run the relevant verification command (go test, go build, lint) and reference its actual output before asserting the result. A claim of success must be grounded in an observed command outcome, not inferred.`, strings.Join(examples, "\n"))

	debug.Log("phantom-verify", "detected %d unverified claims", len(claims))
	return hint
}
