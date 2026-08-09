package agent

// risk_aware_controller.go implements Risk-Aware, Budgeted Controller pattern
// from AI Agent Systems survey (arXiv:2601.01743, 2025).
//
// Core concept: Branch decision-making based on action reversibility and impact:
//   - LOW risk (read-only queries): execute with minimal deliberation
//   - MEDIUM risk (reversible writes): standard verification
//   - HIGH risk (irreversible operations): extra verification, multi-step evidence gathering, or human confirmation
//
// Research basis:
//   - "Risk-aware, budgeted controller" (Sang et al., 2025)
//   - Actions differ in reversibility and potential impact
//   - Verifiers define operational semantics, not optional add-ons
//   - Trace-first operation binds decisions to evidence

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

// RiskLevel represents the risk category of an action
type RiskLevel int

const (
	// RiskLow: read-only queries, harmless information gathering
	// Execute with minimal deliberation, no verification overhead
	RiskLow RiskLevel = iota
	// RiskMedium: reversible writes (staging, creating new files)
	// Standard verification (schema validation, basic checks)
	RiskMedium
	// RiskHigh: irreversible operations (git push, delete commits, production deploys)
	// Extra verification, multi-step evidence gathering, or human confirmation
	RiskHigh
)

func (r RiskLevel) String() string {
	switch r {
	case RiskLow:
		return "LOW"
	case RiskMedium:
		return "MEDIUM"
	case RiskHigh:
		return "HIGH"
	default:
		return "UNKNOWN"
	}
}

// RiskAssessment represents a risk evaluation for a tool call
type RiskAssessment struct {
	Level        RiskLevel
	Reason       string
	Action       string  // suggested mitigation action
	Confidence   float64 // 0.0-1.0, higher = more certain about risk level
	RequiresHalt bool    // if true, require explicit confirmation before proceeding
}

// riskAwareController manages risk-based tool execution policies
type riskAwareController struct {
	mu sync.Mutex

	// Track verification budget allocation for current session
	verificationBudget int // remaining verification tokens/steps
	initialBudget      int // initial budget for this session
	highRiskCount      int // number of high-risk actions executed
	mediumRiskCount    int // number of medium-risk actions executed

	// Cache recent tool patterns for risk learning
	recentToolPatterns map[string]int // tool name -> call count

	// Configuration
	maxHighRiskPerSession   int // cap on high-risk actions (0 = unlimited)
	verificationPerHighRisk int // verification steps allocated per high-risk action
}

func newRiskAwareController(initialBudget int) *riskAwareController {
	return &riskAwareController{
		verificationBudget:      initialBudget,
		initialBudget:           initialBudget,
		recentToolPatterns:      make(map[string]int),
		maxHighRiskPerSession:   0, // unlimited by default
		verificationPerHighRisk: 3, // allocate 3 verification steps for each high-risk action
	}
}

func (r *riskAwareController) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.verificationBudget = r.initialBudget
	r.highRiskCount = 0
	r.mediumRiskCount = 0
	r.recentToolPatterns = make(map[string]int)
}

// AssessRisk evaluates the risk level of a tool call before execution
func (r *riskAwareController) AssessRisk(toolName string, args json.RawMessage) RiskAssessment {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Track tool pattern for learning
	r.recentToolPatterns[toolName]++

	// Default low risk for unknown tools
	assessment := RiskAssessment{
		Level:      RiskLow,
		Reason:     "Unknown tool pattern, assuming low risk",
		Confidence: 0.3,
	}

	// Read-only tools are LOW risk
	if isReadOnlyTool(toolName) {
		assessment.Level = RiskLow
		assessment.Reason = "Read-only operation: no side effects, minimal verification needed"
		assessment.Confidence = 0.95
		return assessment
	}

	// Git tools: assess based on specific operation
	if strings.HasPrefix(toolName, "git_") {
		assessment = r.assessGitRisk(toolName, args)
		return assessment
	}

	// File operations: assess based on action type
	if toolName == "file_ops" {
		assessment = r.assessFileOpsRisk(args)
		return assessment
	}

	// Edit/write tools: medium risk (reversible via undo)
	if isWriteTool(toolName) {
		assessment.Level = RiskMedium
		assessment.Reason = "Write operation: reversible via undo_edit or git, requires standard verification"
		assessment.Confidence = 0.85
		return assessment
	}

	// Shell commands: high risk if potentially destructive
	if toolName == "run_command" || toolName == "start_command" {
		assessment = r.assessCommandRisk(args)
		return assessment
	}

	return assessment
}

