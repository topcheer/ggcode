package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
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

// --- Auto-verify loop integration ---

const verifyPromptTimeout = 30 * time.Second
const verifyExecuteTimeout = 120 * time.Second

// asyncVerify runs build/test verification in the background after the agent
// loop completes. It does NOT block the agent's response — the user sees the
// agent's output immediately, and verification results arrive asynchronously
// via the onVerifyProgress/onVerifyResult callbacks.
//
// If verification fails, errors are injected into the context manager so they're
// available for the next user turn. No automatic retry — the user decides what
// to do with the failure.
func (a *Agent) asyncVerify(ctx context.Context, runStats *RunStats) {
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

	// First try deterministic detection — no LLM call needed.
	cmd := detectBuildSystem(workingDir)
	if cmd == "" {
		a.verifyProgress("Determining verification command…")
		cmd = a.llmDecideVerifyCommand(ctx)
	} else {
		debug.Log("verify", "using deterministic command: %s", cmd)
	}
	if cmd == "" {
		debug.Log("verify", "LLM decided no verification needed")
		return
	}

	a.verifyProgress(fmt.Sprintf("Running `%s`…", cmd))
	result := a.executeVerifyCommand(ctx, cmd)

	if result.Passed {
		debug.Log("verify", "PASSED: %s", cmd)
		a.verifyResult(*result)
		return
	}

	// Verification failed — ratchet: record errors for future rule generation.
	rs := NewRuleStore(workingDir)
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

	// Inject errors into context for the next turn. No auto-retry.
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
			if strings.Contains(content, t+":") {
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

// llmDecideVerifyCommand asks the LLM to output a single verification command
// based on what it just did. Returns empty string if no verification is needed.
func (a *Agent) llmDecideVerifyCommand(ctx context.Context) string {
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
	sysText := `You are a verification oracle. Based on the recent conversation, determine the single most appropriate build/test/lint command to verify the changes made. Output ONLY the command (no explanation, no markdown fences). If no verification is needed or possible, output exactly "SKIP".`

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

	cmd := strings.TrimSpace(extractText(resp.Message))
	if strings.EqualFold(cmd, "SKIP") || cmd == "" {
		return ""
	}

	// Reject obviously dangerous commands
	lower := strings.ToLower(cmd)
	if strings.Contains(lower, "rm -rf") || strings.Contains(lower, "sudo") ||
		strings.Contains(lower, "> /dev/") || strings.Contains(lower, "dd if=") {
		debug.Log("verify", "LLM proposed dangerous command, rejecting: %s", cmd)
		return ""
	}

	return cmd
}

// executeVerifyCommand runs the command and returns the result.
func (a *Agent) executeVerifyCommand(ctx context.Context, command string) *VerifyResult {
	workingDir := a.WorkingDir()
	debug.Log("verify", "running: %s in %s", command, workingDir)

	cmdCtx, cancel := context.WithTimeout(ctx, verifyExecuteTimeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "sh", "-c", command)
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
		result.Passed = false
		result.Errors = extractErrorLines(outputStr)
		if len(result.Errors) == 0 {
			result.Errors = []string{fmt.Sprintf("command failed: %v", err)}
		}
		debug.Log("verify", "FAILED: %d errors", len(result.Errors))
	} else {
		result.Passed = true
		debug.Log("verify", "PASSED")
	}

	return result
}

// extractErrorLines pulls likely error lines from build/test output.
func extractErrorLines(output string) []string {
	var errors []string
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)
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

// --- Rule injection into tool results ---

// injectRulesIntoResult prepends matching harness rules to a tool result.
// Rules are matched via two paths:
//  1. Preventive: MatchingRulesForTool matches rule patterns against tool ARGS
//     (warns before the error occurs, e.g. "go build" without -tags goolm).
//  2. Reactive: MatchingRulesForResult matches rule patterns against the tool
//     RESULT content (warns when a known error pattern appears in the output,
//     e.g. "cannot find package olm" → injects the fix hint immediately).
func (a *Agent) injectRulesIntoResult(toolName string, args json.RawMessage, resultContent string) string {
	workingDir := a.WorkingDir()
	if workingDir == "" {
		return resultContent
	}
	rs := NewRuleStore(workingDir)
	if rs == nil {
		return resultContent
	}

	preventive := rs.MatchingRulesForTool(toolName, string(args))
	reactive := rs.MatchingRulesForResult(resultContent)
	matching := mergeRuleSets(preventive, reactive)
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
