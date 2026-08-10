package agent

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
)

// SafetyContract represents a formal safety contract that policies must adhere to.
// Based on AgentVerify (2026) formal verification approach.
type SafetyContract struct {
	Name        string
	Description string
	// Verifiers are deterministic checkers that evaluate if an action complies
	Verifiers []PolicyVerifier
}

// PolicyVerifier is a function that verifies an action against a safety contract.
// Returns true if the action complies, false if it violates, and an explanation.
type PolicyVerifier func(toolName string, params map[string]interface{}) (complies bool, reason string)

// PolicyVerifierService intercepts and verifies agent-initiated actions
// before execution, ensuring they adhere to formally-specified safety contracts.
// This implements the "Policy Enforcement" phase from AgentVerify/VeriGuard.
type PolicyVerifierService struct {
	contracts []*SafetyContract
	mu        sync.RWMutex
	stats     VerificationStats
}

// VerificationStats tracks policy verification statistics.
type VerificationStats struct {
	TotalVerifications int
	Compliant          int
	Violated           int
	Warnings           int
}

// NewPolicyVerifierService creates a new policy verifier service with default safety contracts.
func NewPolicyVerifierService() *PolicyVerifierService {
	pv := &PolicyVerifierService{}
	pv.registerDefaultContracts()
	return pv
}

// registerDefaultContracts initializes standard safety contracts.
func (pv *PolicyVerifierService) registerDefaultContracts() {
	// Contract 1: No shell commands with destructive patterns
	pv.RegisterContract(&SafetyContract{
		Name:        "destructive-shell-guard",
		Description: "Prevent shell commands with destructive patterns (rm -rf, :(){...}|:, etc.)",
		Verifiers: []PolicyVerifier{
			verifyDestructiveShell,
		},
	})

	// Contract 2: No writes to critical system paths
	pv.RegisterContract(&SafetyContract{
		Name:        "critical-path-protection",
		Description: "Prevent writes to system-critical directories (/etc, /usr, /sys, /proc)",
		Verifiers: []PolicyVerifier{
			verifyCriticalPathWrites,
		},
	})

	// Contract 3: No hardcoded secrets in generated files
	pv.RegisterContract(&SafetyContract{
		Name:        "secret-exclusion",
		Description: "Prevent writing files containing hardcoded API keys or secrets",
		Verifiers: []PolicyVerifier{
			verifySecretExclusion,
		},
	})
}

// RegisterContract adds a new safety contract for verification.
func (pv *PolicyVerifierService) RegisterContract(contract *SafetyContract) {
	pv.mu.Lock()
	defer pv.mu.Unlock()
	pv.contracts = append(pv.contracts, contract)
}

// VerifyAction checks if an action complies with all registered safety contracts.
// This implements the "intercept and evaluate" phase from AgentVerify.
// Returns:
//   - compliant: true if action passes all verifications
//   - violation: contract name if violated, empty if compliant
//   - reason: human-readable explanation
func (pv *PolicyVerifierService) VerifyAction(toolName string, params map[string]interface{}) (compliant bool, violation string, reason string) {
	pv.mu.Lock()
	defer pv.mu.Unlock()

	pv.stats.TotalVerifications++

	for _, contract := range pv.contracts {
		for _, verifier := range contract.Verifiers {
			complies, reason := verifier(toolName, params)
			if !complies {
				pv.stats.Violated++
				return false, contract.Name, fmt.Sprintf("[%s] %s", contract.Name, reason)
			}
		}
	}

	pv.stats.Compliant++
	return true, "", ""
}

// Stats returns current verification statistics.
func (pv *PolicyVerifierService) Stats() VerificationStats {
	pv.mu.RLock()
	defer pv.mu.RUnlock()
	return pv.stats
}

// Predefined verifiers

// verifyDestructiveShell checks for dangerous shell command patterns.
func verifyDestructiveShell(toolName string, params map[string]interface{}) (bool, string) {
	if toolName != "run_command" && toolName != "start_command" {
		return true, ""
	}

	cmd, ok := params["command"].(string)
	if !ok {
		return true, ""
	}

	dangerousPatterns := []string{
		"rm -rf /",
		"rm -rf /*",
		":(){:|:&};:", // fork bomb
		"dd if=/dev/zero",
		"> /dev/sda",
		":> /dev/null",
		"mkfs.ext",
		"chmod 777 /",
	}

	lowerCmd := strings.ToLower(cmd)
	for _, pattern := range dangerousPatterns {
		if strings.Contains(lowerCmd, pattern) {
			return false, fmt.Sprintf("destructive shell pattern detected: %s", pattern)
		}
	}

	return true, ""
}