// isReadOnlyTool returns true if the tool has no side effects
func isReadOnlyTool(toolName string) bool {
	readOnlyTools := map[string]bool{
		"read_file":             true,
		"multi_file_read":       true,
		"list_directory":        true,
		"glob":                  true,
		"search_files":          true,
		"grep":                  true,
		"git_status":            true,
		"git_diff":              true,
		"git_log":               true,
		"git_show":              true,
		"git_branch_list":       true,
		"git_remote":            true,
		"git_blame":             true,
		"lsp_hover":             true,
		"lsp_definition":        true,
		"lsp_references":        true,
		"lsp_symbols":           true,
		"lsp_diagnostics":       true,
		"web_search":            true,
		"web_fetch":             true,
		"runtime":               true,
		"task_list":             true,
		"task_get":              true,
		"code_search":           true,
		"code_execution":        true,
		"list_mcp_capabilities": true,
		"read_mcp_resource":     true,
		"lanchat":               true, // messaging is low-risk
	}
	return readOnlyTools[toolName]
}

// isWriteTool returns true if the tool modifies files (reversible)
func isWriteTool(toolName string) bool {
	writeTools := map[string]bool{
		"edit_file":        true,
		"multi_edit_file":  true,
		"write_file":       true,
		"multi_file_write": true,
		"notebook_edit":    true,
	}
	return writeTools[toolName]
}

// assessGitRisk evaluates risk level for git operations
func (r *riskAwareController) assessGitRisk(toolName string, args json.RawMessage) RiskAssessment {
	var m map[string]any
	if len(args) > 0 {
		json.Unmarshal(args, &m)
	}

	assessment := RiskAssessment{Confidence: 0.9}

	switch toolName {
	case "git_add":
		// Staging is low risk (can be reset)
		assessment.Level = RiskLow
		assessment.Reason = "git_add: Staging changes, reversible via git reset"

	case "git_commit":
		// Committing without push is medium risk (reversible via reset)
		assessment.Level = RiskMedium
		assessment.Reason = "git_commit: Creates commit object, reversible before push"
		assessment.Action = "Consider running tests/build before committing"

	case "git_push":
		// Pushing is HIGH risk (affects remote repository)
		assessment.Level = RiskHigh
		assessment.Reason = "git_push: Publishes commits to remote, affects collaborators and CI/CD"
		assessment.Action = "Verify: local tests pass, changes reviewed, CI status green"
		assessment.RequiresHalt = true

	case "git_reset":
		// Hard reset is HIGH risk (discards work)
		if mode, _ := m["mode"].(string); mode == "hard" {
			assessment.Level = RiskHigh
			assessment.Reason = "git_reset --hard: Permanently discards uncommitted changes"
			assessment.Action = "Consider git stash instead, or use --soft/--mixed mode"
			assessment.RequiresHalt = true
		} else {
			assessment.Level = RiskLow
			assessment.Reason = "git_reset (soft/mixed): Unstaging changes, keeps work intact"
		}

	case "git_revert":
		// Revert is medium risk (creates new commit, reversible)
		assessment.Level = RiskMedium
		assessment.Reason = "git_revert: Creates new commit undoing changes, reversible via reset"

	case "git_checkout":
		// Switching branches is low risk (preserves work in stash if needed)
		assessment.Level = RiskLow
		assessment.Reason = "git_checkout: Branch switching, work preserved via stash if needed"

	case "git_tag":
		// Creating tags is medium risk (published via push)
		assessment.Level = RiskMedium
		assessment.Reason = "git_tag: Creates tag, reversible until pushed"
		assessment.Action = "Verify tag name and target commit before push"

	case "git_stash":
		action, _ := m["action"].(string)
		if action == "drop" || action == "clear" {
			assessment.Level = RiskMedium
			assessment.Reason = "git_stash drop/clear: Removes stashed changes"
			assessment.Action = "Verify stashed work is no longer needed"
		} else {
			assessment.Level = RiskLow
			assessment.Reason = "git_stash push/pop: Safe temporary storage for work"
		}

	default:
		assessment.Level = RiskMedium
		assessment.Reason = "Unknown git operation, applying medium risk default"
	}

	return assessment
}

// assessFileOpsRisk evaluates risk level for file operations
func (r *riskAwareController) assessFileOpsRisk(args json.RawMessage) RiskAssessment {
	assessment := RiskAssessment{Confidence: 0.85, Level: RiskMedium}

	if len(args) == 0 {
		return assessment
	}

	var m map[string]any
	if err := json.Unmarshal(args, &m); err != nil {
		return assessment
	}

	action, _ := m["action"].(string)

	switch action {
	case "mkdir":
		assessment.Level = RiskLow
		assessment.Reason = "Directory creation: reversible via delete"

	case "move":
		assessment.Level = RiskMedium
		assessment.Reason = "File/directory move: reversible by moving back, may break references"
		assessment.Action = "Check for file imports/references before moving"

	case "delete":
		assessment.Level = RiskHigh
		assessment.Reason = "File/directory deletion: not easily reversible if other files reference it"
		assessment.Action = "Verify no imports/references exist (use grep/lsp_references) before deleting"
		assessment.RequiresHalt = true
	}

	return assessment
}

