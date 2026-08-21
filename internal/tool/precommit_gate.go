package tool

// Pre-commit Build Gate
//
// Research basis: AI coding agents (Claude Code, Cursor, Aider) all verify that
// code compiles before creating a git commit. Without this gate, an agent can
// commit broken code that doesn't compile — especially common when:
//   - The agent makes partial edits across multiple files but misses one
//   - Autopilot mode commits after reaching an iteration limit without verifying
//   - Context compaction drops a key constraint the agent was tracking
//   - The agent stops early due to rate limits or timeouts
//
// Existing ggcode systems do NOT cover this:
//   - verify.go: runs AFTER the agent loop completes, not at commit time
//   - verify_lint.go: runs after build passes, not as a gate on commit
//   - diff_scan.go: scans for debug statements and secrets, not compile errors
//   - post_edit_diagnostics.go: per-file LSP check, not a project-wide build
//
// This module runs a fast project-wide build check before git_commit executes.
// It reuses the same build-system detection pattern as verify_hint.go but stays
// in the tool package to avoid cross-package dependencies. The check is
// advisory (non-blocking) — the commit still proceeds, but a clear warning is
// appended so the agent or user can follow up with a fix-up commit.

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

// precommitBuildTimeout is the maximum time for the build check. This must be
// fast enough to not stall the commit — 30s covers most incremental builds.
const precommitBuildTimeout = 30 * time.Second

// precommitMaxErrors caps the number of build errors shown in the warning.
const precommitMaxErrors = 10

// precommitGateEnabled controls whether the pre-commit build gate runs.
// Defaults to true. Can be disabled via configuration.
var precommitGateEnabled = true

// SetPrecommitGateEnabled enables or disables the pre-commit build gate globally.
func SetPrecommitGateEnabled(enabled bool) {
	precommitGateEnabled = enabled
}

// precommitBuildCheck runs a fast project-wide build verification before
// committing. Returns an advisory warning string if the build fails, or empty
// string if the build passes, is disabled, or cannot be determined.
//
// The check is always advisory — it never blocks the commit. This matches the
// behavior of all other git_commit advisory checks (diff scan, message quality,
// scope analysis).
func precommitBuildCheck(ctx context.Context, dir string) string {
	if !precommitGateEnabled {
		return ""
	}
	if dir == "" {
		return ""
	}

	cmd := detectPrecommitBuildCommand(dir)
	if cmd == "" {
		// No build system detected — nothing to verify.
		return ""
	}

	debug.Log("precommit-gate", "running build check: %s in %s", cmd, dir)

	checkCtx, cancel := context.WithTimeout(ctx, precommitBuildTimeout)
	defer cancel()

	// Use sh -c for complex commands with arguments.
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return ""
	}

	var c *exec.Cmd
	if len(parts) > 1 {
		c = exec.CommandContext(checkCtx, parts[0], parts[1:]...)
	} else {
		c = exec.CommandContext(checkCtx, parts[0])
	}
	c.Dir = dir

	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr

	err := c.Run()
	if err == nil {
		// Build passed — no warning needed.
		debug.Log("precommit-gate", "build check passed: %s", cmd)
		return ""
	}

	// #859: any aborted context (timeout AND parent cancellation, e.g. user
	// interrupt) leaves the build result indeterminate — a non-nil err here
	// does not prove the code fails to compile. Don't warn on either.
	if checkCtx.Err() != nil {
		debug.Log("precommit-gate", "build check aborted (ctx: %v): %s", checkCtx.Err(), cmd)
		return ""
	}

	// Build failed. Extract and format error output.
	output := stderr.String()
	if output == "" {
		output = stdout.String()
	}

	errors := extractBuildErrors(output)
	if len(errors) == 0 {
		// No parseable errors — just a generic failure.
		return fmt.Sprintf(
			"Warning: pre-commit build check failed (command: %q). "+
				"The committed code may not compile. Consider verifying before pushing.",
			cmd,
		)
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf(
		"Warning: pre-commit build check FAILED (command: %q).\n"+
			"The committed code does NOT compile. Consider fixing these errors and "+
			"amending the commit before pushing.\n\nBuild errors:\n",
		cmd,
	))
	shown := errors
	if len(shown) > precommitMaxErrors {
		shown = shown[:precommitMaxErrors]
	}
	for _, e := range shown {
		b.WriteString("  " + e + "\n")
	}
	if len(errors) > precommitMaxErrors {
		b.WriteString(fmt.Sprintf("  ... and %d more\n", len(errors)-precommitMaxErrors))
	}

	debug.Log("precommit-gate", "build check FAILED: %d errors", len(errors))
	return b.String()
}

