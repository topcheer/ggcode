package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
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

const autoVerifyMaxRetries = 3

const verifyPromptTimeout = 30 * time.Second
const verifyExecuteTimeout = 120 * time.Second

// maybeAutoVerify asks the LLM to determine a verification command
// after completing its work, runs it, and on failure feeds errors back.
// Returns true if verification failed (meaning we should continue the loop).
func (a *Agent) maybeAutoVerify(ctx context.Context, onEvent func(provider.StreamEvent), lastText string) bool {
	mode := a.currentMode()
	if mode == permission.PlanMode {
		return false
	}

	workingDir := a.WorkingDir()
	if workingDir == "" {
		return false
	}

	// Ask the LLM to decide what verification command to run.
	cmd := a.llmDecideVerifyCommand(ctx)
	if cmd == "" {
		debug.Log("verify", "LLM decided no verification needed")
		return false
	}

	result := a.executeVerifyCommand(ctx, cmd)

	if result.Passed {
		onEvent(provider.StreamEvent{
			Type: provider.StreamEventText,
			Text: fmt.Sprintf("\n✅ [Verification passed: `%s`]\n", cmd),
		})
		return false
	}

	// Verification failed — ratchet: record errors for future rule generation
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

	// Inject the errors back into the agent loop
	onEvent(provider.StreamEvent{
		Type: provider.StreamEventText,
		Text: fmt.Sprintf("\n❌ [Verification failed: `%s`]\n%s\n", cmd, truncStr(result.Output, 500)),
	})

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

	return true
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

	verifierMsgs := append([]provider.Message{
		{
			Role: "system",
			Content: []provider.ContentBlock{{
				Type: "text",
				Text: `You are a verification oracle. Based on the recent conversation, determine the single most appropriate build/test/lint command to verify the changes made. Output ONLY the command (no explanation, no markdown fences). If no verification is needed or possible, output exactly "SKIP".`,
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
