package agent

import (
	"fmt"
	"strings"
)

const (
	verificationDebtThreshold = 5

	verificationDebtMinTotal = 6

	verificationDebtMaxWarn = 2
)

type debtAction int

const (
	debtGrounding debtAction = iota
	debtModifying
	debtVerifying
	debtNeutral
)

var verificationDebtTools = map[string]debtAction{

	"read_file":               debtGrounding,
	"multi_file_read":         debtGrounding,
	"search_files":            debtGrounding,
	"grep":                    debtGrounding,
	"glob":                    debtGrounding,
	"code_search":             debtGrounding,
	"list_directory":          debtGrounding,
	"lsp_hover":               debtGrounding,
	"lsp_definition":          debtGrounding,
	"lsp_references":          debtGrounding,
	"lsp_symbols":             debtGrounding,
	"lsp_workspace_symbols":   debtGrounding,
	"lsp_implementation":      debtGrounding,
	"lsp_document_highlights": debtGrounding,
	"web_search":              debtGrounding,
	"web_fetch":               debtGrounding,

	"edit_file":       debtModifying,
	"write_file":      debtModifying,
	"multi_edit_file": debtModifying,
	"multi_file_edit": debtModifying,
	"file_ops":        debtModifying,
	"notebook_edit":   debtModifying,
	"batch_replace":   debtModifying,

	"run_command":     debtVerifying,
	"lsp_diagnostics": debtVerifying,

	"lsp_code_actions": debtGrounding,
}

func classifyDebtAction(toolName, args string) debtAction {
	if action, ok := verificationDebtTools[toolName]; ok {
		if action == debtVerifying && toolName == "run_command" {

			if !isVerificationCommand(eaExtractCommand(args)) {
				return debtNeutral
			}
		}
		return action
	}
	return debtNeutral
}

func isCommandChannelTool(name string) bool {
	switch name {
	case "run_command", "bash", "powershell", "start_command", "wait_command", "read_command_output":
		return true
	}
	return false
}

func verifyDebtSpawnClearingTool(name string) bool {
	return isCommandChannelTool(name) && name != "start_command"
}

func isVerificationCommand(args string) bool {

	args = stripLeadingShellComment(args)
	tokens := strings.Fields(strings.ToLower(args))

	i := 0
	for i < len(tokens) && strings.Contains(tokens[i], "=") {
		i++
	}
	if i >= len(tokens) {
		return false
	}
	first := tokens[i]
	rest := tokens[i+1:]

	switch first {
	case "pytest", "jest", "mypy", "flake8", "eslint", "prettier", "tsc",
		"typecheck", "golangci-lint", "staticcheck", "ruff":
		return true
	}

	switch first {
	case "go", "make", "npm", "yarn", "pnpm", "cargo", "mvn", "mvnw", "./mvnw", "gradle",
		"./gradlew", "dotnet", "tox", "nox":
	default:
		return false
	}
	verifyMarkers := []string{
		"test", "build", "vet", "check", "lint", "verify", "clippy",
		"e2e", "compile", "typecheck", "tsc",
	}
	for _, f := range rest {
		f = strings.TrimPrefix(f, "run:")
		f = strings.TrimSuffix(f, ";")
		for _, m := range verifyMarkers {
			if f == m || strings.HasPrefix(f, m+"-") || strings.HasPrefix(f, m+"_") {
				return true
			}
		}
	}
	return false
}

type verificationDebtState struct {
	totalCalls     int
	modifyCount    int
	groundCount    int
	verifyCount    int
	debt           int
	maxDebt        int
	warningsIssued int
	lastAction     debtAction
	editedPkgs     map[string]bool
}

func newVerificationDebtState() *verificationDebtState {
	return &verificationDebtState{editedPkgs: make(map[string]bool)}
}

func (v *verificationDebtState) reset() {
	v.totalCalls = 0
	v.modifyCount = 0
	v.groundCount = 0
	v.verifyCount = 0
	v.debt = 0
	v.maxDebt = 0
	v.warningsIssued = 0
	v.lastAction = debtNeutral
	v.editedPkgs = make(map[string]bool)
}

