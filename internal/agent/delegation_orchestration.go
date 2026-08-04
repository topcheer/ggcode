package agent

import (
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

// Delegation Orchestration Intelligence - inspired by LangGraph (state graph
// orchestration), CrewAI (role-based task assignment), OpenAI Swarm (lightweight
// handoff), and Devin (multi-agent collaboration automation).
//
// This module provides three zero-LLM-cost deterministic checks:
//
// 1. Orphaned Delegation Detection: tracks spawned sub-agents and teammates
//    whose results were never consumed (no wait_agent/teammate_results check
//    within N iterations). Analogous to bgOrphan detection for background
//    commands. Prevents silent sub-agent failures from going unnoticed.
//
// 2. Serial Delegation Anti-Pattern: detects consecutive spawn_agent / delegate
//    calls across iterations that operate on independent tasks. When 3+
//    independent delegations occur sequentially, recommends parallelization
//    (batch spawn_agent calls in a single turn instead of one per turn).
//
// 3. Over-Delegation Guard: tracks total delegation count per session. When
//    delegation ratio exceeds threshold (delegations / total tool calls > 60%),
//    advises the agent to do more work itself rather than over-delegating.
//
// References:
//   - LangGraph: state graph for multi-agent orchestration (2024)
//   - CrewAI: role-based task assignment and sequential/process pipelines
//   - OpenAI Swarm: lightweight agent handoff patterns
//   - LLMCompiler (Kim et al., ICML 2024): parallel DAG of tool calls

// delegationToolNames identifies tools that create delegated work.
var delegationToolNames = map[string]bool{
	"spawn_agent":       true, // one-shot sub-agent
	"use_namedagent":    true, // named sub-agent template invocation
	"delegate":          true, // external CLI agent delegation
	"teammate_spawn":    true, // persistent swarm teammate
	"swarm_task_create": true, // task board assignment
	"a2a_remote":        true, // cross-project delegation
	"a2a_send_task":     true, // A2A protocol task send
}

// delegationResultTools identifies tools that consume delegation results.
var delegationResultTools = map[string]bool{
	"wait_agent":       true,
	"list_agents":      true,
	"task_output":      true,
	"teammate_results": true,
	"swarm_task_list":  true,
	"a2a_get_task":     true,
	"a2a_list_tasks":   true,
}

// delegationState tracks delegation orchestration metrics within a single run.
type delegationState struct {
	mu sync.Mutex

	// Orphaned delegation tracking
	activeDelegations map[string]*delegationEntry // key = toolID
	delegationSeq     int                         // monotonic counter for IDs
	orphansWarned     map[string]bool             // toolIDs already warned about
	orphanWarnCount   int                         // cap per run

	// Serial delegation tracking
	delegationHistory []string // tool names of recent delegations (FIFO, capped)
	serialWarnCount   int      // cap serial anti-pattern warnings per run

	// Over-delegation tracking
	totalDelegations int // count of delegation tool calls
	totalToolCalls   int // count of all tool calls this run
	overDelWarned    bool

	// Reset management
	lastResetIteration int
}

// delegationEntry tracks a single spawned delegation awaiting result consumption.
type delegationEntry struct {
	id           string // tool call ID or synthetic ID
	toolName     string // which delegation tool created it
	taskSummary  string // truncated task description
	iteration    int    // iteration when spawned
	lastChecked  int    // last iteration result was checked
	creationTime time.Time
}

const (
	// orphanDelegationThreshold: warn after this many unchecked iterations.
	orphanDelegationThreshold = 3
	// orphanDelegationMaxWarnings caps orphan warnings per run to avoid noise.
	orphanDelegationMaxWarnings = 3
	// serialDelegationThreshold: warn after this many sequential independent delegations.
	serialDelegationThreshold = 3
	// serialDelegationMaxWarnings caps serial warnings per run.
	serialDelegationMaxWarnings = 2
	// overDelegationRatio: if delegations exceed this fraction of all tool calls.
	overDelegationRatio = 0.60
	// overDelegationMinCalls: minimum total calls before ratio check activates.
	overDelegationMinCalls = 10
	// delegationHistoryCap limits the serial history window.
	delegationHistoryCap = 10
)

func newDelegationState() *delegationState {
	return &delegationState{
		activeDelegations: make(map[string]*delegationEntry),
		orphansWarned:     make(map[string]bool),
	}
}

// recordDelegationCall tracks a new delegation (spawn_agent, teammate_spawn, etc.).
func (d *delegationState) recordDelegationCall(toolID, toolName, taskSummary string, iteration int) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.totalDelegations++

	// Track for orphan detection — only track delegations that return an
	// agent_id or create an async task (not fire-and-forget like send_message).
	if delegationToolNames[toolName] {
		synID := toolID
		if synID == "" {
			d.delegationSeq++
			synID = string(rune('A'+d.delegationSeq%26)) + "-" + time.Now().Format("150405")
		}
		d.activeDelegations[synID] = &delegationEntry{
			id:           synID,
			toolName:     toolName,
			taskSummary:  truncateDelSummary(taskSummary),
			iteration:    iteration,
			lastChecked:  iteration,
			creationTime: time.Now(),
		}
	}

	// Track for serial delegation detection
	d.delegationHistory = append(d.delegationHistory, toolName)
	if len(d.delegationHistory) > delegationHistoryCap {
		d.delegationHistory = d.delegationHistory[1:]
	}
}

