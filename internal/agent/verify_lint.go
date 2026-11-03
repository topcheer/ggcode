package agent

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

const lintTimeout = 60 * time.Second

const lintMaxWarnings = 15

type LintResult struct {
	Command  string   `json:"command"`
	Warnings []string `json:"warnings,omitempty"`
	Passed   bool     `json:"passed"`
}

func detectLintCommand(workingDir string) string {
	if workingDir == "" {
		return ""
	}

	for _, mf := range []string{"Makefile", "makefile", "GNUmakefile"} {
		path := filepath.Join(workingDir, mf)
		if data, err := os.ReadFile(path); err == nil {
			if hasMakeTarget(string(data), "lint") {
				return "make lint"
			}
		}
	}

	if fileExists(filepath.Join(workingDir, "go.mod")) {

		if tags := detectGoBuildTags(workingDir); len(tags) > 0 {
			return "go vet -tags " + strings.Join(tags, ",") + " ./..."
		}
		return "go vet ./..."
	}
	if fileExists(filepath.Join(workingDir, "Cargo.toml")) {
		return "cargo clippy"
	}

	if fileExists(filepath.Join(workingDir, "ruff.toml")) ||
		fileExists(filepath.Join(workingDir, ".ruff.toml")) {
		return "ruff check ."
	}
	if fileExists(filepath.Join(workingDir, "pyproject.toml")) ||
		fileExists(filepath.Join(workingDir, "setup.py")) {

		return "ruff check ."
	}

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

func (a *Agent) runLintCheck(ctx context.Context, workingDir string) *LintResult {
	cmd := detectLintCommand(workingDir)
	if cmd == "" {
		return nil
	}

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

	if err != nil && len(result.Warnings) == 0 {
		result.Warnings = []string{fmt.Sprintf(
			"lint did not complete: command %q failed with %v (no recognized warnings; raw output tail: %s)",
			cmd, err, truncateRunesForLint(output, 200),
		)}
	} else if err != nil {

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

func extractLintWarnings(output string) []string {
	var warnings []string
	scanner := bufio.NewScanner(strings.NewReader(output))

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		lower := strings.ToLower(trimmed)

		hasFileLine := containsFileLine(trimmed) || hasRuffPattern(trimmed)
		if !hasFileLine &&
			(strings.HasPrefix(lower, "checking") ||
				strings.HasPrefix(lower, "running") ||
				strings.HasPrefix(lower, "ok") ||
				strings.HasPrefix(lower, "pass") ||
				strings.HasPrefix(lower, "compiling")) ||
			isCompileErrorMessage(lower) ||
			!looksLikeLintWarning(lower, trimmed) {
			continue
		}

		if len(warnings) < 50 {
			warnings = append(warnings, trimmed)
		}
	}

	return warnings
}

func truncateRunesForLint(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return "..." + string(r[len(r)-n:])
}

func isCompileErrorMessage(lower string) bool {
	for _, marker := range []string{
		"undefined:",
		"build failed",
		"cannot find package",
		"undeclared name",
		"missing return",
		"imported and not used",
		"declared and not used",
		"expected declaration",
		"syntax error",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func looksLikeLintWarning(lower, trimmed string) bool {

	if containsFileLine(trimmed) {
		return true
	}

	if strings.HasPrefix(lower, "warning:") {
		return true
	}

	if hasRuffPattern(trimmed) {
		return true
	}

	if hasESLintPattern(trimmed) {
		return true
	}

	return false
}

func containsFileLine(s string) bool {

	for _, ext := range []string{".go:", ".rs:", ".py:", ".ts:", ".tsx:", ".js:", ".jsx:"} {
		if strings.Contains(s, ext) {
			return true
		}
	}
	return false
}

func hasRuffPattern(s string) bool {
	parts := strings.Split(s, ":")
	if len(parts) < 4 {
		return false
	}

	if !strings.ContainsAny(parts[0], "/.") {
		return false
	}

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

func hasESLintPattern(s string) bool {
	trimmed := strings.TrimLeft(s, " ")
	idx := strings.Index(trimmed, ":")
	if idx <= 0 {
		return false
	}

	for _, c := range trimmed[:idx] {
		if c < '0' || c > '9' {
			return false
		}
	}

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

func lintCommandAvailable(workingDir, cmd string) bool {
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return false
	}

	if parts[0] == "make" {
		_, err := exec.LookPath("make")
		return err == nil
	}

	if parts[0] == "npx" {
		_, err := exec.LookPath("npx")
		return err == nil
	}

	_, err := exec.LookPath(parts[0])
	return err == nil
}

func (a *Agent) runLintAfterBuild(ctx context.Context, workingDir string) bool {
	result := a.runLintCheck(ctx, workingDir)
	if result == nil || result.Passed {
		return false
	}

	if len(result.Warnings) == 0 {
		return false
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Lint check (`%s`) found %d warning(s) after successful build:\n\n", result.Command, len(result.Warnings)))
	incomplete := false
	for _, w := range result.Warnings {
		if strings.HasPrefix(w, "lint did not complete") {
			incomplete = true
		}
		sb.WriteString(fmt.Sprintf("- %s\n", w))
	}

	if incomplete {
		sb.WriteString("\nThe lint run did not complete (timeout, missing linter, or crash). The warning list above may be truncated - investigate the lint command and environment instead of only fixing the listed issues.")
	} else {
		sb.WriteString("\nFix these lint issues to improve code quality. They are not blocking, but addressing them now avoids tech debt.")
	}

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
