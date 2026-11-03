package agent

import (
	"fmt"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	propagateWarnSteps = 2

	propagateMaxChains = 8

	degradedMinContentLen = 15

	degradedPatternMaxLen = 50

	degradedMaxChainsWarn = 2
)

type degradedKind int

const (
	degradedNone degradedKind = iota
	degradedEmpty
	degradedNullish
	degradedTruncated
	degradedNoResult
)

func (k degradedKind) String() string {
	switch k {
	case degradedEmpty:
		return "empty"
	case degradedNullish:
		return "nullish"
	case degradedTruncated:
		return "truncated"
	case degradedNoResult:
		return "no-result"
	default:
		return "none"
	}
}

type degradedOutput struct {
	toolName string
	kind     degradedKind
	step     int
}

type propagationChain struct {
	origin     degradedOutput
	downstream int
	guided     bool
}

type errorPropagateState struct {
	mu sync.Mutex

	chains []*propagationChain

	totalSteps int

	warningsFired int
}

func newErrorPropagateState() *errorPropagateState {
	return &errorPropagateState{}
}

func (e *errorPropagateState) reset() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.chains = nil
	e.totalSteps = 0
	e.warningsFired = 0
}

var degradedPatterns = []string{
	"no results", "no matches", "0 matches", "0 results",
	"nothing found", "not found", "no such file", "file not found",
	"no elements", "empty result", "zero results",
	"no symbols", "no definitions", "no references",
}

var truncationFooterPrefixes = []string{
	"[file truncated:", "[showing lines", "[output too large",
	"[truncated:", "... [output truncated]", "... results omitted",

	toolHeadTailTruncationPrefix,
}

func hasTruncationFooter(content string) bool {
	for _, ln := range strings.Split(content, "\n") {
		t := strings.TrimSpace(strings.ToLower(ln))
		for _, p := range truncationFooterPrefixes {
			if strings.HasPrefix(t, p) {
				return true
			}
		}
	}
	return false
}

var nullishValues = []string{
	"null", "nil", "none", "undefined", "n/a", "{}", "[]",
}

func classifyDegraded(toolName, content string) degradedKind {
	trimmed := strings.TrimSpace(content)

	if trimmed == "" {
		return degradedEmpty
	}

	lower := strings.ToLower(trimmed)

	if len(lower) <= 12 {
		for _, nv := range nullishValues {
			if lower == nv {
				return degradedNullish
			}
		}
	}

	if hasTruncationFooter(trimmed) && !hasPaginationGuidance(trimmed) {
		return degradedTruncated
	}

	if len(trimmed) <= degradedPatternMaxLen {
		for _, p := range degradedPatterns {
			if strings.Contains(lower, p) {
				return degradedNoResult
			}
		}
	}

	if isContentTool(toolName) && !isPathListTool(toolName) && len(trimmed) < degradedMinContentLen {
		return degradedEmpty
	}

	return degradedNone
}

func isPathListTool(toolName string) bool {
	switch toolName {
	case "glob", "grep":
		return true
	default:
		return false
	}
}

func isContentTool(toolName string) bool {
	switch toolName {
	case "read_file", "multi_file_read", "grep", "search_files", "code_search",
		"lsp_references", "lsp_definition", "lsp_workspace_symbols",
		"lsp_implementation", "web_search", "web_fetch", "glob":
		return true
	default:
		return false
	}
}

func (e *errorPropagateState) recordGuardedTruncation(toolName, content string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if hasPaginationGuidance(content) {
		return
	}
	for i := len(e.chains) - 1; i >= 0; i-- {
		c := e.chains[i]
		if c.origin.step < e.totalSteps {
			break
		}
		if c.origin.toolName == toolName {
			return
		}
	}
	if len(e.chains) < propagateMaxChains {
		e.chains = append(e.chains, &propagationChain{
			origin: degradedOutput{
				toolName: toolName,
				kind:     degradedTruncated,
				step:     e.totalSteps,
			},
		})
	}
}

func (e *errorPropagateState) recordResult(toolName, content string, isError bool) string {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.totalSteps++

	for _, c := range e.chains {
		if c.guided {
			continue
		}
		c.downstream++
	}

	var guidance string
	if !isError {
		kind := classifyDegraded(toolName, content)
		if kind != degradedNone {

			if len(e.chains) < propagateMaxChains {
				e.chains = append(e.chains, &propagationChain{
					origin: degradedOutput{
						toolName: toolName,
						kind:     kind,
						step:     e.totalSteps,
					},
				})
			}
		}
	}

	for _, c := range e.chains {
		if c.guided || c.downstream < propagateWarnSteps {
			continue
		}
		if e.warningsFired >= degradedMaxChainsWarn {
			break
		}
		c.guided = true
		e.warningsFired++
		guidance = e.formatPropagationGuidance(c)
		break
	}

	if len(e.chains) > propagateMaxChains {
		e.chains = e.chains[len(e.chains)-propagateMaxChains:]
	}

	if guidance != "" {
		debug.Log("agent", "error_propagate: propagation chain detected (warning %d/%d)", e.warningsFired, degradedMaxChainsWarn)
	}

	return guidance
}

func (e *errorPropagateState) formatPropagationGuidance(c *propagationChain) string {
	return fmt.Sprintf(
		"[Error Propagation Chain] A prior tool call (%s, step %d) returned a "+
			"degraded %s result that was NOT flagged as an error, yet %d "+
			"subsequent tool calls may have consumed it. There is no data-flow "+
			"tracking, so these later calls may be unrelated -- but if any of "+
			"them built on the %s output, reasoning derived from empty/truncated/null "+
			"intermediate outputs compounds silently. Before continuing, verify "+
			"whether the %s output from %s (step %d) was actually valid. If it was "+
			"genuinely empty (e.g., no search results), confirm your approach is "+
			"still grounded rather than building conclusions on missing data.",
		c.origin.toolName, c.origin.step, c.origin.kind.String(),
		c.downstream, c.origin.kind.String(),
		c.origin.kind.String(), c.origin.toolName, c.origin.step,
	)
}

func hasPaginationGuidance(content string) bool {

	lines := strings.Split(content, "\n")
	if len(lines) > 5 {
		lines = lines[len(lines)-5:]
	}
	tail := strings.ToLower(strings.Join(lines, "\n"))

	return strings.Contains(tail, "offset/limit") ||
		strings.Contains(tail, "offset and limit") ||
		strings.Contains(tail, "use read_command_output") ||
		strings.Contains(tail, "use wait_command") ||
		strings.Contains(tail, "use web_search") ||
		strings.Contains(tail, "use the browser tool") ||
		strings.Contains(tail, "narrow your search") ||
		strings.Contains(tail, "refine your query")
}
