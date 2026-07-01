package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
)

// VerifyResult is the outcome of an auto-verification check.
type VerifyResult struct {
	Passed  bool     `json:"passed"`
	Errors  []string `json:"errors,omitempty"`
	Command string   `json:"command"`
	Output  string   `json:"output,omitempty"`
}

// inferBuildCommand guesses the build/test command from the project structure.
func inferBuildCommand(workingDir string) string {
	// Go project
	if fileExists(filepath.Join(workingDir, "go.mod")) {
		// Check for Makefile first
		if fileExists(filepath.Join(workingDir, "Makefile")) {
			return "make build"
		}
		return "go build ./..."
	}
	// Flutter
	if fileExists(filepath.Join(workingDir, "pubspec.yaml")) {
		return "flutter analyze"
	}
	// Node
	if fileExists(filepath.Join(workingDir, "package.json")) {
		return "npm run build"
	}
	// Rust
	if fileExists(filepath.Join(workingDir, "Cargo.toml")) {
		return "cargo build"
	}
	return ""
}

func fileExists(path string) bool {
	_, err := exec.Command("test", "-f", path).Output()
	return err == nil
}

// RunVerification executes the build/test command and returns the result.
// This is the "checker" — it runs in the main process (not a sub-agent) for
// speed and simplicity. The verification is deterministic: run the build,
// check if it passes.
func (a *Agent) RunVerification(ctx context.Context, command string) (*VerifyResult, error) {
	workingDir := a.WorkingDir()
	if workingDir == "" {
		return nil, fmt.Errorf("no working directory set")
	}

	if command == "" {
		command = inferBuildCommand(workingDir)
	}
	if command == "" {
		return &VerifyResult{Passed: true, Command: ""}, nil
	}

	debug.Log("verify", "running: %s in %s", command, workingDir)

	// Run with a timeout
	cmdCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	// Parse command (handle shell features like &&, |, etc.)
	cmd := exec.CommandContext(cmdCtx, "sh", "-c", command)
	cmd.Dir = workingDir

	output, err := cmd.CombinedOutput()
	outputStr := string(output)

	result := &VerifyResult{
		Command: command,
		Output:  truncStr(outputStr, 2000),
	}

	if err != nil {
		result.Passed = false
		// Extract error lines
		result.Errors = extractErrorLines(outputStr)
		if len(result.Errors) == 0 {
			result.Errors = []string{fmt.Sprintf("command failed: %v", err)}
		}
		debug.Log("verify", "FAILED: %d errors", len(result.Errors))
	} else {
		result.Passed = true
		debug.Log("verify", "PASSED")
	}

	return result, nil
}

// extractErrorLines pulls likely error lines from build/test output.
func extractErrorLines(output string) []string {
	var errors []string
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)
		// Match common error patterns
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

// --- Auto-verify loop integration ---

// autoVerifyThreshold limits how many verify-fail cycles we allow before
// giving up and letting the user intervene.
const autoVerifyMaxRetries = 3

// maybeAutoVerify checks if the run appears complete and triggers verification.
// Only runs in non-plan modes. Called after the main loop exits (no more tool calls).
// Returns true if verification ran and failed (meaning we should continue the loop).
func (a *Agent) maybeAutoVerify(ctx context.Context, onEvent func(provider.StreamEvent), lastText string) bool {
	mode := a.currentMode()
	if mode == permission.PlanMode {
		return false
	}

	workingDir := a.WorkingDir()
	if workingDir == "" {
		return false
	}

	// Check if there's something to verify (files were modified)
	// We can't easily check this without tracking, so just run verification
	// if a build command can be inferred.
	cmd := inferBuildCommand(workingDir)
	if cmd == "" {
		return false // can't verify without a build command
	}

	result, err := a.RunVerification(ctx, cmd)
	if err != nil {
		debug.Log("verify", "verification error: %v", err)
		return false
	}

	if result.Passed {
		onEvent(provider.StreamEvent{
			Type: provider.StreamEventText,
			Text: fmt.Sprintf("\n[Verification passed: `%s`]\n", cmd),
		})
		return false
	}

	// Verification failed — inject the errors back into the agent
	onEvent(provider.StreamEvent{
		Type: provider.StreamEventText,
		Text: fmt.Sprintf("\n[Verification failed: `%s`]\n%s\n", cmd, result.Output[:min(500, len(result.Output))]),
	})

	// Add the verification errors to the context manager as a user message
	errorSummary := fmt.Sprintf("Verification failed with the following command:\n```\n%s\n```\n\nErrors:\n", cmd)
	for _, e := range result.Errors {
		errorSummary += fmt.Sprintf("- %s\n", e)
	}
	errorSummary += "\nFix these issues and ensure the build passes."

	a.contextManager.Add(provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{{
			Type: "text",
			Text: errorSummary,
		}},
	})

	return true // signal: continue the loop
}

// --- Rule injection into tool results ---

// injectRulesIntoResult prepends matching harness rules to a tool result.
// This is the "ratchet" in action: the agent sees relevant rules when it's
// about to make the same mistake.
func (a *Agent) injectRulesIntoResult(toolName string, args json.RawMessage, resultContent string) string {
	workingDir := a.WorkingDir()
	if workingDir == "" {
		return resultContent
	}
	rs := NewRuleStore(workingDir)
	if rs == nil {
		return resultContent
	}

	matching := rs.MatchingRulesForTool(toolName, string(args))
	if len(matching) == 0 {
		return resultContent
	}

	var b strings.Builder
	b.WriteString("[Harness Rules — learned from past mistakes]\n")
	for _, r := range matching {
		b.WriteString(fmt.Sprintf("⚠ %s\n", r.Rule))
		if r.FixHint != "" {
			b.WriteString(fmt.Sprintf("  → %s\n", r.FixHint))
		}
	}
	b.WriteString("\n")
	b.WriteString(resultContent)
	return b.String()
}
