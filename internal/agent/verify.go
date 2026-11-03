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

type VerifyResult struct {
	Passed  bool     `json:"passed"`
	Skipped bool     `json:"skipped,omitempty"`
	Errors  []string `json:"errors,omitempty"`
	Command string   `json:"command"`
	Output  string   `json:"output,omitempty"`
}

const verifyPromptTimeout = 30 * time.Second
const verifyExecuteTimeout = 120 * time.Second

const maxSyncVerifyRetries = 3

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

	cmd := ""
	if len(runStats.FilesEdited) > 0 {
		a.verifyProgress("Choosing scoped verification…")
		cmd = a.llmDecideVerifyCommand(ctx, runStats.FilesEdited)
		if cmd == skipVerifyCommand {

			debug.Log("verify", "async: skipping - oracle decided no verification applies")
			return
		}
	}
	if cmd == "" {
		cmd = detectBuildSystem(workingDir)
	}
	if cmd == "" {

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

			debug.Log("verify", "async: skipped result not recorded as last-good")
		} else {
			a.lastGoodCheckpointRecordPass()
		}

		a.verifyProgress("Running lint check…")
		lintStart := time.Now()
		a.runLintAfterBuild(ctx, workingDir)
		debug.Log("verify", "async: lint completed in %s", time.Since(lintStart))
		a.verifyResult(*result)
		return
	}

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

	errorSummary := fmt.Sprintf("Verification failed with the following command:\n```\n%s\n```\n\nErrors:\n", cmd)
	for _, e := range result.Errors {
		errorSummary += fmt.Sprintf("- %s\n", e)
	}
	errorSummary += "\nFix these issues and ensure the build passes."
	if a.verifyRegression != nil {
		transition, regressionSummary := a.verifyRegression.classifyErrorsWithTransition(result.Errors, cmd)
		errorSummary += regressionSummary

		if gateGuidance := a.selfCorrectionGateRecordRound(
			len(transition.newErrors),
			len(transition.persistentErrors),
			transition.resolvedCount,
		); gateGuidance != "" {
			errorSummary += "\n\n" + gateGuidance

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

func (a *Agent) verifyProgress(text string) {
	a.mu.RLock()
	fn := a.onVerifyProgress
	a.mu.RUnlock()
	if fn != nil {
		fn(text)
	}
}

func (a *Agent) verifyResult(result VerifyResult) {
	a.mu.RLock()
	fn := a.onVerifyResult
	a.mu.RUnlock()
	if fn != nil {
		fn(result)
	}
}

func buildVerifyContext(workingDir string) string {
	var hints []string

	makefile := filepath.Join(workingDir, "Makefile")
	if data, err := os.ReadFile(makefile); err == nil {
		content := string(data)
		var targets []string
		for _, t := range []string{"test", "verify", "check", "lint", "ci", "build"} {

			if hasMakeTarget(content, t) {
				targets = append(targets, t)
			}
		}
		if len(targets) > 0 {
			hints = append(hints, fmt.Sprintf("Available make targets: %s (e.g. 'make %s')", strings.Join(targets, ", "), targets[0]))
		}
	}

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

const skipVerifyCommand = "\x00SKIP"

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

	workingDir := a.WorkingDir()
	sysText := buildVerifyOraclePrompt(changedFiles)

	fullSys := a.SystemPrompt()
	if fullSys != "" {

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

		return skipVerifyCommand
	}
	if cmd == "" {
		return ""
	}

	detector := permission.NewDangerousDetector()
	if detector.IsDangerous(cmd) {
		debug.Log("verify", "LLM proposed dangerous command, rejecting: %s", cmd)
		return ""
	}

	return cmd
}

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

		if n >= 2 {
			closerIdx := n - 1 - n%2
			openerIdx := closerIdx - 1
			block = t[fences[openerIdx]+3 : fences[closerIdx]]
		} else {

			block = t[fences[0]+3:]
		}
		if nl := strings.IndexByte(block, '\n'); nl >= 0 {
			block = block[nl+1:]
		} else if sp := strings.IndexAny(block, " \t"); sp >= 0 {

			if isFirstTokenLangTag(block) {
				block = strings.TrimSpace(block[sp+1:])
			}
		} else {
			block = ""
		}
		t = block
	}

	if nl := strings.IndexByte(t, '\n'); nl >= 0 {
		t = t[:nl]
	}
	return strings.TrimSpace(t)
}

