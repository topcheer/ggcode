package agent

import (
	"encoding/json"
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
		// #2255 M2: owner-anchor the signal - the test/build token must
		// belong to a real test/build COMMAND OWNER (the command's first
		// token), not merely appear anywhere in the token stream.
		// `git commit -m "build: bump version"` tokenizes `build:` to a
		// bare `build` and used to flip buildRan, silently disarming the
		// commit/push gate: a message CLAIMING verification counted as
		// verification. commandTokens leaves the JSON `command` key in the
		// stream, so the command's first token is tokens[1] when present.
		cmd0 := ""
		if len(tokens) > 0 {
			cmd0 = tokens[0]
			if cmd0 == "command" && len(tokens) > 1 {
				cmd0 = tokens[1]
			}
		}
		switch cmd0 {
		case "go", "npm", "yarn", "pnpm", "cargo", "python", "python3":
			if hasCommandToken(tokens[1:], "test", "pytest", "vitest", "jest") {
				r.testsRan = true
			}
			if hasCommandToken(tokens[1:], "build") {
				r.buildRan = true
			}
		case "make", "build":
			// #2552: only real build targets count as build verification.
			// `make clean/fmt/tidy/install-*` used to flip buildRan
			// unconditionally, silently disarming the commit/push gate the
			// same way #2255's "build: bump" commit message did. A bare
			// `make` (default target is conventionally all/build) stays
			// conservative-build; the JSON envelope form is
			// {"command":"make"} -> tokens [command make].
			bareMake := len(tokens) <= 1 || (tokens[0] == "command" && len(tokens) == 2)
			if cmd0 == "build" || bareMake || hasCommandToken(tokens[1:], "build", "all", "verify", "verify-ci") {
				r.buildRan = true
			}
			if hasCommandToken(tokens[1:], "test", "check") {
				r.testsRan = true
			}
		case "pytest", "py.test", "vitest", "jest":
			// Bare test-runner commands as the first token ARE test runs
			// (#1194: `pytest -q scripts/` has no `test` token following).
			// #2754: bare `test` is NOT in this list - it is the POSIX
			// shell builtin (`test -f x`, `test -d dist && rm -rf dist`),
			// a conditional, not a test run. Counting it flipped testsRan
			// and silently disarmed the commit/push gate - the same
			// false-verification family as #2255 ("build:" in a commit
			// message) and #2552 (`make clean` counted as build).
			r.testsRan = true
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
// #2563: the ':' cut is gated on actually seeing a JSON envelope - git
// global-flag VALUES legally contain colons (`git -c http.proxy=http://p:8080
// clean -fd`, `--git-dir=/a:b/.git`), and splitting them left value debris
// that stripGitGlobalFlagTokens anchored the subcommand on, silencing the
// destructive check. Outside an envelope, colon values stay whole tokens
// (aligned with the sibling layer's strings.Fields).
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
	trimmed := strings.TrimSpace(cmd)
	isEnvelope := strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")
	if isEnvelope {
		// Parse the envelope and tokenize the command VALUE instead of
		// cutting through JSON punctuation: a colon-bearing value inside
		// the envelope ({"command":"git -c http.proxy=http://p:8080 ..."})
		// must stay whole too.
		var env struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal([]byte(trimmed), &env); err == nil && env.Command != "" {
			return commandTokens(env.Command)
		}
	}
	cut := func(r rune) bool {
		switch r {
		case '{', '}', '[', ']', '"', ',', ';':
			return true
		case ':':
			return isEnvelope
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

// #1490-D: raw substring tests on the whole args ("git push" in a
// comment line, "reset --hard" inside a grep pattern) fired the gate
// on harmless commands - the same false-positive family #1194 fixed
// for test/build. Now tokenized with ownership bigrams: `git` must be
// the adjacent predecessor token of the subcommand.
func isGitPush(s string) bool {
	tokens := commandTokens(s)
	for i := 0; i+1 < len(tokens); i++ {
		// Prefix keeps the #1194 conservative posture: `git pushd` and
		// fused forms still fire; a mere mention in a comment/grep
		// cannot (git must be the adjacent owner token).
		if strings.Trim(tokens[i], "\"'") == "git" && strings.HasPrefix(strings.Trim(tokens[i+1], "\"'"), "push") {
			return true
		}
	}
	return false
}

func isDestructiveGit(s string) bool {
	// #2255 F2 (review residual): commandTokens cuts on ';' and newlines,
	// so those segment boundaries vanish from the token stream - split the
	// raw string on them first and judge each subcommand independently.
	for _, sub := range strings.FieldsFunc(s, func(r rune) bool { return r == ';' || r == '\n' }) {
		if isDestructiveGitSub(sub) {
			return true
		}
	}
	return false
}

func isDestructiveGitSub(s string) bool {
	tokens := commandTokens(s)
	// #2255 H1: see through git global flags (-C <path>, --git-dir=X) so
	// the bigrams anchor on the real subcommand, mirroring the sibling
	// layer's normalizeGitGlobalFlags.
	tokens = stripGitGlobalFlagTokens(tokens)
	// #2255 F3 (review residual): judge each git SEGMENT as a unit - the
	// subcommand bigram and the destructive flag must come from the SAME
	// command segment. A whole-stream bigram plus a whole-stream flag scan
	// let `git checkout main && git log -- file` combine checkout (segment
	// 1) with -- (segment 2); a per-segment-only flag scan let `git status
	// && git reset --hard` hide the flag on segment 2. Both were wrong.
	for i := 0; i+1 < len(tokens); i++ {
		if strings.Trim(tokens[i], "\"'") != "git" {
			continue
		}
		sub := tokens[i+1]
		for _, u := range tokens[i+1:] {
			switch u {
			case "&&", "||", "|", "&":
				goto nextGit
			}
			if u == ";" || strings.HasPrefix(u, ";") {
				goto nextGit
			}
			// #2268: strip trailing quote/backslash glue - the JSON-escaped
			// closing quote of `sh -c "git reset --hard"` makes the last
			// token `--hard\"` and the exact compare missed it.
			u = strings.TrimRight(u, "\"'\\")
			switch sub {
			case "reset":
				if u == "--hard" {
					return true
				}
			case "clean":
				// -f may be fused (-fd, -fx...) because '-' is not a
				// token separator (#1194); --force is a separate token
				// (#1490-D).
				if u == "--force" || (len(u) > 1 && u[0] == '-' && u[1] == 'f') {
					return true
				}
			case "checkout":
				if u == "--" {
					return true
				}
			}
		}
	nextGit:
	}
	return false
}

// stripGitGlobalFlagTokens removes the global-flag segment (plus consumed
// values) sitting between `git` and its subcommand. #2255 H1, reversibility layer.
func stripGitGlobalFlagTokens(tokens []string) []string {
	// #2560: explicit index loop, NOT range - the range expression is
	// snapshotted once, so rebuilding `tokens` below left i/t reading the
	// OLD slice while tokens[j]/len(tokens) read the new one. The second
	// git segment's global flags then never stripped (sub anchored on
	// "-C" -> destructive bigram silently missed). The sibling layer
	// (normalizeGitGlobalFlags) uses this exact pattern with a comment
	// calling out the same trap.
	for i := 0; i < len(tokens); i++ {
		if strings.Trim(tokens[i], "\"'") != "git" {
			continue
		}
		j := i + 1
		for j < len(tokens) {
			ft := strings.Trim(tokens[j], "\"'")
			if !isGitGlobalFlag(ft) {
				break
			}
			j++
			if !strings.Contains(ft, "=") && j < len(tokens) {
				j++
			}
		}
		if j > i+1 && j <= len(tokens) {
			// rewrite in place and KEEP SCANNING (#2255 F4): later git
			// occurrences in the same token stream need stripping too.
			// i stays put: git remains at i in the rewritten slice and the
			// loop's i++ advances to the first tail element (old tokens[j]).
			tokens = append(append([]string{}, tokens[:i+1]...), tokens[j:]...)
		}
	}
	return tokens
}