// verifyCriticalPathWrites prevents writes to system-critical directories.
func verifyCriticalPathWrites(toolName string, params map[string]interface{}) (bool, string) {
	writeTools := map[string]bool{
		"write_file":       true,
		"multi_file_write": true,
		"edit_file":        true,
		"multi_file_edit":  true,
		"batch_replace":    true,
		"file_ops":         true,
	}

	if !writeTools[toolName] {
		return true, ""
	}

	criticalPaths := []string{
		"/etc/",
		"/usr/",
		"/sys/",
		"/proc/",
		"/boot/",
		"/lib/",
		"/lib64/",
	}

	// Check file path parameters
	pathParams := []string{"file", "path", "files", "source", "destination"}
	for _, paramKey := range pathParams {
		if path, ok := params[paramKey]; ok {
			pathStr := fmt.Sprintf("%v", path)
			for _, critical := range criticalPaths {
				if strings.HasPrefix(pathStr, critical) {
					return false, fmt.Sprintf("write attempt to critical system path: %s", pathStr)
				}
			}
		}
	}

	// For files parameter (array of file objects)
	if files, ok := params["files"].([]interface{}); ok {
		for _, f := range files {
			if fileMap, ok := f.(map[string]interface{}); ok {
				if path, ok := fileMap["path"].(string); ok {
					for _, critical := range criticalPaths {
						if strings.HasPrefix(path, critical) {
							return false, fmt.Sprintf("write attempt to critical system path: %s", path)
						}
					}
				}
			}
		}
	}

	// Also check for typed []map[string]interface{} (as used in tests)
	if filesMap, ok := params["files"].([]map[string]interface{}); ok {
		for _, f := range filesMap {
			if path, ok := f["path"].(string); ok {
				for _, critical := range criticalPaths {
					if strings.HasPrefix(path, critical) {
						return false, fmt.Sprintf("write attempt to critical system path: %s", path)
					}
				}
			}
		}
	}

	return true, ""
}

// verifySecretExclusion prevents writing files with hardcoded secrets.
func verifySecretExclusion(toolName string, params map[string]interface{}) (bool, string) {
	writeTools := map[string]bool{
		"write_file":       true,
		"multi_file_write": true,
	}

	if !writeTools[toolName] {
		return true, ""
	}

	// Check content parameter
	content, ok := params["content"].(string)
	if !ok {
		return true, ""
	}

	// Secret patterns (basic detection)
	secretPatterns := []*regexp.Regexp{
		regexp.MustCompile(`(?i)(api[_-]?key|secret[_-]?key|auth[_-]?token)\s*[:=]\s*['"]?[a-zA-Z0-9]{20,}['"]?`),
		regexp.MustCompile(`(?i)sk-[a-zA-Z0-9]{20,}`), // OpenAI key pattern
		regexp.MustCompile(`(?i)Bearer\s+[a-zA-Z0-9]{20,}`),
		regexp.MustCompile(`(?i)password\s*[:=]\s*['"]?[^\s'"]{8,}['"]?`),
	}

	for _, pattern := range secretPatterns {
		if pattern.MatchString(content) {
			match := pattern.FindString(content)
			// Truncate match for security
			if len(match) > 30 {
				match = match[:30] + "..."
			}
			return false, fmt.Sprintf("potential secret detected in content: %s", match)
		}
	}

	return true, ""
}

// VerifyToolCall is a convenience method for integrating into the agent loop.
func (pv *PolicyVerifierService) VerifyToolCall(toolName string, params map[string]interface{}) error {
	compliant, violation, reason := pv.VerifyAction(toolName, params)
	if !compliant {
		return &PolicyViolationError{
			Violation: violation,
			Reason:    reason,
			ToolName:  toolName,
		}
	}
	return nil
}

// PolicyViolationError represents a policy verification violation.
type PolicyViolationError struct {
	Violation string
	Reason    string
	ToolName  string
}

func (e *PolicyViolationError) Error() string {
	return fmt.Sprintf("policy violation [%s] in tool '%s': %s", e.Violation, e.ToolName, e.Reason)
}
