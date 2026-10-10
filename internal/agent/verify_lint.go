package agent

// verify_lint.go implements static analysis (linting) as a secondary verification
// step after build/test passes. While the primary verify loop catches compilation
// errors and test failures, linting catches subtle issues that compilers miss:
//
//   - go vet: printf format mismatches, unreachable code, struct tag issues,
//     lock copies, impossible comparisons, shadowed returns, etc.
//   - cargo clippy: idiomatic Rust issues, common performance pitfalls
//   - ruff/flake8: unused imports, style issues, potential bugs
//   - eslint: unused vars, type issues, best practice violations
//
// Competitor mapping:
//   - Claude Code: runs `go vet` as part of its default post-edit checks
//   - Cursor: shows lint warnings inline and offers auto-fixes
//   - Cline: runs linters in its verification loop
//   - Aider: supports `--lint` flag for pre/post-edit linting
//
// Design: lint runs AFTER build passes, not before. Build errors are higher
// priority and must be fixed first. Lint warnings are advisory — they don't
// block the agent from completing, but they are injected into context so the
// agent can fix them in the same turn (sync) or next turn (async).

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

// lintTimeout is the maximum wall-clock time for a lint run.
const lintTimeout = 60 * time.Second

// lintMaxWarnings caps the number of warnings injected into the agent context
// to avoid flooding it in projects with large pre-existing lint debt.
const lintMaxWarnings = 15

// LintResult holds the outcome of a lint run.
type LintResult struct {
	Command  string   `json:"command"`
	Warnings []string `json:"warnings,omitempty"`
	Passed   bool     `json:"passed"`
}

// detectLintCommand returns the appropriate lint command for the project type.
// Priority: Makefile `lint` target > language-specific linters.
//
// Returns empty string if no suitable linter is found.
func detectLintCommand(workingDir string) string {
	if workingDir == "" {
		return ""
	}

	// 1. Makefile with a lint target — the project's authoritative lint config.
	// This is preferred because it includes build tags, env vars, and excludes.
	for _, mf := range []string{"Makefile", "makefile", "GNUmakefile"} {
		path := filepath.Join(workingDir, mf)
		if data, err := os.ReadFile(path); err == nil {
			if hasMakeTarget(string(data), "lint") {
				return "make lint"
			}
		}
	}

	// 2. Language-specific defaults.
	if fileExists(filepath.Join(workingDir, "go.mod")) {
		// #3752: carry the detected build tags into go vet. Without them,
		// tag-gated code fails to compile under vet and those COMPILE errors
		// were extracted as lint warnings, sending the agent chasing phantom
		// lint issues right after a green build. detectGoBuildTags reads the
		// Makefile's -tags flag (with variable expansion); projects without
		// a Makefile have no known tags and keep the plain command.
		if tags := detectGoBuildTags(workingDir); len(tags) > 0 {
			return "go vet -tags " + strings.Join(tags, ",") + " ./..."
		}
		return "go vet ./..."
	}
	if fileExists(filepath.Join(workingDir, "Cargo.toml")) {
		return "cargo clippy"
	}
	// Python: ruff if config present (fast, modern linter)
	if fileExists(filepath.Join(workingDir, "ruff.toml")) ||
		fileExists(filepath.Join(workingDir, ".ruff.toml")) {
		return "ruff check ."
	}
	if fileExists(filepath.Join(workingDir, "pyproject.toml")) ||
		fileExists(filepath.Join(workingDir, "setup.py")) {
		// Best effort — ruff may not be installed
		return "ruff check ."
	}
	// JS/TS: eslint if config exists
	for _, cfg := range []string{
		".eslintrc.js", ".eslintrc.json", ".eslintrc.yml",
		".eslintrc.cjs", "eslint.config.js", "eslint.config.mjs",
	} {
		if fileExists(filepath.Join(workingDir, cfg)) {
			return "npx eslint ."
		}
	}

	return ""
}

