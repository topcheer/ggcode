package agent

import (
	"encoding/json"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

var delegationToolNames = map[string]bool{
	"spawn_agent":       true,
	"use_namedagent":    true,
	"delegate":          true,
	"send_message":      true,
	"teammate_spawn":    true,
	"swarm_task_create": true,
	"a2a_remote":        true,
	"a2a_send_task":     true,
}

var fireAndForgetDelegationTools = map[string]bool{
	"swarm_task_create": true,
	"a2a_send_task":     true,
}

var delegationResultTools = map[string]bool{
	"wait_agent":          true,
	"list_agents":         true,
	"task_output":         true,
	"teammate_results":    true,
	"swarm_task_list":     true,
	"a2a_get_task":        true,
	"a2a_list_tasks":      true,
	"read_command_output": true,
}

var delegationSurveyResultTools = map[string]bool{
	"list_agents":     true,
	"swarm_task_list": true,
	"a2a_list_tasks":  true,
}

type delegationState struct {
	mu sync.Mutex

	activeDelegations map[string]*delegationEntry
	delegationSeq     int
	orphansWarned     map[string]bool
	orphanWarnCount   int

	delegationHistory []string
	serialWarnCount   int

	totalDelegations int
	totalToolCalls   int
	overDelWarned    bool

	lastResetIteration int
}

type delegationEntry struct {
	id           string
	resultID     string
	toolName     string
	taskSummary  string
	iteration    int
	lastChecked  int
	creationTime time.Time
}

const (
	delegationOrphanGateEnabled = false
	delegationSerialGateEnabled = false
)

const (
	orphanDelegationThreshold   = 8
	orphanDelegationMaxWarnings = 3
	serialDelegationThreshold   = 3
	serialDelegationMaxWarnings = 2
	overDelegationRatio         = 0.60
	overDelegationMinCalls      = 10
	delegationHistoryCap        = 10
)

func newDelegationState() *delegationState {
	return &delegationState{
		activeDelegations: make(map[string]*delegationEntry),
		orphansWarned:     make(map[string]bool),
	}
}

func (d *delegationState) recordDelegationCall(toolID, toolName, taskSummary, resultContent string, iteration int) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.totalDelegations++

	if delegationToolNames[toolName] && !fireAndForgetDelegationTools[toolName] {
		synID := toolID
		if synID == "" {
			d.delegationSeq++
			synID = string(rune('A'+d.delegationSeq%26)) + "-" + time.Now().Format("150405")
		}
		d.activeDelegations[synID] = &delegationEntry{
			id:           synID,
			resultID:     parseDelegationResultID(resultContent),
			toolName:     toolName,
			taskSummary:  truncateDelSummary(taskSummary),
			iteration:    iteration,
			lastChecked:  iteration,
			creationTime: time.Now(),
		}
	}

	d.delegationHistory = append(d.delegationHistory, toolName)
	if len(d.delegationHistory) > delegationHistoryCap {
		d.delegationHistory = d.delegationHistory[1:]
	}
}

func (d *delegationState) recordResultCheck(toolName string, args json.RawMessage, resultContent string, iteration int) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if len(d.activeDelegations) == 0 {
		return
	}

	if delegationSurveyResultTools[toolName] {
		for _, del := range d.activeDelegations {
			del.lastChecked = iteration
		}
		return
	}

	target := extractDelegationTarget(args)
	for _, del := range d.activeDelegations {
		if target != "" && delegationTargetMatches(del, target) {
			del.lastChecked = iteration
			continue
		}

		if resultContent != "" && ((del.resultID != "" && strings.Contains(resultContent, del.resultID)) || strings.Contains(resultContent, del.id)) {
			del.lastChecked = iteration
		}
	}
}

var delegationResultIDRe = regexp.MustCompile(`(?i)\bID:\s*([A-Za-z0-9][A-Za-z0-9._-]*)`)

