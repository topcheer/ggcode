package agent

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

type integrationState struct {
	warnings        int
	pendingEvidence []string
	pendingTool     string
}

func newIntegrationState() *integrationState {
	return &integrationState{}
}

func (s *integrationState) reset() {
	s.warnings = 0
	s.pendingEvidence = nil
	s.pendingTool = ""
}

const integrationMaxWarnings = 2

const integrationMaxPendingEvidence = 8

var toolsForIntegration = map[string]bool{
	"read_file":       true,
	"multi_file_read": true,
	"grep":            true,
	"search_files":    true,
	"code_search":     true,
	"lsp_definition":  true,
	"lsp_references":  true,
	"lsp_symbols":     true,
	"lsp_hover":       true,
	"lsp_diagnostics": true,
	"git_show":        true,
	"git_log":         true,
	"git_diff":        true,
	"git_blame":       true,
	"web_search":      true,
	"web_fetch":       true,
}

var integrationMutatingTools = derivedEditTools(map[string]bool{
	"run_command":   true,
	"start_command": true,
	"git_add":       true,
	"git_commit":    true,
	"git_reset":     true,
	"git_revert":    true,
	"git_checkout":  true,
	"git_stash":     true,
	"git_tag":       true,
})

var (
	integrationFilePathRe    = regexp.MustCompile(`(?:[\w.-]+/)+[\w.-]+\.\w+`)
	integrationLineRefRe     = regexp.MustCompile(`(?i)(?:\bline\s*[:：]?\s*|\bl\s*[:：]?\s*|第\s*)(\d{1,4})(?:\s*行)?`)
	integrationGoSymbolRe    = regexp.MustCompile(`\b(?:func|type|var|const)\s+([A-Z]\w+)`)
	integrationSummaryLineRe = regexp.MustCompile(`^\d+ file\(s\) matched$`)
	integrationPathLineRe    = regexp.MustCompile(`^[\w./~-]+\.[A-Za-z]{1,6}$`)
)

const minEvidenceLen = 4

var integrationDirNoise = map[string]bool{
	"internal": true, "agent": true, "pkg": true, "src": true,
	"test": true, "tests": true, "vendor": true, "example": true,
	"examples": true, "main": true, "app": true, "lib": true,
	"util": true, "utils": true, "common": true, "core": true,
	"package": true, "content": true, "docs": true, "site": true,
}

func (s *integrationState) recordToolEvidence(toolName, content string) {
	if integrationMutatingTools[toolName] {
		s.consumeEvidenceOnMutation(toolName, content)
		return
	}
	if !toolsForIntegration[toolName] || content == "" {
		return
	}

	if isPathListOutput(content) {
		return
	}

	tokens := extractEvidenceTokens(content)
	if len(tokens) == 0 {
		return
	}

	s.appendEvidence(tokens)
	s.pendingTool = toolName
}

func extractEvidenceTokens(content string) []string {

	scan := content
	if len(scan) > 3000 {
		scan = scan[:3000]
	}

	var tokens []string
	seen := make(map[string]bool)

	extract := func(re *regexp.Regexp, group int) {
		matches := re.FindAllStringSubmatch(scan, 10)
		for _, m := range matches {
			var tok string
			if group < len(m) {
				tok = m[group]
			}
			tok = strings.TrimSpace(strings.ToLower(tok))
			if len(tok) >= minEvidenceLen && !seen[tok] && !isCommonNoise(tok) {
				seen[tok] = true
				tokens = append(tokens, tok)
			}
		}
	}

	extract(integrationFilePathRe, 0)

	extract(integrationLineRefRe, 0)
	extract(integrationGoSymbolRe, 1)
	return tokens
}

func (s *integrationState) appendEvidence(tokens []string) {
	for _, tok := range tokens {
		dup := false
		for _, existing := range s.pendingEvidence {
			if existing == tok {
				dup = true
				break
			}
		}
		if !dup {
			s.pendingEvidence = append(s.pendingEvidence, tok)
		}
	}
	if len(s.pendingEvidence) > integrationMaxPendingEvidence {
		s.pendingEvidence = s.pendingEvidence[len(s.pendingEvidence)-integrationMaxPendingEvidence:]
	}
}

func (s *integrationState) consumeEvidenceOnMutation(toolName, content string) {
	if len(s.pendingEvidence) == 0 {
		return
	}

	scan := content
	if len(scan) > 2000 {
		scan = scan[:2000]
	}
	targets := integrationFilePathRe.FindAllString(scan, 5)

	consumed := 0
	for _, ev := range s.pendingEvidence {
		if !strings.Contains(ev, "/") {
			continue
		}
		evBase := filepath.Base(ev)
		for _, t := range targets {
			if strings.EqualFold(filepath.Base(t), evBase) {
				consumed++
				break
			}
		}
	}
	if consumed > 0 {
		debug.Log("integration_monitor", "evidence integrated by action: tool=%s consumed=%d", toolName, consumed)
	}

	s.pendingEvidence = nil
	s.pendingTool = ""
}