// runLintCheck executes the lint command and returns warnings.
// Returns nil if no lint command is available or the linter is not installed.
func (a *Agent) runLintCheck(ctx context.Context, workingDir string) *LintResult {
	cmd := detectLintCommand(workingDir)
	if cmd == "" {
		return nil
	}

	// Quick check: is the linter actually available? Avoid cryptic errors
	// when the tool isn't installed (common in minimal CI environments).
	if !lintCommandAvailable(workingDir, cmd) {
		debug.Log("verify-lint", "linter not available, skipping: %s", cmd)
		return nil
	}

	lintCtx, cancel := context.WithTimeout(ctx, lintTimeout)
	defer cancel()

	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return nil
	}
	execCmd := exec.CommandContext(lintCtx, parts[0], parts[1:]...)
	execCmd.Dir = workingDir
	configureVerifyCommand(execCmd)
	execCmd.WaitDelay = 5 * time.Second

	var stderr bytes.Buffer
	var stdout bytes.Buffer
	execCmd.Stdout = &stdout
	execCmd.Stderr = &stderr

	debug.Log("verify-lint", "running: %s in %s", cmd, workingDir)
	err := execCmd.Run()

	output := stdout.String() + stderr.String()
	warnings := extractLintWarnings(output)
	if len(warnings) > lintMaxWarnings {
		warnings = warnings[:lintMaxWarnings]
	}

	result := &LintResult{
		Command:  cmd,
		Warnings: warnings,
		Passed:   err == nil && len(warnings) == 0,
	}

	// #3813: a non-zero exit with ZERO parsed warnings means the linter
	// failed in a shape we do not recognize (custom linter output, linter
	// crash, timeout kill). Swallowing it made a real lint failure
	// indistinguishable from a clean run - not even the err (including
	// timeout context) reached the hint. Inject one synthesized warning
	// carrying the exit status and a truncated tail of the raw output so
	// the advisory path degrades loudly instead of silently.
	// #3842: the phrasing is "did not complete" so the hint template can
	// tell an environment/timeout failure from real lint findings and stop
	// advising "fix these lint issues" for a killed go vet.
	if err != nil && len(result.Warnings) == 0 {
		result.Warnings = []string{fmt.Sprintf(
			"lint did not complete: command %q failed with %v (no recognized warnings; raw output tail: %s)",
			cmd, err, truncateRunesForLint(output, 200),
		)}
	} else if err != nil {
		// #3842: partial warnings + non-zero exit - the linter printed some
		// findings and then died (crash, timeout kill). The parsed set may be
		// truncated; mark it so the hint does not present it as complete.
		result.Warnings = append(result.Warnings, fmt.Sprintf(
			"lint did not complete: exit status %v after the warnings above - the warning list may be truncated (raw output tail: %s)",
			err, truncateRunesForLint(output, 200),
		))
	}

	if !result.Passed {
		debug.Log("verify-lint", "found %d warnings", len(warnings))
	} else {
		debug.Log("verify-lint", "clean")
	}

	return result
}

// extractLintWarnings parses linter output to extract meaningful warning lines.
// Different linters use different formats:
//   - go vet:    "file:line: message"
//   - clippy:    "warning: message\n  --> file:line:col"
//   - ruff:      "file:line:col: CODE message"
//   - eslint:    "  line:col  rule  message"
func extractLintWarnings(output string) []string {
	var warnings []string
	scanner := bufio.NewScanner(strings.NewReader(output))
	// scanner uses default buffer; for very large outputs, lines may be truncated
	// at 64KB which is acceptable for lint warning extraction.

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		lower := strings.ToLower(trimmed)

		// Skip lines that are clearly not warnings/errors
		if strings.HasPrefix(lower, "checking") ||
			strings.HasPrefix(lower, "running") ||
			strings.HasPrefix(lower, "ok") ||
			strings.HasPrefix(lower, "pass") ||
			strings.HasPrefix(lower, "compiling") ||
			isCompileErrorMessage(lower) ||
			!looksLikeLintWarning(lower, trimmed) {
			continue
		}

		if len(warnings) < 50 { // pre-cap before final trim
			warnings = append(warnings, trimmed)
		}
	}

	return warnings
}

// isCompileErrorMessage reports whether a `file:line: message` line is a
// COMPILER error rather than a lint finding. When go vet runs without the
// project's build tags (#3752), every tag-gated file fails to compile and
// lines like `foo.go:381: undefined: x` were misclassified as lint
// warnings and injected as "fix these lint issues" - an environment
// problem the agent cannot fix by editing style. Compile errors surface
// properly through the build/verify steps; the lint pass must stay silent
// about them. Markers are kept deliberately narrow: broad fragments like
// "type " or "unexpected " would also match real vet findings.
// truncateRunesForLint bounds a raw-output tail to n runes for the #3813
// synthesized warning (linter failure with unrecognized output shape).
func truncateRunesForLint(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return "..." + string(r[len(r)-n:])
}