// detectPrecommitBuildCommand returns the appropriate build command for the
// project type. This is a lightweight version of agent.detectBuildSystem that
// focuses on fast compilation checks only (no tests, no linting).
//
// Priority: Makefile build target > language-specific fast build.
func detectPrecommitBuildCommand(dir string) string {
	if dir == "" {
		return ""
	}

	// 1. Makefile with a build target — uses the project's exact build config.
	for _, mf := range []string{"Makefile", "makefile", "GNUmakefile"} {
		path := filepath.Join(dir, mf)
		if data, err := os.ReadFile(path); err == nil {
			content := string(data)
			// Prefer a "build" target (fast, no tests). Fall back to
			// "verify-ci" which is often a quick compile check.
			for _, target := range []string{"build", "verify-ci"} {
				if hasPrecommitMakeTarget(content, target) {
					return "make " + target
				}
			}
			break
		}
	}

	// 2. Language-specific fast build checks.
	if fileExistsPrecommit(filepath.Join(dir, "go.mod")) {
		return "go build ./..."
	}
	if fileExistsPrecommit(filepath.Join(dir, "Cargo.toml")) {
		// "check" is faster than "build" — it doesn't generate artifacts.
		return "cargo check"
	}
	if fileExistsPrecommit(filepath.Join(dir, "package.json")) {
		// Try TypeScript type-check first (catches most JS build errors).
		// Fall back to npm run build if a build script exists.
		if hasNPMBuildScript(filepath.Join(dir, "package.json")) {
			return "npm run build"
		}
		// No build script — skip. Don't run tsc blindly (may not be installed).
		return ""
	}
	if fileExistsPrecommit(filepath.Join(dir, "CMakeLists.txt")) {
		return "cmake --build build"
	}

	return ""
}

// hasPrecommitMakeTarget checks if a Makefile defines a target.
func hasPrecommitMakeTarget(content, target string) bool {
	prefix := target + ":"
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}
	return false
}

// fileExistsPrecommit is a local version to avoid cross-package imports.
func fileExistsPrecommit(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// hasNPMBuildScript checks if package.json has a "build" script.
func hasNPMBuildScript(pkgJSONPath string) bool {
	data, err := os.ReadFile(pkgJSONPath)
	if err != nil {
		return false
	}
	// Simple check — look for "build" in "scripts" section.
	// Full JSON parse would be more robust but adds complexity for a heuristic.
	return bytes.Contains(data, []byte(`"build"`))
}

// extractBuildErrors parses compiler output to extract concise error lines.
// Handles Go, Rust, C/C++, and TypeScript compiler formats.
func extractBuildErrors(output string) []string {
	if output == "" {
		return nil
	}

	var errors []string
	scanner := bufio.NewScanner(strings.NewReader(output))
	scanner.Buffer(make([]byte, 0, 64*1024), 256*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		// Skip non-error lines (informational output, warnings, progress).
		if isBuildErrorLine(line) {
			// Truncate very long lines for readability.
			if len(line) > 200 {
				line = line[:200] + "..."
			}
			errors = append(errors, line)
		}
	}

	return errors
}

// isBuildErrorLine checks if a line is a compiler error (not a warning or info).
func isBuildErrorLine(line string) bool {
	lower := strings.ToLower(line)

	// Go: "file.go:10:5: undefined: foo" or "# package/path" prefix (errors only)
	if strings.Contains(line, ".go:") && strings.Contains(lower, "error") {
		return true
	}
	// Go: undefined references, type mismatches, etc. (no explicit "error" word)
	if strings.Contains(line, ".go:") && (strings.Contains(lower, "undefined") ||
		strings.Contains(lower, "cannot use") ||
		strings.Contains(lower, "mismatched") ||
		strings.Contains(lower, "not enough") ||
		strings.Contains(lower, "too many")) {
		return true
	}

	// Rust: "error[E0xxx]: message"
	if strings.HasPrefix(lower, "error[") || strings.HasPrefix(lower, "error:") {
		return true
	}

	// C/C++: "file.c:10:5: error: ..."
	if strings.Contains(lower, ": error:") {
		return true
	}

	// TypeScript: "file.ts(10,5): error TS2xxx: ..."
	if strings.Contains(lower, ": error ts") {
		return true
	}

	// Generic: lines starting with "Error" or "ERROR"
	if strings.HasPrefix(lower, "error ") || strings.HasPrefix(line, "Error ") {
		return true
	}

	// Python: "SyntaxError", "ImportError", "NameError", etc.
	// #859: bare Contains("0 errors") also swallowed "Found 10 errors in N
	// files." — require a digit boundary before "0 errors".
	if strings.Contains(lower, "error:") && !regexp.MustCompile(`\b0 errors`).MatchString(lower) {
		return true
	}

	return false
}
