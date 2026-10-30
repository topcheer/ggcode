package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/util"
)

// VerifyResult is the outcome of an auto-verification check.
type VerifyResult struct {
	Passed bool `json:"passed"`
	// Skipped marks a "pass by amnesty" result (binary absent, exit 127,
	// pytest no-tests exit 5): nothing was actually verified. Kept out of
	// last-known-good checkpoint blessing (#3242) - an unverified tree must
	// not be recorded as a verified baseline that later revert guidance
	// points to.
	Skipped bool     `json:"skipped,omitempty"`
	Errors  []string `json:"errors,omitempty"`
	Command string   `json:"command"`
	Output  string   `json:"output,omitempty"`
}

// --- Auto-verify loop integration ---

const verifyPromptTimeout = 30 * time.Second
const verifyExecuteTimeout = 120 * time.Second

// maxSyncVerifyRetries is the maximum number of automatic verification-and-fix
// cycles within a single agent run. When the agent finishes editing code but
// verification (build/test) fails, the errors are injected back into the
// conversation and the loop continues — up to this many times. This eliminates
// the manual round-trip where the user must say "fix the build" after every
// failed change. Competitors (Claude Code, Aider, Cursor) all have this pattern.
const maxSyncVerifyRetries = 3

// asyncVerify runs build/test verification in the background after the agent
// loop completes. It does NOT block the agent's response — the user sees the
// agent's output immediately, and verification results arrive asynchronously
// via the onVerifyProgress/onVerifyResult callbacks.
//
// If verification fails, errors are injected into the context manager so they're
// available for the next user turn. No automatic retry — the user decides what
// to do with the failure.
func (a *Agent) asyncVerify(ctx context.Context, runStats *RunStats) {
	if !a.autoVerify {
		debug.Log("verify", "post-loop auto-verification disabled; in-loop verification is mandated by the system prompt")
		return
	}
	mode := a.currentMode()
	if mode == permission.PlanMode {
		return
	}

	workingDir := a.WorkingDir()
	if workingDir == "" {
		return
	}

	if !codeChangedInRun(runStats) {
		debug.Log("verify", "skipping: no file-editing tools used in this run")
		return
	}

	a.verifyProgress("Running verification…")

	// Diff-scoped verification (generic, any language/ecosystem): the files
	// changed in this run are known, so ask the verification oracle to pick
	// the NARROWEST command that verifies exactly those files. Language
	// knowledge lives in the model, not in hardcoded ecosystem switches -
	// this works for Go, Python, JS, Rust, or any project. The deterministic
	// whole-repo command (make verify-ci et al) is only a fallback: running a
	// full CI pipeline after every small edit made the inner loop spend most
	// of its wall-clock verifying, and often exceeded the 120s budget.
	cmd := ""
	if len(runStats.FilesEdited) > 0 {
		a.verifyProgress("Choosing scoped verification…")
		cmd = a.llmDecideVerifyCommand(ctx, runStats.FilesEdited)
		if cmd == skipVerifyCommand {
			// #4523: the oracle explicitly decided no verification applies
			// (e.g. markdown-only edits). Do NOT override with the
			// detectBuildSystem fallback.
			debug.Log("verify", "async: skipping - oracle decided no verification applies")
			return
		}
	}
	if cmd == "" {
		cmd = detectBuildSystem(workingDir)
	}
	if cmd == "" {
		// Oracle was already tried (with the changed-file list) when files were
		// known; without a build system and with only non-code edits there is
		// nothing left to try - skip rather than hallucinate a command whose
		// failure pollutes the context and triggers auto-repair loops.
		if len(runStats.FilesEdited) == 0 && !runTouchedCode(runStats) {
			debug.Log("verify", "async: skipping - no build system and only non-code files edited")
			return
		}
		debug.Log("verify", "async: no verification command determined")
		return
	}
	a.verifyProgress(fmt.Sprintf("Running `%s`…", cmd))
	result := a.executeVerifyCommand(ctx, cmd)

	if result.Passed {
		debug.Log("verify", "PASSED: %s", cmd)
		if result.Skipped {
			// #3242: skip-amnesty must not bless a last-known-good baseline -
			// nothing was verified, and revert guidance would point at an
			// unverified snapshot as if it were ground truth.
			debug.Log("verify", "async: skipped result not recorded as last-good")
		} else {
			a.lastGoodCheckpointRecordPass()
		}
		// Build/test passed — run lint as advisory secondary check.
		a.verifyProgress("Running lint check…")
		lintStart := time.Now()
		a.runLintAfterBuild(ctx, workingDir)
		debug.Log("verify", "async: lint completed in %s", time.Since(lintStart))
		a.verifyResult(*result)
		return
	}

	// Verification failed — ratchet: record errors for future rule generation.
	// #3227: shared singleton - per-run instances lost updates.
	rs := a.getRuleStore()
	if rs != nil {
		matched, unmatched := rs.MatchErrors(result.Errors)
		debug.Log("verify", "ratchet: %d matched, %d unmatched", len(matched), len(unmatched))
		if len(unmatched) > 0 {
			newRules := a.generalizeErrorsWithRetry(ctx, unmatched, cmd)
			for _, r := range newRules {
				rs.AddRule(r)
			}
			debug.Log("verify", "ratchet: learned %d new rules", len(newRules))
		}
	}

	debug.Log("verify", "FAILED: %s — %d errors", cmd, len(result.Errors))
	a.lastGoodCheckpointRecordFail()

	// Inject errors into context for the next turn. No auto-retry.
	errorSummary := fmt.Sprintf("Verification failed with the following command:\n```\n%s\n```\n\nErrors:\n", cmd)
	for _, e := range result.Errors {
		errorSummary += fmt.Sprintf("- %s\n", e)
	}
	errorSummary += "\nFix these issues and ensure the build passes."
	if a.verifyRegression != nil {
		transition, regressionSummary := a.verifyRegression.classifyErrorsWithTransition(result.Errors, cmd)
		errorSummary += regressionSummary

		// Feed the per-round transition into the self-correction stability gate.
		// This detects when the correction loop is net-negative (introducing
		// more errors than it fixes) and injects a step-back warning.
		if gateGuidance := a.selfCorrectionGateRecordRound(
			len(transition.newErrors),
			len(transition.persistentErrors),
			transition.resolvedCount,
		); gateGuidance != "" {
			errorSummary += "\n\n" + gateGuidance

			// When the self-correction gate fires, surface the
			// last-known-good checkpoint as ONE option, not a directive -
			// #1492-B: newly-visible errors may be exposure, not
			// regression, and the checkpoint may still contain the
			// original error.
			if checkpointGuidance := a.lastGoodCheckpointGuidance(); checkpointGuidance != "" {
				errorSummary += "\n\nIf (and only if) inspection shows these errors were CAUSED by recent fixes, the last-known-good checkpoint is available as a fallback:\n" + checkpointGuidance
			}
		}
	}
	a.contextManager.Add(provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{{
			Type: "text",
			Text: errorSummary,
		}},
	})

	a.verifyResult(*result)
}

