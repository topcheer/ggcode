package agent

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

const (
	reproLifecycleMaxWarnings = 1

	reproducerRerunGraceIterations = 2

	commandTokenMinLen = 3
)

type reproducerLifecycleState struct {
	mu sync.Mutex

	hasReproducer         bool
	reproducerIteration   int
	reproducerSnippet     string
	editedAfterReproducer bool
	editIteration         int
	reranAfterEdit        bool
	warned                bool
	intentSeenIter        int
}

func newReproducerLifecycleState() *reproducerLifecycleState {
	return &reproducerLifecycleState{}
}

func (s *reproducerLifecycleState) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hasReproducer = false
	s.reproducerIteration = 0
	s.reproducerSnippet = ""
	s.editedAfterReproducer = false
	s.editIteration = 0
	s.intentSeenIter = 0
	s.reranAfterEdit = false
	s.warned = false
}

var reproducerIntentRe = regexp.MustCompile(
	`(?i)\b(?:reproduc(?:e[rd]?|ing|tion)|repro\s+script|reproducer|demonstrate\s+(?:the\s+)?(?:error|bug|issue|crash)|minimal\s+(?:repro|example|test\s+case))\b`,
)

var reproducerCommandRe = regexp.MustCompile(
	`(?:^|[\s:"])(?:python3?|node|go\s+run|ruby|cargo\s+run|bash|sh)\s+\S+\.(?:py|js|ts|go|rb|rs|sh)`,
)

var reproducerTestRunnerRe = regexp.MustCompile(
	`(?:^|[\s:"])(?:go\s+test|cargo\s+test|make\s+test|npm\s+test|pytest)\b`,
)

var reproducerEditToolNames = sourceMutatingTools

var reproducerRunToolNames = map[string]bool{
	"run_command":   true,
	"start_command": true,
}

func reproducerRerunMatches(inp, snippet string) bool {
	if inp == "" {
		return false
	}
	if snippet == "" {

		return reproducerCommandRe.MatchString(inp) || reproducerTestRunnerRe.MatchString(inp)
	}
	return reproCommandTokenOverlap(inp, snippet)
}

func reproCommandTokenOverlap(a, b string) bool {
	tokensA := reproCommandTokens(a)
	tokensB := reproCommandTokens(b)
	if len(tokensA) == 0 || len(tokensB) == 0 {
		return false
	}
	for ta := range tokensA {
		if tokensB[ta] {
			return true
		}
	}
	return false
}

func reproCommandTokens(s string) map[string]bool {

	if t, handled := reproUnwrapCommandEnvelope(s); handled {
		s = t
	}
	generic := map[string]bool{
		"and": true, "the": true, "run": true, "bash": true, "sh": true,
		"python": true, "python3": true, "node": true, "ruby": true,
		"cargo": true, "go": true, "test": true, "tests": true, "cd": true,
		"echo": true, "make": true, "cmd": true, "src": true, "pkg": true,
		"internal": true, "desktop": true, "main": true, "github.com": true,
		"github": true, "www": true, "head": true, "git": true, "diff": true,
	}
	tokens := make(map[string]bool)
	for _, field := range strings.Fields(strings.ToLower(s)) {
		for _, comp := range strings.Split(field, "/") {
			comp = strings.Trim(comp, "\"'`$();|&~.:{}[],")
			if len(comp) < commandTokenMinLen || strings.HasPrefix(comp, "-") {
				continue
			}
			if generic[comp] {
				continue
			}
			tokens[comp] = true
		}
	}
	return tokens
}

func reproUnwrapCommandEnvelope(s string) (string, bool) {
	trimmed := strings.TrimSpace(s)
	if !strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "[") {
		return "", false
	}
	var env struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal([]byte(trimmed), &env); err != nil {
		return "", false
	}
	if env.Command != "" {
		return env.Command, true
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(trimmed), &m); err != nil {
		return "", true
	}
	var vals []string
	for _, v := range m {
		if str, ok := v.(string); ok && str != "" {
			vals = append(vals, str)
		}
	}
	return strings.Join(vals, " "), true
}

func (s *reproducerLifecycleState) observeToolCalls(iteration int, toolNames []string, toolInputs []string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for ti, tn := range toolNames {
		inp := ""
		if ti < len(toolInputs) {
			inp = toolInputs[ti]
		}

		if !s.hasReproducer {

			if reproducerRunToolNames[tn] && s.intentSeenIter == iteration && reproducerCommandRe.MatchString(inp) {
				s.hasReproducer = true
				s.reproducerIteration = iteration
				s.reproducerSnippet = firstLine(inp)
				debug.Log("agent", "reproducer-lifecycle: reproducer established at iter %d (%s)", iteration, s.reproducerSnippet)
			}
		}

		if s.hasReproducer && !s.editedAfterReproducer {
			if reproducerEditToolNames[tn] {
				s.editedAfterReproducer = true
				s.editIteration = iteration
				debug.Log("agent", "reproducer-lifecycle: edit after reproducer at iter %d", iteration)
			}
		}

		if s.editedAfterReproducer && !s.reranAfterEdit {
			if reproducerRunToolNames[tn] && reproducerRerunMatches(inp, s.reproducerSnippet) {
				s.reranAfterEdit = true
				debug.Log("agent", "reproducer-lifecycle: re-run after edit at iter %d", iteration)
			}
		}
	}
}

func (s *reproducerLifecycleState) observeText(iteration int, text string, hasRunTool bool, runInput string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if reproducerIntentRe.MatchString(text) {
		s.intentSeenIter = iteration
	}

	if !s.hasReproducer && reproducerIntentRe.MatchString(text) && hasRunTool {
		s.hasReproducer = true
		s.reproducerIteration = iteration

		if cmd := extractStringField(json.RawMessage(runInput), "command"); cmd != "" && reproducerTestRunnerRe.MatchString(cmd) {
			s.reproducerSnippet = firstLine(cmd)
		}
		debug.Log("agent", "reproducer-lifecycle: reproducer established via text at iter %d", iteration)
	}
}

func (s *reproducerLifecycleState) checkIncomplete(iteration int) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.warned {
		return ""
	}

	if !s.hasReproducer || !s.editedAfterReproducer || s.reranAfterEdit {
		return ""
	}
	if iteration-s.editIteration < reproducerRerunGraceIterations {
		return ""
	}

	s.warned = true
	hint := "[reproducer-lifecycle] You established a reproducer (iteration " +
		fmt.Sprintf("%d", s.reproducerIteration) + ") and then " +
		"edited source code (iteration " + fmt.Sprintf("%d", s.editIteration) + "), but the reproducer has not been re-run " +
		"to confirm the fix. Per SWE-bench best practices (Anthropic, 2025), always " +
		"re-run your reproducer script after editing to verify the error is actually " +
		"resolved before claiming success. Re-run: " + s.reproducerSnippet
	return hint
}

func extractToolNamesAndInputs(toolCalls []provider.ToolCallDelta) ([]string, []string) {
	names := make([]string, 0, len(toolCalls))
	inputs := make([]string, 0, len(toolCalls))
	for _, tcd := range toolCalls {
		names = append(names, tcd.Name)
		inputs = append(inputs, string(tcd.Arguments))
	}
	return names, inputs
}

func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if len(line) > 80 {
			line = line[:77] + "..."
		}
		return line
	}
	return ""
}
