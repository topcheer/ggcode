package agent

import (
	"strings"
	"sync"
	"unicode"

	"github.com/topcheer/ggcode/internal/debug"
)

// reversibilityState tracks pre-action risk assessment for potentially
// irreversible operations. Inspired by Counterfactual Pre-Mortem Loops
// (Curve Labs, 2026) and irreversibility awareness incidents (Vectimus, 2026).
//
// Unlike gitDestructiveState (which blocks known-bad git commands post-hoc),
// this detector runs BEFORE tool execution and assesses whether the agent
// has verified safety conditions (tests pass, build succeeds, changes staged)
// before committing to high-stakes actions.
type reversibilityState struct {
	mu          sync.Mutex
	warnCount   int
	maxWarnings int

	// Track whether safety prerequisites were met during this run
	testsRan    bool
	buildRan    bool
	stagingSeen bool
}

func newReversibilityState() *reversibilityState {
	return &reversibilityState{
		maxWarnings: 2,
	}
}

func (r *reversibilityState) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.warnCount = 0
	r.testsRan = false
	r.buildRan = false
	r.stagingSeen = false
}

// recordSafetySignal tracks that the agent performed a safety check
// (test, build, staging) during this run. This resets the "you haven't verified"
// risk flag so subsequent high-stakes actions don't re-trigger unnecessarily.
func (r *reversibilityState) recordSafetySignal(toolName, args string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	switch toolName {
	case "run_command":
		tokens := commandTokens(args)
		if hasCommandToken(tokens, "test", "pytest") {
			r.testsRan = true
		}
		if hasCommandToken(tokens, "build", "make") {
			r.buildRan = true
		}
	case "git_add", "git_commit":
		r.stagingSeen = true
	}
}

// commandTokens strips the leading '# ' description comment that run_command
// args carry, lowercases the remainder, and splits it into word tokens.
// Splitting happens on whitespace AND on JSON-wrapper punctuation
// ({}[]":,;) so that when the raw args are a JSON envelope like
// `{"command":"make verify-ci"}` the embedded words are still visible.
// Path/flag punctuation (- . / _) is deliberately NOT a separator so
// "releases/latest", "Makefile" and "latest-build.txt" stay single tokens
// and cannot trigger false positives (#1194). A bare "test" token already
// covers the "go test", "npm test" and "make test" subcommand forms, so no
// separate bigram checks are needed.
func commandTokens(args string) []string {
	cmd := args
	if strings.HasPrefix(cmd, "#") {
		if idx := strings.IndexByte(cmd, '\n'); idx >= 0 {
			cmd = cmd[idx+1:]
		} else {
			cmd = ""
		}
	}
	cut := func(r rune) bool {
		switch r {
		case '{', '}', '[', ']', '"', ':', ',', ';':
			return true
		}
		return unicode.IsSpace(r)
	}
	return strings.FieldsFunc(strings.ToLower(cmd), cut)
}

// hasCommandToken reports whether any command token equals one of the given
// words (exact, word-boundary match; never a substring match).
func hasCommandToken(tokens []string, words ...string) bool {
	for _, tok := range tokens {
		for _, w := range words {
			if tok == w {
				return true
			}
		}
	}
	return false
}

// checkPreAction evaluates whether a high-stakes tool call should trigger
// a reversibility warning. Returns non-empty guidance if the action is
// potentially irreversible AND the agent hasn't demonstrated safety verification.
//
// High-stakes actions:
//   - git_commit: irreversible without reset; verify tests/build pass first
//   - git_push: pushes to remote; verify CI/local checks pass first
//   - file_ops (delete): file deletion; verify no references broken first
//   - git_reset --hard, git_checkout: can discard uncommitted work
func (r *reversibilityState) checkPreAction(toolName, args string) string {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.warnCount >= r.maxWarnings {
		return ""
	}

	lowerArgs := strings.ToLower(args)

	switch toolName {
	case "git_commit":
		// Committing without any test or build verification in this run.
		// The commit is hard to undo if it contains bugs.
		if !r.testsRan && !r.buildRan && !r.stagingSeen {
			r.warnCount++
			debug.Log("agent", "reversibility: git_commit without prior verification (run #%d)", r.warnCount)
			return "[reversibility] Committing without tests/build. Run verification first."
		}

	case "run_command":
		// Detect git push commands - pushing without local verification.
		if isGitPush(lowerArgs) && !r.testsRan && !r.buildRan {
			r.warnCount++
			debug.Log("agent", "reversibility: git push without prior verification (run #%d)", r.warnCount)
			return "[reversibility] Pushing without local tests/build. Verify first."
		}
		// git reset --hard or git clean - these discard uncommitted work
		// permanently and are not reversible.
		if isDestructiveGit(lowerArgs) {
			r.warnCount++
			debug.Log("agent", "reversibility: destructive git command detected (run #%d)", r.warnCount)
			return "[reversibility] Destructive git command. Consider `git stash` first."
		}

	case "file_ops":
		// File deletion via file_ops tool - verify no references first.
		if strings.Contains(lowerArgs, `"action":"delete"`) || strings.Contains(lowerArgs, `"action": "delete"`) {
			r.warnCount++
			debug.Log("agent", "reversibility: file_ops delete detected (run #%d)", r.warnCount)
			return "[reversibility] Deleting file. Verify no references first."
		}
	}

	return ""
}

func isGitPush(s string) bool {
	return strings.Contains(s, "git push")
}

func isDestructiveGit(s string) bool {
	return strings.Contains(s, "reset --hard") ||
		strings.Contains(s, "clean -f") ||
		strings.Contains(s, "checkout -- ")
}