// verifyProgress safely calls the progress callback if set.
func (a *Agent) verifyProgress(text string) {
	a.mu.RLock()
	fn := a.onVerifyProgress
	a.mu.RUnlock()
	if fn != nil {
		fn(text)
	}
}

// verifyResult safely calls the result callback if set.
func (a *Agent) verifyResult(result VerifyResult) {
	a.mu.RLock()
	fn := a.onVerifyResult
	a.mu.RUnlock()
	if fn != nil {
		fn(result)
	}
}

// buildVerifyContext gathers project-specific hints to help the verification
// oracle choose the right command. This is language- and tool-agnostic:
// it checks for common build system files and Makefile targets so the oracle
// knows what's available, without hardcoding any single ecosystem.
func buildVerifyContext(workingDir string) string {
	var hints []string

	// Check for Makefile with common targets (works for any language)
	makefile := filepath.Join(workingDir, "Makefile")
	if data, err := os.ReadFile(makefile); err == nil {
		content := string(data)
		var targets []string
		for _, t := range []string{"test", "verify", "check", "lint", "ci", "build"} {
			// #3801 A: bare strings.Contains(t+":") matches LONGER targets'
			// prefixes (Makefile with only "unit-test:" claimed a phantom
			// `test` target) and variable assignments. detectBuildSystem's
			// hasMakeTarget already anchors at line start, skips comments
			// and `=`/`:=` assignments - reuse it instead of a weaker copy.
			if hasMakeTarget(content, t) {
				targets = append(targets, t)
			}
		}
		if len(targets) > 0 {
			hints = append(hints, fmt.Sprintf("Available make targets: %s (e.g. 'make %s')", strings.Join(targets, ", "), targets[0]))
		}
	}

	// Detect common build systems
	checks := []struct {
		file  string
		label string
		cmd   string
	}{
		{"package.json", "npm", "npm test"},
		{"pyproject.toml", "Python", "pytest"},
		{"setup.py", "Python", "pytest"},
		{"Cargo.toml", "Rust", "cargo test"},
		{"go.mod", "Go", "go test ./..."},
		{"CMakeLists.txt", "CMake", "cmake --build . && ctest"},
		{"pubspec.yaml", "Flutter/Dart", "flutter test"},
		{"mix.exs", "Elixir", "mix test"},
		{"Gemfile", "Ruby", "bundle exec rake test"},
	}
	for _, c := range checks {
		if _, err := os.Stat(filepath.Join(workingDir, c.file)); err == nil {
			hints = append(hints, fmt.Sprintf("Project uses %s (likely: '%s')", c.label, c.cmd))
			break
		}
	}

	if len(hints) == 0 {
		return ""
	}
	return "Project context:\n" + strings.Join(hints, "\n")
}