func (v *verificationDebtState) recordToolCall(toolName, args string) {
	action := classifyDebtAction(toolName, args)
	v.totalCalls++
	v.lastAction = action

	switch action {
	case debtModifying:
		v.modifyCount++
		v.debt++
		for _, f := range extractFileHints(toolName, []byte(args)) {
			if pkg := fileToPkgKey(f); pkg != "" {
				v.editedPkgs[pkg] = true
			}
		}
		if v.debt > v.maxDebt {
			v.maxDebt = v.debt
		}
	case debtGrounding:
		v.groundCount++

		if v.debt > 0 {
			v.debt--
		}
	case debtVerifying:
		v.verifyCount++

		if v.debt > 0 {
			if covered, total, ok := v.verificationCoverage(args); ok && total > 0 && covered < total {
				v.debt = v.debt * (total - covered) / total
			} else {
				v.debt = 0
			}
		}
	case debtNeutral:

	}
}

func (v *verificationDebtState) maybeWarn() string {
	if v.warningsIssued >= verificationDebtMaxWarn {
		return ""
	}
	if v.totalCalls < verificationDebtMinTotal {
		return ""
	}
	if v.debt < verificationDebtThreshold {
		return ""
	}

	v.warningsIssued++

	return fmt.Sprintf(
		"[Verification Debt] You have made %d file modifications since the last "+
			"build/test/diagnostic check. Each unverified edit compounds the risk "+
			"of building on stale or incorrect premises (SAUP: uncertainty propagation, "+
			"ACL 2025). Before making more changes, run a build or test to verify your "+
			"edits are correct so far. This prevents error cascades where a single wrong "+
			"assumption propagates through all subsequent edits.",
		v.debt,
	)
}

func (v *verificationDebtState) verificationCoverage(args string) (int, int, bool) {
	scopes, ok := extractVerificationScopes(args)
	if !ok {
		return 0, 0, false
	}
	if len(v.editedPkgs) == 0 {
		return 0, 0, false
	}
	total := len(v.editedPkgs)
	covered := 0
	for pkg := range v.editedPkgs {

		rel := strings.TrimPrefix(pkg, "/")
		for _, sc := range scopes {
			if sc == "." || rel == sc || strings.HasPrefix(rel, sc+"/") ||
				strings.HasSuffix(rel, "/"+sc) {
				covered++
				break
			}
		}
	}
	return covered, total, true
}

func fileToPkgKey(f string) string {
	f = strings.TrimPrefix(f, "./")
	i := strings.LastIndexByte(f, '/')
	if i < 0 {
		return ""
	}
	return f[:i]
}

func extractVerificationScopes(args string) ([]string, bool) {
	cmd := eaExtractCommand(args)
	if cmd == "" {
		return nil, false
	}
	if !isVerificationCommand(cmd) {
		return nil, false
	}
	scopes := parseGoPackageScopes(cmd)
	if scopes == nil {

		return []string{"."}, true
	}
	return scopes, true
}

func parseGoPackageScopes(cmd string) []string {
	fields := strings.Fields(cmd)
	var scopes []string
	for _, f := range fields {
		f = strings.TrimSuffix(f, "...")
		f = strings.TrimRight(f, "/")
		if f == "" {
			continue
		}

		if strings.HasSuffix(f, ".go") {
			if idx := strings.LastIndex(f, "/"); idx > 0 {
				f = f[:idx]
			} else {
				continue
			}
		}
		if strings.HasPrefix(f, "./") || strings.HasPrefix(f, "../") {
			scopes = append(scopes, strings.TrimPrefix(f, "./"))
			continue
		}

		if !strings.HasPrefix(f, "-") && strings.Contains(f, "/") &&
			!strings.Contains(f, ":") && isASCIIPathStart(f) {
			scopes = append(scopes, strings.TrimSuffix(f, "/"))
		}
	}
	return scopes
}

func isASCIIPathStart(f string) bool {
	if f == "" {
		return false
	}
	c := f[0]
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}