// recordResultCheck marks that a delegation result was consumed.
func (d *delegationState) recordResultCheck(toolName string, iteration int) {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Mark all active delegations as checked — we can't know which specific
	// delegation a result tool consumed, but any result check resets the
	// orphan timer for all tracked delegations.
	for _, del := range d.activeDelegations {
		del.lastChecked = iteration
	}
}

// recordToolCallCount increments the total tool call counter for ratio tracking.
func (d *delegationState) recordToolCallCount() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.totalToolCalls++
}

// resetForNewTurn clears per-turn state. Called at the start of each
// RunStreamWithContent invocation.
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

// maybeWarnOrphanedDelegations checks if any tracked delegations have gone
// unchecked for too long. Returns non-empty guidance if warning needed.
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
		elapsed := iteration - del.lastChecked
		if elapsed >= orphanDelegationThreshold {
			timeElapsed := time.Since(del.creationTime)
			if elapsed >= orphanDelegationThreshold || timeElapsed >= 30*time.Second {
				orphans = append(orphans, del.toolName+": "+del.taskSummary)
				d.orphansWarned[delID] = true
			}
		}
	}

	if len(orphans) == 0 {
		return ""
	}

	d.orphanWarnCount++
	debug.Log("agent", "Delegation orphan gate: %d unchecked delegations detected", len(orphans))

	var sb strings.Builder
	sb.WriteString("[Delegation Orchestration] You have delegated tasks but haven't checked their results in several iterations.\n\n")
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

// maybeWarnSerialDelegation detects consecutive delegation calls across
// iterations where tasks appear independent (different file scopes or topics).
// Returns guidance recommending batch parallelization.
func (d *delegationState) maybeWarnSerialDelegation() string {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.serialWarnCount >= serialDelegationMaxWarnings {
		return ""
	}

	// Count consecutive delegation calls at the end of history
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

// maybeWarnOverDelegation checks if the agent is over-delegating relative to
// doing work itself. Returns guidance if the delegation ratio is too high.
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

// removeDelegation removes a tracked delegation by tool ID (called when
// explicitly stopped or its result is consumed).
func (d *delegationState) removeDelegation(toolID string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.activeDelegations, toolID)
}

// truncateDelSummary truncates a task summary for display.
func truncateDelSummary(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 80 {
		return s[:77] + "..."
	}
	return s
}

// extractDelegationTaskSummary extracts a brief task description from delegation
// tool arguments. Different delegation tools use different argument field names.
func extractDelegationTaskSummary(toolName string, args json.RawMessage) string {
	if len(args) == 0 {
		return toolName
	}
	var m map[string]interface{}
	if err := json.Unmarshal(args, &m); err != nil {
		return toolName
	}
	// Common field names across delegation tools
	for _, field := range []string{"task", "message", "prompt", "description", "subject"} {
		if v, ok := m[field]; ok {
			if s, ok := v.(string); ok && s != "" {
				return truncateDelSummary(s)
			}
		}
	}
	return toolName
}

// itoaDel is a local int-to-string to avoid strconv import for simple cases.
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