// buildVerifyOraclePrompt constructs the verification oracle's system text:
// scoping rules plus the run's changed-file list (capped at 40 entries,
// overflow summarized by directory so the model still sees the blast radius
// without flooding the call).
func buildVerifyOraclePrompt(changedFiles []string) string {
	sysText := `You are a verification oracle. Given the files changed in this run and the recent conversation, determine the single most appropriate build/test/lint command to verify EXACTLY those changes. Output ONLY the command (no explanation, no markdown fences). If no verification is needed or possible, output exactly "SKIP".

Scoping rules - verification must be proportional to the change:
- Prefer the NARROWEST command that exercises the changed code: package- or directory-scoped test selectors (e.g. ` + "`go test ./internal/foo/...`" + `), related-test finders (e.g. ` + "`jest --findRelatedTests <changed files>`" + `, ` + "`vitest related <files>`" + `), or the specific changed test files (e.g. ` + "`pytest tests/test_changed.py`" + `).
- Include the project's required build flags (build tags, env) if the project context below documents them.
- Whole-repo commands (CI pipelines, ` + "`make verify-ci`" + `, ` + "`go test ./...`" + `, ` + "`npm test`" + ` run-everything) are ONLY appropriate when the change genuinely affects the whole repo (root config, build system files, dependency manifests). Never choose them for ordinary code edits.
- Docs/markdown-only changes need no verification: answer SKIP.`

	if len(changedFiles) == 0 {
		return sysText
	}
	const maxListed = 40
	listed := changedFiles
	summary := ""
	if len(listed) > maxListed {
		dirs := map[string]bool{}
		for _, f := range listed[maxListed:] {
			dirs[filepath.Dir(f)] = true
		}
		listed = listed[:maxListed]
		dsl := make([]string, 0, len(dirs))
		for d := range dirs {
			dsl = append(dsl, d+"/")
		}
		sort.Strings(dsl)
		summary = fmt.Sprintf("\n  ... and %d more files under: %s", len(changedFiles)-maxListed, strings.Join(dsl, ", "))
	}
	sysText += "\n\nFiles changed in this run:\n"
	for _, f := range listed {
		sysText += "  - " + f + "\n"
	}
	return sysText + summary
}

// skipVerifyCommand is the sentinel llmDecideVerifyCommand returns for an
// EXPLICIT oracle SKIP decision (#4523) - distinct from "" (no decision
// obtained: provider failure/empty output), so callers do not override a
// deliberate "no verification applies" with the detectBuildSystem
// fallback (a full make verify-ci on a docs-only change).
const skipVerifyCommand = "\x00SKIP"

// llmDecideVerifyCommand asks the LLM to output a single verification command
// for the CHANGED FILES of the current run. Returns empty string if no
// verification is needed or the oracle is unavailable.
//
// The oracle is explicitly instructed to scope the command to the changed
// files (package/dir-level test selectors, related-test finders) and to
// avoid whole-repo commands unless the change genuinely warrants them.
// This keeps inner-loop verification proportional to the change: a one-file
// edit gets a one-package test run, not the full CI pipeline.
func (a *Agent) llmDecideVerifyCommand(ctx context.Context, changedFiles []string) string {
	a.mu.RLock()
	prov := a.provider
	a.mu.RUnlock()

	if prov == nil {
		return ""
	}

	promptCtx, cancel := context.WithTimeout(ctx, verifyPromptTimeout)
	defer cancel()

	msgs := a.contextManager.Messages()
	start := 0
	if len(msgs) > 6 {
		start = len(msgs) - 6
	}
	recent := msgs[start:]

	// Build project-aware context for the verification oracle.
	// This replaces hardcoded build-tag injection with a general approach:
	// the oracle gets project memory (GGCODE.md, which documents required
	// build flags) + build system hints (Makefile targets, detected ecosystem).
	workingDir := a.WorkingDir()
	sysText := buildVerifyOraclePrompt(changedFiles)

	// Include project memory excerpt (build/validation instructions)
	fullSys := a.SystemPrompt()
	if fullSys != "" {
		// Extract just the build/validation relevant sections — avoid sending
		// the entire multi-KB system prompt to keep this LLM call fast.
		for _, marker := range []string{"Build & Validation", "Build", "Validation", "Testing", "Quick Reference"} {
			if idx := strings.Index(fullSys, marker); idx >= 0 {
				end := strings.Index(fullSys[idx:], "\n## ")
				if end < 0 {
					end = len(fullSys) - idx
					if end > 1500 {
						end = 1500
					}
				}
				sysText += "\n\n" + fullSys[idx:idx+end]
				break
			}
		}
	}

	// Add project build context hints
	if ctx := buildVerifyContext(workingDir); ctx != "" {
		sysText += "\n\n" + ctx
	}

	verifierMsgs := append([]provider.Message{
		{
			Role: "system",
			Content: []provider.ContentBlock{{
				Type: "text",
				Text: sysText,
			}},
		},
	}, recent...)

	verifierMsgs = append(verifierMsgs, provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{{
			Type: "text",
			Text: "What single command should be run to verify the changes above? Output only the command or SKIP.",
		}},
	})

	resp, err := prov.Chat(promptCtx, verifierMsgs, nil)
	if err != nil || resp == nil {
		debug.Log("verify", "LLM decide command failed: %v", err)
		return ""
	}
	a.emitUsageWithSource(resp.Usage, "verify")

	cmd := stripCodeFence(strings.TrimSpace(extractText(resp.Message)))
	if strings.EqualFold(cmd, "SKIP") {
		// #4523: an explicit SKIP is a DECISION (e.g. markdown-only edits
		// need no build/test) - surfacing the sentinel keeps the callers
		// from overriding it with the detectBuildSystem fallback (a full
		// repo make verify-ci on a docs change). Provider failure and empty
		// output still return "" (no decision obtained → fallback is right).
		return skipVerifyCommand
	}
	if cmd == "" {
		return ""
	}

	// Reject dangerous commands using the comprehensive DangerousDetector
	// (same detector used by the permission system). This replaces the previous
	// naive substring matching which missed many dangerous patterns (e.g.,
	// "git clean -fdx", "find . -delete", "chmod 777", fork bombs, etc.).
	detector := permission.NewDangerousDetector()
	if detector.IsDangerous(cmd) {
		debug.Log("verify", "LLM proposed dangerous command, rejecting: %s", cmd)
		return ""
	}

	return cmd
}