// assessCommandRisk evaluates risk level for shell commands
func (r *riskAwareController) assessCommandRisk(args json.RawMessage) RiskAssessment {
	assessment := RiskAssessment{Confidence: 0.8, Level: RiskMedium}

	if len(args) == 0 {
		return assessment
	}

	var m map[string]any
	if err := json.Unmarshal(args, &m); err != nil {
		return assessment
	}

	cmd, _ := m["command"].(string)
	if cmd == "" {
		return assessment
	}

	lowerCmd := strings.ToLower(cmd)

	// Detect destructive patterns
	destructivePatterns := []struct {
		pattern      string
		risk         RiskLevel
		reason       string
		requiresHalt bool
	}{
		{"rm -rf", RiskHigh, "Recursive force delete: extremely destructive", true},
		{"git reset --hard", RiskHigh, "Git hard reset: discards all uncommitted work", true},
		{"git push --force", RiskHigh, "Force push: overwrites remote history", true},
		{"git clean -f", RiskHigh, "Git clean force: deletes untracked files", true},
		{":>.*\\.log", RiskMedium, "Log truncation: may destroy debugging data", false},
		{"chmod -R 000", RiskMedium, "Revoking all permissions: may lock files", false},
		{"dd if=/dev/", RiskHigh, "Direct disk write: can destroy filesystem", true},
	}

	for _, dp := range destructivePatterns {
		if strings.Contains(lowerCmd, dp.pattern) {
			assessment.Level = dp.risk
			assessment.Reason = dp.reason
			assessment.RequiresHalt = dp.requiresHalt
			assessment.Action = "Review command carefully. Consider safer alternative."
			return assessment
		}
	}

	// Build/test commands are low risk
	if strings.Contains(lowerCmd, "go build") || strings.Contains(lowerCmd, "go test") ||
		strings.Contains(lowerCmd, "make") || strings.Contains(lowerCmd, "npm test") {
		assessment.Level = RiskLow
		assessment.Reason = "Build/test command: verification activity, low risk"
		return assessment
	}

	// Install commands are medium risk
	if strings.Contains(lowerCmd, "go install") || strings.Contains(lowerCmd, "npm install") ||
		strings.Contains(lowerCmd, "cargo install") {
		assessment.Level = RiskMedium
		assessment.Reason = "Package installation: modifies system state, usually safe"
		return assessment
	}

	return assessment
}

// AllocateVerificationBudget reserves verification budget for high-risk actions
// Returns the number of verification steps that should be performed
func (r *riskAwareController) AllocateVerificationBudget(riskLevel RiskLevel) int {
	r.mu.Lock()
	defer r.mu.Unlock()

	switch riskLevel {
	case RiskHigh:
		// Check if we've exceeded high-risk cap
		if r.maxHighRiskPerSession > 0 && r.highRiskCount >= r.maxHighRiskPerSession {
			debug.Log("risk_controller", "high-risk cap reached (%d), denying action", r.maxHighRiskPerSession)
			return 0
		}
		r.highRiskCount++
		return r.verificationPerHighRisk
	case RiskMedium:
		r.mediumRiskCount++
		return 1 // Standard verification for medium-risk
	case RiskLow:
		// No verification budget needed for low-risk actions
		return 0
	default:
		return 1
	}
}

// GetStats returns risk statistics for the current session
func (r *riskAwareController) GetStats() map[string]interface{} {
	r.mu.Lock()
	defer r.mu.Unlock()

	return map[string]interface{}{
		"initial_budget":        r.initialBudget,
		"remaining_budget":      r.verificationBudget,
		"high_risk_count":       r.highRiskCount,
		"medium_risk_count":     r.mediumRiskCount,
		"unique_tools_used":     len(r.recentToolPatterns),
		"high_risk_cap":         r.maxHighRiskPerSession,
		"verification_per_high": r.verificationPerHighRisk,
	}
}

// formatRiskWarning generates a human-readable risk warning
func formatRiskWarning(assessment RiskAssessment) string {
	if assessment.Level == RiskLow {
		return ""
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("\n[Risk Assessment: %s]\n", assessment.Level))
	sb.WriteString(fmt.Sprintf("Reason: %s\n", assessment.Reason))

	if assessment.Action != "" {
		sb.WriteString(fmt.Sprintf("Recommended: %s\n", assessment.Action))
	}

	if assessment.RequiresHalt {
		sb.WriteString("\nThis action requires explicit confirmation or additional verification steps.\n")
		sb.WriteString("Consider gathering more evidence (tests, reviews, diffs) before proceeding.\n")
	}

	return sb.String()
}