func (a *Agent) executeVerifyCommand(ctx context.Context, command string) *VerifyResult {
	workingDir := a.WorkingDir()

	if !verifyCommandAvailable(command) {
		debug.Log("verify", "skipping: binary not available for command: %s", command)
		return &VerifyResult{
			Command: command,
			Passed:  true,
			Skipped: true,
			Output:  "verification skipped: tool not available on this system",
		}
	}

	debug.Log("verify", "running: %s in %s", command, workingDir)

	cmdCtx, cancel := context.WithTimeout(ctx, verifyExecuteTimeout)
	defer cancel()

	cmd, _, err := util.NewShellCommandContext(cmdCtx, command)
	if err != nil {

		cmd = exec.CommandContext(cmdCtx, "sh", "-c", command)
	}
	cmd.Dir = workingDir

	configureVerifyCommand(cmd)
	cmd.WaitDelay = 5 * time.Second
	output, err := cmd.CombinedOutput()
	outputStr := string(output)

	result := &VerifyResult{
		Command: command,
		Output:  truncStr(outputStr, 2000),
	}

	if err != nil {

		if exitErr, ok := err.(*exec.ExitError); ok && isNonFailureExit(command, exitErr.ExitCode()) {
			debug.Log("verify", "skipping: non-failure exit %d: %s", exitErr.ExitCode(), command)
			return &VerifyResult{
				Command: command,
				Passed:  true,
				Skipped: true,

				Output: fmt.Sprintf("verification skipped: non-failure exit %d (tool unavailable or no tests collected)", exitErr.ExitCode()),
			}
		}
		result.Passed = false
		result.Errors = extractErrorLines(outputStr)
		if len(result.Errors) == 0 {

			fallback := fmt.Sprintf("command failed: %v", err)
			if tail := truncTail(strings.TrimSpace(outputStr), 2000); tail != "" {
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

func isNonFailureExit(command string, code int) bool {

	isCompound := strings.Contains(command, "||") || strings.Contains(command, "&&") || strings.Contains(command, ";")
	if code == 127 {

		if isCompound {
			return false
		}
		return true
	}

	if isCompound {
		return false
	}
	return code == 5 && strings.Contains(command, "pytest")
}

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

func isNegationProseAt(lower, phrase string) bool {
	idx := 0
	for {
		i := strings.Index(lower[idx:], phrase)
		if i < 0 {
			return false
		}
		at := idx + i
		end := at + len(phrase)

		if end < len(lower) && lower[end] == 's' {
			end++
		}

		leftOK := at == 0 || !sdIsLowerAZ(lower[at-1])

		rightOK := end >= len(lower) || !sdIsLowerAZ(lower[end])
		if leftOK && rightOK {
			return true
		}
		idx = at + 1
	}
}

func carriesFailureContext(lower string) bool {
	for _, m := range []string{"assert", "expected", "got ", "runtimeerror", "exception"} {
		if strings.Contains(lower, m) {
			return true
		}
	}
	return false
}

func looksLikeDiffBody(trimmed string) bool {

	if strings.Count(trimmed, "(") > 0 && strings.Count(trimmed, ")") > 0 {
		return true
	}
	return strings.Contains(trimmed, "\"") || strings.Contains(trimmed, ".")
}

func sdIsLowerAZ(b byte) bool { return b >= 'a' && b <= 'z' }

func extractErrorLines(output string) []string {
	var errors []string
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)

		if zeroFailSummary(lower) {

			continue
		}
		if (isNegationProseAt(lower, "no error") || isNegationProseAt(lower, "without error")) &&
			!carriesFailureContext(lower) {
			continue
		}
		if strings.HasPrefix(trimmed, "-") && !strings.HasPrefix(trimmed, "--- ") &&
			!looksLikeDiffBody(trimmed) &&
			(strings.Contains(lower, "fail") || strings.Contains(lower, "error")) {

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

func extractText(msg provider.Message) string {
	for _, b := range msg.Content {
		if b.Type == "text" {
			return b.Text
		}
	}
	return ""
}

func (a *Agent) SetAutoVerify(enabled bool) {
	a.mu.Lock()
	a.autoVerify = enabled
	a.mu.Unlock()
}

func (a *Agent) SetClaimsSupervision(enabled bool) {
	a.mu.Lock()
	a.claimsSupervision = enabled
	a.mu.Unlock()
}

func (a *Agent) ClaimsSupervisionEnabled() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.claimsSupervision
}

func (a *Agent) AutoVerifyEnabled() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.autoVerify
}

const maxRuleInjectPerSession = 2

const maxRulesPerInjection = 3

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

	cmd := ""
	if len(runStats.FilesEdited) > 0 {
		a.verifyProgress("Choosing scoped verification…")
		start := time.Now()
		cmd = a.llmDecideVerifyCommand(ctx, runStats.FilesEdited)
		debug.Log("verify", "sync: oracle chose verify command in %s: %q", time.Since(start), cmd)
		if cmd == skipVerifyCommand {

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

			debug.Log("verify", "sync: skipped result not recorded as last-good")
		} else {
			a.lastGoodCheckpointRecordPass()
		}

		a.verifyProgress("Running lint check…")
		lintStart := time.Now()
		a.runLintAfterBuild(ctx, workingDir)
		debug.Log("verify", "sync: lint completed in %s", time.Since(lintStart))
		a.verifyResult(*result)
		return false, true
	}

	debug.Log("verify", "sync: FAILED: %d errors", len(result.Errors))
	a.lastGoodCheckpointRecordFail()

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

	if retryCount >= maxSyncVerifyRetries {
		debug.Log("verify", "sync: budget exhausted (%d/%d), proceeding to return", retryCount, maxSyncVerifyRetries)
		a.verifyProgress(fmt.Sprintf("Verification still failing after %d attempts — please review manually.", maxSyncVerifyRetries+1))
		return false, false
	}

	a.verifyProgress(fmt.Sprintf("Verification failed — auto-repairing (attempt %d/%d)…", retryCount+1, maxSyncVerifyRetries+1))

	errorSummary := fmt.Sprintf("Verification failed with the following command:\n```\n%s\n```\n\nErrors:\n", cmd)
	for _, e := range result.Errors {
		errorSummary += fmt.Sprintf("- %s\n", e)
	}
	errorSummary += "\nFix these issues and ensure the build passes."
	if a.verifyRegression != nil {
		transition, regressionSummary := a.verifyRegression.classifyErrorsWithTransition(result.Errors, cmd)
		errorSummary += regressionSummary

		if gateGuidance := a.selfCorrectionGateRecordRound(
			len(transition.newErrors),
			len(transition.persistentErrors),
			transition.resolvedCount,
		); gateGuidance != "" {
			errorSummary += "\n\n" + gateGuidance

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

	return true, false
}