// stripCodeFence removes a markdown code fence wrapper the LLM may add
// despite the "output only the command" instruction (#1522 case C): a
// fenced command reached the pre-flight LookPath as a 3-backtick prefix,
// failed, and came back Passed=true "verification skipped" - a silent
// false green for every fenced oracle answer.
//
// #3743: the old prefix-strip + LastIndex-truncate pipeline broke on the
// prose-then-fence shape common for non-English models ("建议运行：\n```bash
// ..."). The LastIndex hit the CLOSING fence, leaving "prose + opening
// fence" behind; the first-line cut then reduced it to the bare prose, which
// leaked into executeVerifyCommand, failed LookPath, got skip-amnestied to
// Passed=true, AND bypassed the deterministic fallback (a non-empty string
// was returned). Now, whenever a fence opener appears ANYWHERE, the command
// is the content of the LAST fenced block - prose before it is discarded.
// isFirstTokenLangTag reports whether s's first whitespace-delimited token
// is a Markdown code-fence language tag rather than a command verb (#3801 B).
// "go" is deliberately ABSENT: in a single-line fence like ```go test ./x/`
// it is the compiler command, not the Go language tag.
func isFirstTokenLangTag(s string) bool {
	sp := strings.IndexAny(s, " \t")
	if sp < 0 {
		return false
	}
	switch strings.ToLower(s[:sp]) {
	case "bash", "sh", "shell", "zsh", "console", "shell-session",
		"python", "python3", "py", "ruby", "node", "js", "javascript",
		"typescript", "ts", "java", "rust", "c", "cpp", "sql", "yaml",
		"json", "makefile", "dockerfile":
		return true
	}
	return false
}

func stripCodeFence(s string) string {
	t := strings.TrimSpace(s)
	// Collect every fence marker; fences pair left-to-right (opener, closer).
	var fences []int
	for i := 0; i+3 <= len(t); {
		if j := strings.Index(t[i:], "```"); j >= 0 {
			fences = append(fences, i+j)
			i += j + 3
		} else {
			break
		}
	}
	if n := len(fences); n > 0 {
		var block string
		// Fences pair sequentially: f0/f1, f2/f3, ... (opener, closer).
		// Use the LAST COMPLETE pair: a trailing ODD fence is stray noise
		// (prose mentioning ``` or an unclosed aside), not a block
		// boundary; pairing it with the preceding closer leaked
		// inter-block prose (#3743 review follow-up).
		if n >= 2 {
			closerIdx := n - 1 - n%2 // n even -> n-1; n odd -> n-2
			openerIdx := closerIdx - 1
			block = t[fences[openerIdx]+3 : fences[closerIdx]]
		} else {
			// Single fence: an opener with unterminated content (prose
			// before it, command after - the #3743 shape), or trailing
			// noise after a bare command. Either way the command, if any,
			// is what follows the fence's language-tag line.
			block = t[fences[0]+3:]
		}
		if nl := strings.IndexByte(block, '\n'); nl >= 0 {
			block = block[nl+1:] // drop the language-tag line
		} else if sp := strings.IndexAny(block, " \t"); sp >= 0 {
			// #3801 B: single-LINE fence (```go test ./x/```): there is no
			// language-tag LINE. If the first token is a known tag, strip
			// it and the remainder IS the command; if not (```go test ./x/`
			// - "go" is also a command verb), the whole content is the
			// command. Blank it and the oracle's scoped command silently
			// fell back to whole-repo verification.
			if isFirstTokenLangTag(block) {
				block = strings.TrimSpace(block[sp+1:])
			}
		} else {
			block = "" // lang-only / trailing fence: no command, use fallback
		}
		t = block
	}
	// Single line only - the oracle is asked for ONE command.
	if nl := strings.IndexByte(t, '\n'); nl >= 0 {
		t = t[:nl]
	}
	return strings.TrimSpace(t)
}