func isCompileErrorMessage(lower string) bool {
	for _, marker := range []string{
		"undefined:",            // unresolved symbol (tag-gated code)
		"build failed",          // vet build phase failure banner
		"cannot find package",   // missing import path
		"undeclared name",       // go/types phrasing variant
		"missing return",        // compile-time control-flow error
		"imported and not used", // compile-time unused import
		"declared and not used", // compile-time unused variable
		"expected declaration",  // syntax-class error
		"syntax error",          // parser failure
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// looksLikeLintWarning heuristically determines if a line is a lint warning.
func looksLikeLintWarning(lower, trimmed string) bool {
	// go vet / compiler-style: "path/file.go:42: ..."
	// Any file:line formatted line from lint output is a warning — the noise
	// filter (checking/compiling/ok) already ran in extractLintWarnings.
	if containsFileLine(trimmed) {
		return true
	}

	// clippy / cargo style: "warning: ..."
	if strings.HasPrefix(lower, "warning:") {
		return true
	}

	// ruff style: "file:line:col: CODE ..."
	if hasRuffPattern(trimmed) {
		return true
	}

	// eslint style: "  42:5  error/no-undef  ..."
	if hasESLintPattern(trimmed) {
		return true
	}

	return false
}

// containsFileLine checks if the line contains a file:line pattern.
func containsFileLine(s string) bool {
	// Look for patterns like ".go:42", ".rs:10", ".py:5", ".ts:3"
	for _, ext := range []string{".go:", ".rs:", ".py:", ".ts:", ".tsx:", ".js:", ".jsx:"} {
		if strings.Contains(s, ext) {
			return true
		}
	}
	return false
}

// hasRuffPattern detects ruff-style output: "path/file.py:line:col: CODE message"
func hasRuffPattern(s string) bool {
	parts := strings.Split(s, ":")
	if len(parts) < 4 {
		return false
	}
	// #3862 B: anchor on a path-like first segment. A ruff line is
	// "path/to/file.py:12:5: CODE msg"; timing lines like "duration: 0:00:05"
	// or "total: 12:34:56" share the numeric shape but their first token is a
	// bare word with no '/' or '.' - exclude those.
	if !strings.ContainsAny(parts[0], "/.") {
		return false
	}
	// Check if parts[1] and parts[2] are numeric (line:col)
	for _, p := range []string{parts[1], parts[2]} {
		p = strings.TrimSpace(p)
		if p == "" {
			return false
		}
		for _, c := range p {
			if c < '0' || c > '9' {
				return false
			}
		}
	}
	return true
}

// hasESLintPattern detects eslint-style output: "  line:col  rule  message"
func hasESLintPattern(s string) bool {
	trimmed := strings.TrimLeft(s, " ")
	idx := strings.Index(trimmed, ":")
	if idx <= 0 {
		return false
	}
	// Check part before : is numeric (line number)
	for _, c := range trimmed[:idx] {
		if c < '0' || c > '9' {
			return false
		}
	}
	// Check there's a second colon (col)
	rest := trimmed[idx+1:]
	idx2 := strings.Index(rest, ":")
	if idx2 <= 0 {
		return false
	}
	for _, c := range rest[:idx2] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// lintCommandAvailable checks if the linter binary is available on the system.
// For `make lint`, we assume make is available (it was used to detect the target).
// For language-specific commands, we check if the binary exists in PATH.
func lintCommandAvailable(workingDir, cmd string) bool {
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return false
	}

	// #3862 A: `make` was unconditionally available when a Makefile exists,
	// but detectLintCommand only READS the Makefile text - zero exec. In a
	// minimal container without make, the lint run then failed with
	// "not found" and the #3813 branch synthesized an environment-shaped
	// warning that the #3752 intent explicitly wanted excluded from the
	// lint-warning category. Probe PATH for the real binary.
	if parts[0] == "make" {
		_, err := exec.LookPath("make")
		return err == nil
	}

	// `npx` is assumed available if node_modules/.bin exists or npx is in PATH
	if parts[0] == "npx" {
		_, err := exec.LookPath("npx")
		return err == nil
	}

	// For all others (go, cargo, ruff), check PATH
	_, err := exec.LookPath(parts[0])
	return err == nil
}

// runLintAfterBuild runs the lint check after build passes and injects warnings
// into the agent context if any are found. Returns true if warnings were found.
//
// This is called from both syncVerifyAndGate and asyncVerify after the primary
// build/test verification passes.
func (a *Agent) runLintAfterBuild(ctx context.Context, workingDir string) bool {
	result := a.runLintCheck(ctx, workingDir)
	if result == nil || result.Passed {
		return false
	}

	if len(result.Warnings) == 0 {
		return false
	}

	// Build advisory message for the agent
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Lint check (`%s`) found %d warning(s) after successful build:\n\n", result.Command, len(result.Warnings)))
	incomplete := false
	for _, w := range result.Warnings {
		if strings.HasPrefix(w, "lint did not complete") {
			incomplete = true
		}
		sb.WriteString(fmt.Sprintf("- %s\n", w))
	}
	// #3842: when the run did not complete (timeout kill, missing linter,
	// crash), the closing line must NOT say "fix these lint issues" - the
	// problem is the lint command/environment, and the list may even be
	// truncated. Keep the actionable quality-debt closing only for a
	// completed run.
	if incomplete {
		sb.WriteString("\nThe lint run did not complete (timeout, missing linter, or crash). The warning list above may be truncated - investigate the lint command and environment instead of only fixing the listed issues.")
	} else {
		sb.WriteString("\nFix these lint issues to improve code quality. They are not blocking, but addressing them now avoids tech debt.")
	}

	// Inject into context for the agent to see
	a.contextManager.Add(provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{{
			Type: "text",
			Text: sb.String(),
		}},
	})

	debug.Log("verify-lint", "injected %d lint warnings into context", len(result.Warnings))
	return true
}