func parseDelegationResultID(content string) string {
	if content == "" {
		return ""
	}
	m := delegationResultIDRe.FindStringSubmatch(content)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

func extractDelegationTarget(args json.RawMessage) string {
	if len(args) == 0 {
		return ""
	}
	var m map[string]interface{}
	if err := json.Unmarshal(args, &m); err != nil {
		return ""
	}
	for _, field := range []string{"agent_id", "task_id", "teammate_id", "job_id", "id", "to", "target"} {
		if v, ok := m[field]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

func delegationTargetMatches(del *delegationEntry, target string) bool {
	if target == "" {
		return false
	}
	if del.resultID != "" && del.resultID == target {
		return true
	}
	return del.id == target
}

func (d *delegationState) recordToolCallCount() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.totalToolCalls++
}

func (d *delegationState) resetForNewTurn() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.activeDelegations = make(map[string]*delegationEntry)
	d.orphansWarned = make(map[string]bool)
	d.delegationHistory = nil
	d.orphanWarnCount = 0
	d.serialWarnCount = 0
	d.totalDelegations = 0
	d.totalToolCalls = 0
	d.overDelWarned = false
}

func (d *delegationState) maybeWarnOrphanedDelegations(iteration int) string {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.orphanWarnCount >= orphanDelegationMaxWarnings {
		return ""
	}

	var orphans []string
	for delID, del := range d.activeDelegations {
		if d.orphansWarned[delID] {
			continue
		}

		if iteration-del.lastChecked >= orphanDelegationThreshold {
			orphans = append(orphans, del.toolName+": "+del.taskSummary)
			d.orphansWarned[delID] = true
		}
	}

	if len(orphans) == 0 {
		return ""
	}

	d.orphanWarnCount++
	debug.Log("agent", "Delegation orphan gate: %d unchecked delegations detected", len(orphans))

	var sb strings.Builder
	sb.WriteString("[delegation] Delegated tasks not checked in several iterations.\n\n")
	sb.WriteString("Unchecked delegations:\n")
	for _, o := range orphans {
		sb.WriteString("  - ")
		sb.WriteString(o)
		sb.WriteString("\n")
	}
	sb.WriteString("\nUse wait_agent / list_agents / teammate_results / task_output to retrieve results. ")
	sb.WriteString("Sub-agents may have completed, errored, or be stuck — unchecked delegations are a leading cause of silent failures in multi-agent workflows.\n")
	return sb.String()
}

func (d *delegationState) maybeWarnSerialDelegation() string {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.serialWarnCount >= serialDelegationMaxWarnings {
		return ""
	}

	consecutive := 0
	for i := len(d.delegationHistory) - 1; i >= 0; i-- {
		if delegationToolNames[d.delegationHistory[i]] {
			consecutive++
		} else {
			break
		}
	}

	if consecutive < serialDelegationThreshold {
		return ""
	}

	d.serialWarnCount++
	debug.Log("agent", "Serial delegation gate: %d consecutive delegations detected", consecutive)

	return "[Delegation Orchestration] You've made " + itoaDel(consecutive) +
		" consecutive delegation calls across separate turns. If these tasks are independent, " +
		"consider batching them: emit multiple spawn_agent calls in a SINGLE response to enable " +
		"parallel execution (3x latency reduction, per LLMCompiler arXiv:2312.04511). " +
		"Only serialize when tasks have data dependencies (one needs the other's output).\n"
}

func (d *delegationState) maybeWarnOverDelegation() string {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.overDelWarned || d.totalToolCalls < overDelegationMinCalls {
		return ""
	}

	ratio := float64(d.totalDelegations) / float64(d.totalToolCalls)
	if ratio <= overDelegationRatio {
		return ""
	}

	d.overDelWarned = true
	debug.Log("agent", "Over-delegation gate: %.0f%% delegation ratio (%d/%d)", ratio*100, d.totalDelegations, d.totalToolCalls)

	return "[Delegation Orchestration] High delegation ratio detected: " +
		itoaDel(d.totalDelegations) + "/" + itoaDel(d.totalToolCalls) +
		" tool calls were delegations (" + itoaDel(int(ratio*100)) + "%). " +
		"Over-delegation adds latency and cost overhead. Consider doing simpler tasks directly " +
		"and reserving delegation for genuinely parallelizable or specialized work.\n"
}

func (d *delegationState) removeDelegation(toolID string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.activeDelegations, toolID)
}

func truncateDelSummary(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 80 {
		return s[:77] + "..."
	}
	return s
}

func extractDelegationTaskSummary(toolName string, args json.RawMessage) string {
	if len(args) == 0 {
		return toolName
	}
	var m map[string]interface{}
	if err := json.Unmarshal(args, &m); err != nil {
		return toolName
	}

	for _, field := range []string{"task", "message", "prompt", "description", "subject"} {
		if v, ok := m[field]; ok {
			if s, ok := v.(string); ok && s != "" {
				return truncateDelSummary(s)
			}
		}
	}
	return toolName
}

func itoaDel(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [12]byte
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