// executeVerifyCommand runs the command and returns the result.
// If the command's binary is not available on the system (exit 127 scenario),
// it returns a Passed=true result with a skipped note instead of capturing
// "command not found" as a verification failure. This prevents pointless
// error injection and auto-repair loops when the host lacks the required
// toolchain (e.g. no `go`, `make`, `cargo` in PATH).
func (a *Agent) executeVerifyCommand(ctx context.Context, command string) *VerifyResult {
	workingDir := a.WorkingDir()

	// Pre-flight: check if the command's binary is available on PATH.
	// This prevents "command not found" output from being captured as a
	// verification failure and injected into the LLM context.
	if !verifyCommandAvailable(command) {
		debug.Log("verify", "skipping: binary not available for command: %s", command)
		return &VerifyResult{
			Command: command,
			Passed:  true, // treat as pass (skip) to avoid error injection
			Skipped: true, // #3242: amnesty, not a verified pass
			Output:  "verification skipped: tool not available on this system",
		}
	}

	debug.Log("verify", "running: %s in %s", command, workingDir)

	cmdCtx, cancel := context.WithTimeout(ctx, verifyExecuteTimeout)
	defer cancel()

	cmd, _, err := util.NewShellCommandContext(cmdCtx, command)
	if err != nil {
		// Fallback to sh for non-Windows
		cmd = exec.CommandContext(cmdCtx, "sh", "-c", command)
	}
	cmd.Dir = workingDir
	// Kill the entire process group on timeout to prevent orphaned children
	// from keeping stdout/stderr pipes open (which would make CombinedOutput hang).
	configureVerifyCommand(cmd)
	cmd.WaitDelay = 5 * time.Second
	output, err := cmd.CombinedOutput()
	outputStr := string(output)

	result := &VerifyResult{
		Command: command,
		Output:  truncStr(outputStr, 2000),
	}

	if err != nil {
		// Defense-in-depth: classify "tooling absent / nothing to verify"
		// exits as skip rather than code failure:
		//   127 - command not found (binary vanished despite LookPath)
		//   5   - pytest "no tests collected" (project simply has no tests;
		//         was misread as a failure and triggered auto-repair loops)
		if exitErr, ok := err.(*exec.ExitError); ok && isNonFailureExit(command, exitErr.ExitCode()) {
			debug.Log("verify", "skipping: non-failure exit %d: %s", exitErr.ExitCode(), command)
			return &VerifyResult{
				Command: command,
				Passed:  true,
				Skipped: true, // #3242: amnesty, not a verified pass
				// #3242: exit 5 (pytest "no tests collected") and 127 (binary
				// vanished) land here together - say what actually happened
				// instead of the copy-pasted "tool not available".
				Output: fmt.Sprintf("verification skipped: non-failure exit %d (tool unavailable or no tests collected)", exitErr.ExitCode()),
			}
		}
		result.Passed = false
		result.Errors = extractErrorLines(outputStr)
		if len(result.Errors) == 0 {
			// #4607: marker-free failure output ("signal: killed", segfault,
			// OOM) extracts zero lines via the substring gate above, leaving
			// the auto-repair loop with only "exit status 1" - zero information
			// (and ratchet MatchErrors cannot learn a pattern from it either).
			// Attach the truncated raw output tail (same 2000-char budget as
			// result.Output) so both errorSummary consumers inject the real
			// failure text.
			fallback := fmt.Sprintf("command failed: %v", err)
			if tail := truncStr(strings.TrimSpace(outputStr), 2000); tail != "" {
				fallback += "\noutput tail:\n" + tail
			}
			result.Errors = []string{fallback}
		}
		debug.Log("verify", "FAILED: %d errors", len(result.Errors))
	} else {
		result.Passed = true
		debug.Log("verify", "PASSED")
	}

	return result
}

// isNonFailureExit reports whether a command's exit code means "nothing to
// verify / tooling absent" rather than "the code is broken". Treating these
// as failures injects bogus errors and spins auto-repair loops in projects
// that simply lack tests or the toolchain.
func isNonFailureExit(command string, code int) bool {
	// #4638: compound detection must be SUBSTRING-based. ContainsAny takes a
	// char SET, so "||;&&" meant any single | & ; - and the most common
	// verification shape (`go test ./... 2>&1`, single pipes, bash &>
	// redirects) lost the not-found/pytest amnesty, turned "verification
	// skipped" into FAILED and burned 3-4 doomed auto-repair rounds.
	isCompound := strings.Contains(command, "||") || strings.Contains(command, "&&") || strings.Contains(command, ";")
	if code == 127 {
		// Command not found. #1522 case B: only a MISSING LEADING binary is
		// "nothing to verify" - `go test ./... || npm run build` with npm
		// absent exits 127 from the FALLBACK side while the real test run
		// already failed; blanket 127 amnesty turned that into Passed=true
		// ("verification passed") with a genuine test failure swallowed.
		// Compound commands (||, &&, ;) lose the amnesty: we cannot tell
		// which leg exited 127 without shell semantics.
		if isCompound {
			return false
		}
		return true
	}
	// pytest exit 5: no tests were collected - not a code defect.
	// #4402: same compound-command guard as the 127 branch above (#1522
	// case B) - `go test ./pkg; pytest` with a go compile failure exits 5
	// from the pytest tail (0 collected); without the guard the real
	// failure was swallowed into Passed=true ("silent green chain").
	if isCompound {
		return false
	}
	return code == 5 && strings.Contains(command, "pytest")
}

// zeroFailSummary reports a passing "0 failed"/"0 failures" summary
// with a left digit boundary, so real failure summaries like "10 failed,
// 2 passed" (or 20/100...) are not swallowed by the #4403 success-
// negation filter (#4415 V1).
func zeroFailSummary(lower string) bool {
	for _, pat := range []string{"0 failed", "0 failures"} {
		idx := 0
		for {
			i := strings.Index(lower[idx:], pat)
			if i < 0 {
				break
			}
			at := idx + i
			if at == 0 || (lower[at-1] < '0' || lower[at-1] > '9') {
				return true
			}
			idx = at + 1
		}
	}
	return false
}

