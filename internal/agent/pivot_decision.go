package agent

import (
	"encoding/json"
	"strings"
	"sync"
)

type pivotDecisionTracker struct {
	mu     sync.Mutex
	fails  map[string]int
	warned map[string]int
}

const (
	pivotFirstThreshold    = 3
	pivotForcedThreshold   = 6
	pivotMaxWarningsPerKey = 2
)

func newPivotDecisionTracker() *pivotDecisionTracker {
	return &pivotDecisionTracker{
		fails:  make(map[string]int),
		warned: make(map[string]int),
	}
}

func (p *pivotDecisionTracker) reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.fails = make(map[string]int)
	p.warned = make(map[string]int)
}

type pivotCommandArgs struct {
	Command string `json:"command"`
}

func (p *pivotDecisionTracker) recordToolCall(toolName, args string, isError bool, resultContent string) {
	if p == nil {
		return
	}
	key := pivotCommandKey(toolName, args)
	if key == "" {
		return
	}
	if isError && pivotEnvironmentalFailure(resultContent) {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !isError {
		delete(p.fails, key)

		delete(p.warned, key)
		return
	}
	p.fails[key]++
}

func (p *pivotDecisionTracker) checkAndWarn() string {
	if p == nil {
		return ""
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for key, n := range p.fails {
		if p.warned[key] >= pivotMaxWarningsPerKey {
			continue
		}
		if n < pivotFirstThreshold {
			continue
		}
		if n >= pivotForcedThreshold && p.warned[key] >= 1 {

			p.warned[key]++
			delete(p.fails, key)
			return "[pivot-decision] The command family `" + key + "` has now failed " +
				itoaPivot(n) + " consecutive times. Continuing to repair this exact path is no longer credible. " +
				"PIVOT: state in one line why this approach is failing, then name and switch to a DIFFERENT approach " +
				"(different tool, different scope, or ask the user). Do not run `" + key + "` again without that pivot statement."
		}
		if p.warned[key] == 0 {
			p.warned[key]++
			return "[pivot-decision] The command family `" + key + "` has failed " +
				itoaPivot(n) + " consecutive times. Before the next retry, make an explicit decision and state it in one line:\n" +
				"- REPAIR: keep this approach. Say why it is still viable and what concretely changes in the next attempt.\n" +
				"- PIVOT: abandon it. Name the different approach you switch to instead.\n" +
				"Silent incremental retries of the same command are the failure mode this interrupt exists to break."
		}
	}
	return ""
}

func pivotCommandKey(toolName, args string) string {
	if toolName != "run_command" {
		return ""
	}
	var ca pivotCommandArgs
	if err := json.Unmarshal([]byte(args), &ca); err != nil || ca.Command == "" {
		return ""
	}
	for _, line := range strings.Split(ca.Command, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if key := pivotMainSegment(line); key != "" {
			return key
		}
	}
	return ""
}

func pivotMainSegment(line string) string {
	for _, seg := range strings.Split(line, "&&") {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		fields := strings.Fields(seg)
		if len(fields) == 0 {
			continue
		}

		i := 0
		for i < len(fields) && (fields[i] == "sudo" || fields[i] == "env" || pivotIsEnvAssign(fields[i])) {
			i++
		}
		fields = fields[i:]
		if len(fields) == 0 {
			continue
		}

		if fields[0] == "cd" || fields[0] == "set" {
			continue
		}
		if len(fields) >= 2 {
			return fields[0] + " " + fields[1]
		}
		return fields[0]
	}
	return ""
}

func pivotIsEnvAssign(tok string) bool {
	i := strings.Index(tok, "=")
	if i <= 0 {
		return false
	}
	for _, c := range tok[:i] {
		if !(c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

func pivotEnvironmentalFailure(content string) bool {
	if content == "" {
		return false
	}
	env := []string{
		"signal: killed",
		"signal: terminated",
		"context deadline exceeded",
		"command timed out",
		"i/o timeout",
		"exit status 137",
		"exit status 124",
		"exit code 137",
		"exit code 124",
	}
	for _, m := range env {
		if strings.Contains(content, m) {
			return true
		}
	}
	return false
}

func itoaPivot(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