func isPathListOutput(content string) bool {
	scan := content
	if len(scan) > 3000 {
		scan = scan[:3000]
	}
	lines := strings.Split(strings.TrimRight(scan, "\n"), "\n")

	if n := len(lines); n > 0 {
		if last := strings.TrimSpace(lines[n-1]); integrationSummaryLineRe.MatchString(last) {
			lines = lines[:n-1]
		}
	}
	paths := 0
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if strings.ContainsAny(l, " \t") {
			return false
		}
		if !strings.Contains(l, "/") {
			return false
		}
		if !integrationPathLineRe.MatchString(l) {
			return false
		}
		paths++
	}
	return paths >= 2
}

func (s *integrationState) checkIntegration(assistantText string) string {
	if len(s.pendingEvidence) == 0 || s.warnings >= integrationMaxWarnings {
		s.pendingEvidence = nil
		s.pendingTool = ""
		return ""
	}

	evidence := s.pendingEvidence
	toolName := s.pendingTool

	if assistantText == "" {
		return ""
	}

	s.pendingEvidence = nil
	s.pendingTool = ""

	lower := strings.ToLower(assistantText)
	integrated := 0
	for _, tok := range evidence {
		if evidenceTokenInText(tok, lower) {
			integrated++
		}
	}

	threshold := len(evidence) / 3
	if threshold < 1 {
		threshold = 1
	}

	if integrated >= threshold {
		return ""
	}

	s.warnings++
	debug.Log("integration_monitor", "evidence not integrated: tool=%s tokens=%d integrated=%d", toolName, len(evidence), integrated)

	example := evidence[0]
	if len(evidence) > 1 {
		example = evidence[0] + ", " + evidence[1]
	}

	return fmt.Sprintf(
		"[evidence] Previous %s returned key info (%s) not in your reasoning. Integrate findings before proceeding.",
		toolName, example,
	)
}

func evidenceTokenInText(tok, lowerText string) bool {
	if strings.Contains(lowerText, tok) {
		return true
	}

	if m := integrationLineRefRe.FindStringSubmatch(tok); m != nil {
		digits := m[1]
		idx := strings.Index(lowerText, digits)
		for idx >= 0 {
			beforeOK := idx == 0 || !isDigit(lowerText[idx-1])
			after := idx + len(digits)
			afterOK := after >= len(lowerText) || !isDigit(lowerText[after])
			if beforeOK && afterOK {
				return true
			}
			next := strings.Index(lowerText[idx+1:], digits)
			if next < 0 {
				break
			}
			idx = idx + 1 + next
		}
		return false
	}
	if !strings.Contains(tok, "/") {
		return false
	}
	base := filepath.Base(tok)
	if len(base) >= minEvidenceLen && !isCommonNoise(base) && strings.Contains(lowerText, base) {
		return true
	}

	dir := filepath.Dir(tok)
	if dir != "." && dir != "/" && !strings.HasSuffix(dir, "\\") {
		pkg := filepath.Base(dir)
		if len(pkg) >= minEvidenceLen && !integrationDirNoise[pkg] && !isCommonNoise(pkg) && strings.Contains(lowerText, pkg) {
			return true
		}
	}
	return false
}

func isCommonNoise(tok string) bool {
	noise := map[string]bool{
		"main.go":     true,
		"go.mod":      true,
		"go.sum":      true,
		"makefile":    true,
		"readme.md":   true,
		"readme":      true,
		"package":     true,
		"import":      true,
		"function":    true,
		"error":       true,
		"string":      true,
		"table":       true,
		"true":        true,
		"false":       true,
		"none":        true,
		"null":        true,
		"type":        true,
		"value":       true,
		"return":      true,
		"foo":         true,
		"bar":         true,
		"test":        true,
		"example.com": true,
		"http":        true,
		"https":       true,
	}
	return noise[tok]
}

func (a *Agent) integrationRecordToolResult(toolName, content string) {
	if a.integrationMonitor == nil {
		return
	}
	a.integrationMonitor.recordToolEvidence(toolName, content)
}

func (a *Agent) integrationCheckAndWarn(assistantText string) {
	if a.integrationMonitor == nil {
		return
	}
	if hint := a.integrationMonitor.checkIntegration(assistantText); hint != "" {
		debug.Log("integration_monitor", "guidance injected: evidence not integrated")
		a.injectGuidance(hint)
	}
}

func (a *Agent) integrationResetForRun() {
	if a.integrationMonitor != nil {
		a.integrationMonitor.reset()
	}
}