// isNegationProse reports whether the phrase occurs as a standalone
// success assertion (word-boundary "no error(s)"/"without error(s)"),
// NOT inside a larger failure message. #4555: pytest assertions like
// "AssertionError: expected error, got no error" carry "no error" as
// part of the failure context and must survive the noise filter; only
// lines where the phrase stands as its own sentence/summary ("no errors",
// "completed without error", "exit without error: clean") are noise.
// Word boundary + left colon/quote independence keeps "got no error"
// (phrase preceded by other prose words) out of the exemption.
// isNegationProseAt reports whether the phrase occurs with word
// boundaries as its own word sequence (optionally pluralized: "no errors",
// "without errors"). #4555: bare strings.Contains swallowed pytest
// assertion lines like "AssertionError: expected error, got no error".
func isNegationProseAt(lower, phrase string) bool {
	idx := 0
	for {
		i := strings.Index(lower[idx:], phrase)
		if i < 0 {
			return false
		}
		at := idx + i
		end := at + len(phrase)
		// Absorb a trailing plural so "no errors" still counts.
		if end < len(lower) && lower[end] == 's' {
			end++
		}
		// Left boundary: start of line, or a non-letter (space/punct).
		leftOK := at == 0 || !sdIsLowerAZ(lower[at-1])
		// Right boundary: end of line, or a non-letter.
		rightOK := end >= len(lower) || !sdIsLowerAZ(lower[end])
		if leftOK && rightOK {
			return true
		}
		idx = at + 1
	}
}

// carriesFailureContext reports whether the line carries failure-message
// context around an otherwise-clean negation phrase, marking it as
// assertion/prose content rather than a standalone success summary (#4555).
func carriesFailureContext(lower string) bool {
	for _, m := range []string{"assert", "expected", "got ", "runtimeerror", "exception"} {
		if strings.Contains(lower, m) {
			return true
		}
	}
	return false
}

// looksLikeDiffBody reports whether a "-"-prefixed line is a unified diff
// deletion body (e.g. "-errors.New(...)") rather than a flag echo (#4555).
func looksLikeDiffBody(trimmed string) bool {
	// Diff deletion bodies contain code punctuation typical of source lines:
	// balanced call parens, quoted strings, or dot-qualified identifiers.
	if strings.Count(trimmed, "(") > 0 && strings.Count(trimmed, ")") > 0 {
		return true
	}
	return strings.Contains(trimmed, "\"") || strings.Contains(trimmed, ".")
}

func sdIsLowerAZ(b byte) bool { return b >= 'a' && b <= 'z' }

// extractErrorLines pulls likely error lines from build/test output.
func extractErrorLines(output string) []string {
	var errors []string
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)
		// #4403: bare substring matching pulled success negations and flag
		// echoes into the ratchet learning pipeline: "0 failed, 3 passed"
		// summaries from passing packages, "without error" prose, and
		// "--fail-fast" argument echoes all generalized into bogus rules.
		// #4415 V1: the first cut over-filtered - "0 failed" as a bare
		// substring also swallowed "10 failed, 2 passed" (and 20/100...),
		// and the HasPrefix("-") flag filter swallowed go test's standard
		// "--- FAIL: TestX" per-test failure lines (only the package-level
		// FAIL summary survived). Left-digit-boundary the zero counts and
		// keep "--- " test-output markers out of the flag-echo filter.
		if zeroFailSummary(lower) {
			// #4555: "0 failed, 1 errored" merged-count lines lost the
			// errored count to whole-line dropping; keep the line and let
			// the per-segment filter below decide (segments without a real
			// failure signal simply don't match the capture predicates).
			continue
		}
		if (isNegationProseAt(lower, "no error") || isNegationProseAt(lower, "without error")) &&
			!carriesFailureContext(lower) {
			continue
		}
		if strings.HasPrefix(trimmed, "-") && !strings.HasPrefix(trimmed, "--- ") &&
			!looksLikeDiffBody(trimmed) &&
			(strings.Contains(lower, "fail") || strings.Contains(lower, "error")) {
			// flag/argument echo (e.g. "--fail-fast", "-errorflags"), not
			// command output. "--- FAIL:"/"--- PASS:" test markers carry a
			// space and are real output; diff deletion bodies like
			// "-errors.New(...)" are unified-diff context, not echoes (#4555).
			continue
		}
		if strings.Contains(lower, "error") || strings.Contains(lower, "fail") ||
			strings.Contains(lower, "undefined:") || strings.Contains(lower, "cannot find") ||
			strings.Contains(lower, "panic:") || strings.Contains(lower, "fatal") {
			if len(errors) < 10 {
				errors = append(errors, truncStr(trimmed, 300))
			}
		}
	}
	return errors
}

// extractText pulls text content from a message.
func extractText(msg provider.Message) string {
	for _, b := range msg.Content {
		if b.Type == "text" {
			return b.Text
		}
	}
	return ""
}

// SetAutoVerify enables/disables the post-loop automatic verification pass.
// Disabled by default; wired from config verify.auto_after_run.
func (a *Agent) SetAutoVerify(enabled bool) {
	a.mu.Lock()
	a.autoVerify = enabled
	a.mu.Unlock()
}

// SetClaimsSupervision enables/disables the success-claim heuristic
// detector family (premature_success, success_declare, unverified_claim,
// phantom_verify). Disabled by default; wired from config
// verify.claims_supervision.
func (a *Agent) SetClaimsSupervision(enabled bool) {
	a.mu.Lock()
	a.claimsSupervision = enabled
	a.mu.Unlock()
}

// ClaimsSupervisionEnabled reports whether the success-claim heuristic
// detector family is enabled. Default false.
func (a *Agent) ClaimsSupervisionEnabled() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.claimsSupervision
}

// AutoVerifyEnabled reports whether the post-loop automatic verification
// pass is enabled for this agent. Subagent invariant tests rely on it:
// only the main interactive/pipe agents are ever wired to the config, so
// every other agent instance must report false.
func (a *Agent) AutoVerifyEnabled() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.autoVerify
}

// --- Rule injection into tool results ---

// maxRuleInjectPerSession caps how many times the same rule can be injected
// into tool results within a single session. This prevents repetitive noise
// where the same hint (e.g. "use -tags goolm") appears on every go build call.
const maxRuleInjectPerSession = 2

// maxRulesPerInjection caps how many distinct rules may ride on ONE tool
// result (#4293). Beyond this the block stops being a targeted hint and
// becomes context noise that trains the agent to ignore it.
const maxRulesPerInjection = 3

// injectRulesIntoResult prepends matching rules to a tool result.
// Rules are matched via two paths:
//  1. Preventive: MatchingRulesForTool matches rule patterns against tool ARGS
//     (warns before the error occurs, e.g. "go build" without -tags goolm).
//  2. Reactive: MatchingRulesForResult matches rule patterns against the tool
//     RESULT content (warns when a known error pattern appears in the output,
//     e.g. "cannot find package olm" -> injects the fix hint immediately).
//
// Dedup: each rule ID is injected at most maxRuleInjectPerSession times per
// session to avoid repetitive noise on successful operations.
func (a *Agent) injectRulesIntoResult(toolName string, args json.RawMessage, resultContent string) string {
	workingDir := a.WorkingDir()
	if workingDir == "" {
		return resultContent
	}
	rs := a.getRuleStore()
	if rs == nil {
		return resultContent
	}

	preventive := rs.MatchingRulesForTool(toolName, string(args))
	reactive := rs.MatchingRulesForResult(resultContent, string(args))
	matching := mergeRuleSets(preventive, reactive)
	if len(matching) == 0 {
		return resultContent
	}

	// Dedup: filter out rules that have already been injected enough times.
	a.mu.Lock()
	if a.ruleInjectCount == nil {
		a.ruleInjectCount = make(map[string]int)
	}
	var filtered []Rule
	for _, r := range matching {
		if len(filtered) >= maxRulesPerInjection {
			break
		}
		if a.ruleInjectCount[r.ID] < maxRuleInjectPerSession {
			filtered = append(filtered, r)
			a.ruleInjectCount[r.ID]++
		}
	}
	a.mu.Unlock()

	if len(filtered) == 0 {
		return resultContent
	}

	var b strings.Builder
	b.WriteString("[Rules — learned from past mistakes]\n")
	for _, r := range filtered {
		b.WriteString(fmt.Sprintf("\u26a0 %s\n", r.Rule))
		if r.FixHint != "" {
			b.WriteString(fmt.Sprintf("  \u2192 %s\n", r.FixHint))
		}
	}
	b.WriteString("\n")
	b.WriteString(resultContent)
	return b.String()
}

// syncVerifyAndGate runs verification synchronously before the agent loop
// returns. This implements the "fix-on-fail" pattern: when code was changed,
// build/test is executed inline. If it fails and retry budget remains, the
// errors are injected into the conversation context and the method returns
// true to signal the caller to continue the loop (auto-repair). If it passes
// or the budget is exhausted, it returns false to signal "proceed to return."
//
// When verification passes, the caller should skip the redundant async
// verification. When it fails with budget exhausted, async verify still runs
// (in the defer) for ratchet rule learning.
//
// retryCount is the number of sync verify retries already consumed (0 on the
// first check). The method returns:
//   - (false, true)  — verification PASSED; proceed to return, skip async verify
//   - (true,  false) — verification failed, errors injected; continue the loop
//   - (false, false) — verification not needed, no command, or budget exhausted;
//     proceed to return (async verify may still run for reporting)
//
// #953: the passed flag distinguishes "passed" from "budget exhausted / not
// needed". The caller uses it to skip redundant async verify whenever
// verification passes — including passes on a retry round after auto-repair
// (the old design only skipped first-attempt passes).
func (a *Agent) syncVerifyAndGate(ctx context.Context, runStats *RunStats, retryCount int) (shouldContinue, passed bool) {
	if !a.autoVerify {
		debug.Log("verify", "post-loop auto-verification disabled; in-loop verification is mandated by the system prompt")
		return false, false
	}
	workingDir := a.WorkingDir()
	if workingDir == "" {
		return false, false
	}

	if !codeChangedInRun(runStats) {
		return false, false
	}

	// Determine verification command — scoped oracle first (it knows which
	// files changed and can pick a proportional command for any ecosystem),
	// deterministic whole-repo detection as fallback.
	cmd := ""
	if len(runStats.FilesEdited) > 0 {
		a.verifyProgress("Choosing scoped verification…")
		start := time.Now()
		cmd = a.llmDecideVerifyCommand(ctx, runStats.FilesEdited)
		debug.Log("verify", "sync: oracle chose verify command in %s: %q", time.Since(start), cmd)
		if cmd == skipVerifyCommand {
			// #4523: explicit oracle SKIP - same exit as "no command
			// determined", but WITHOUT the detectBuildSystem override.
			debug.Log("verify", "sync: skipping - oracle decided no verification applies")
			return false, false
		}
	}
	if cmd == "" {
		cmd = detectBuildSystem(workingDir)
		if cmd != "" {
			debug.Log("verify", "sync: oracle empty, using deterministic command: %s", cmd)
		}
	}
	if cmd == "" {
		// Same non-code gate as asyncVerify: without a build system and with
		// only docs/notes edited, nothing left to try - skip rather than risk
		// a bogus failure-injection loop. (The oracle already ran if any files
		// were known.)
		if len(runStats.FilesEdited) == 0 && !runTouchedCode(runStats) {
			debug.Log("verify", "sync: skipping - no build system and only non-code files edited")
			return false, false
		}
		debug.Log("verify", "sync: no verification command determined")
		return false, false
	}

	if retryCount == 0 {
		a.verifyProgress(fmt.Sprintf("Running verification: `%s`…", cmd))
	} else {
		a.verifyProgress(fmt.Sprintf("Re-running verification: `%s` (attempt %d/%d)…", cmd, retryCount+1, maxSyncVerifyRetries+1))
	}
	debug.Log("verify", "sync: running '%s' (retry %d/%d)", cmd, retryCount, maxSyncVerifyRetries)

	result := a.executeVerifyCommand(ctx, cmd)

	if result.Passed {
		debug.Log("verify", "sync: PASSED")
		if result.Skipped {
			// #3242: skip-amnesty must not bless a last-known-good baseline.
			debug.Log("verify", "sync: skipped result not recorded as last-good")
		} else {
			a.lastGoodCheckpointRecordPass()
		}
		// Build/test passed -- run lint as advisory secondary check.
		// Warnings are injected into context for the agent to fix.
		a.verifyProgress("Running lint check…")
		lintStart := time.Now()
		a.runLintAfterBuild(ctx, workingDir)
		debug.Log("verify", "sync: lint completed in %s", time.Since(lintStart))
		a.verifyResult(*result)
		return false, true // proceed to return — caller skips async verify
	}

	// Verification failed.
	debug.Log("verify", "sync: FAILED: %d errors", len(result.Errors))
	a.lastGoodCheckpointRecordFail()

	// Ratchet: learn rules from the errors (same as async path).
	// #3227: shared singleton - per-run instances lost updates.
	rs := a.getRuleStore()
	if rs != nil {
		matched, unmatched := rs.MatchErrors(result.Errors)
		debug.Log("verify", "sync ratchet: %d matched, %d unmatched", len(matched), len(unmatched))
		if len(unmatched) > 0 {
			newRules := a.generalizeErrorsWithRetry(ctx, unmatched, cmd)
			for _, r := range newRules {
				rs.AddRule(r)
			}
			debug.Log("verify", "sync ratchet: learned %d new rules", len(newRules))
		}
	}

	// Budget exhausted — report and let the caller proceed to return.
	// The async verify in the defer will run for final reporting.
	if retryCount >= maxSyncVerifyRetries {
		debug.Log("verify", "sync: budget exhausted (%d/%d), proceeding to return", retryCount, maxSyncVerifyRetries)
		a.verifyProgress(fmt.Sprintf("Verification still failing after %d attempts — please review manually.", maxSyncVerifyRetries+1))
		return false, false
	}

	// Budget remains — inject errors into context and signal continue.
	a.verifyProgress(fmt.Sprintf("Verification failed — auto-repairing (attempt %d/%d)…", retryCount+1, maxSyncVerifyRetries))

	errorSummary := fmt.Sprintf("Verification failed with the following command:\n```\n%s\n```\n\nErrors:\n", cmd)
	for _, e := range result.Errors {
		errorSummary += fmt.Sprintf("- %s\n", e)
	}
	errorSummary += "\nFix these issues and ensure the build passes."
	if a.verifyRegression != nil {
		transition, regressionSummary := a.verifyRegression.classifyErrorsWithTransition(result.Errors, cmd)
		errorSummary += regressionSummary

		// Feed the per-round transition into the self-correction stability gate.
		// This detects when the correction loop is net-negative (introducing
		// more errors than it fixes) and injects a step-back warning.
		if gateGuidance := a.selfCorrectionGateRecordRound(
			len(transition.newErrors),
			len(transition.persistentErrors),
			transition.resolvedCount,
		); gateGuidance != "" {
			errorSummary += "\n\n" + gateGuidance

			// When the self-correction gate fires, surface the
			// last-known-good checkpoint as ONE option, not a directive -
			// #1492-B: newly-visible errors may be exposure, not
			// regression, and the checkpoint may still contain the
			// original error.
			if checkpointGuidance := a.lastGoodCheckpointGuidance(); checkpointGuidance != "" {
				errorSummary += "\n\nIf (and only if) inspection shows these errors were CAUSED by recent fixes, the last-known-good checkpoint is available as a fallback:\n" + checkpointGuidance
			}
		}
	}
	a.contextManager.Add(provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{{
			Type: "text",
			Text: errorSummary,
		}},
	})

	return true, false // continue the loop for auto-repair
}
