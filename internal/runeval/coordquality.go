// Coordination-quality evaluation: the multi-agent counterpart of the
// single-trajectory scorecard above. Research basis (sa-76 adjudication of
// MDPI Future Internet 18(6):326's six-dimension coordination-quality
// survey framework): /runreport previously scored only the monolithic
// trajectory; sessions that fan work out across subagents, swarm teammates,
// or A2A peers additionally need cross-agent dimensions - duplicated work,
// delegation round-trip overhead, chain integrity, parallel waste, and the
// coordination token tax. All metrics are deterministic and offline; the
// coordination section renders only when the event stream shows actual
// swarm/subagent activity, so single-agent sessions are unchanged.
package runeval

import (
	"fmt"
	"sort"
	"strings"

	"github.com/topcheer/ggcode/internal/provider"
)

// CoordEventKind classifies one entry of the coordination event stream.
type CoordEventKind string

const (
	// CoordTool is any tool invocation attributed to an agent.
	CoordTool CoordEventKind = "tool"
	// CoordDelegation opens a delegated unit of work (delegate, spawn_agent,
	// swarm task dispatch, A2A task submission, ...).
	CoordDelegation CoordEventKind = "delegation"
	// CoordDelegationResult closes a delegation with its outcome.
	CoordDelegationResult CoordEventKind = "delegation_result"
	// CoordMessage is an inter-agent message (teammate DM, lanchat, ...).
	CoordMessage CoordEventKind = "message"
	// CoordReadBack verifies a delegation's claimed result against ground
	// truth (internal/tool/delegation_readback.go's compareReadBack pattern).
	CoordReadBack CoordEventKind = "readback"
)

// CoordEvent is one agent-attributed observation in the coordination
// stream. runeval stays decoupled from internal/metrics and internal/swarm:
// the TUI (or a metrics exporter) maps MetricEvent.AgentID, swarm teammate
// FIFO entries, and delegation tool-call sequences onto this struct, exactly
// as UsageSample decouples usage accounting from the session package.
type CoordEvent struct {
	Seq        int            // stream order; ties broken by array order
	Kind       CoordEventKind // classification
	AgentID    string         // "main", subagent name, teammate nick, ...
	Tool       string         // tool name for CoordTool events
	InputKey   string         // canonical input identity (tool + input)
	ResultHash string         // truncated result identity, when observed
	StartMS    int64          // span start (relative ms); 0 = unknown
	EndMS      int64          // span end (relative ms); 0 = unknown
	Tokens     int            // tokens this event contributed to the stream
	Success    bool           // outcome flag for delegation results
}

// CoordinationQualityReport carries the five cross-agent dimensions.
type CoordinationQualityReport struct {
	// AgentCount is the number of distinct AgentIDs observed.
	AgentCount int

	// CrossAgentDuplicateGroups lists tool+input keys invoked by more than
	// one agent (the single-trajectory DuplicateGroup logic re-keyed across
	// MetricEvent.AgentID), worst first.
	CrossAgentDuplicateGroups []DuplicateGroup

	// MessageRoundTrips counts closed delegation→result round trips;
	// UnansweredDelegations counts delegations that never returned.
	MessageRoundTrips     int
	UnansweredDelegations int

	// DelegationRecoveryRate is delegation results / delegations.
	// ReadBackCoverage is read-back verifications / delegations.
	// DelegationChainIntegrity is their product (0-1): work must both come
	// back AND be verified before its claims are trusted.
	DelegationRecoveryRate   float64
	ReadBackCoverage         float64
	DelegationChainIntegrity float64

	// ParallelLayerWasteMS sums wall-clock overlap of same-key work executed
	// concurrently by different agents (waterfall span-overlap, cross-agent).
	ParallelLayerWasteMS int64

	// CoordinationTokenShare is coordination traffic (delegations, results,
	// messages, read-backs, coordination tools) as a fraction of all event
	// tokens (0-1).
	CoordinationTokens     int
	TotalTokens            int
	CoordinationTokenShare float64

	// Findings are human-readable, worst-first improvement hints.
	Findings []string
}

// coordinationTools are tools whose every invocation counts as coordination
// traffic for the token-share dimension.
var coordinationTools = map[string]bool{
	"delegate":          true,
	"spawn_agent":       true,
	"use_namedagent":    true,
	"teammate_spawn":    true,
	"swarm_task_create": true,
	"send_message":      true,
	"teammate_results":  true,
	"wait_agent":        true,
	"lanchat":           true,
	"a2a_send_task":     true,
	"a2a_get_task":      true,
	"a2a_remote":        true,
	"best_of_n":         true,
}

// delegationTools open a delegated unit of work when seen in a session log.
var delegationTools = map[string]bool{
	"delegate":          true,
	"spawn_agent":       true,
	"use_namedagent":    true,
	"teammate_spawn":    true,
	"swarm_task_create": true,
	"a2a_send_task":     true,
	"a2a_remote":        true,
	"best_of_n":         true,
}

// EvaluateCoordination computes the five cross-agent dimensions from the
// coordination event stream. It returns nil when the stream shows no
// swarm/subagent activity (no delegation, no inter-agent messages, and
// fewer than two distinct agents) so single-agent reports stay unchanged.
func EvaluateCoordination(events []CoordEvent) *CoordinationQualityReport {
	if len(events) == 0 {
		return nil
	}
	// Stable stream order: Seq first, array order as tiebreaker.
	ordered := append([]CoordEvent(nil), events...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].Seq < ordered[j].Seq
	})

	agents := map[string]bool{}
	delegations, results, answered, readbacks := 0, 0, 0, 0
	var coordTokens, totalTokens int

	// Cross-agent duplicate detection: same callKey across >=2 AgentIDs.
	type agentSet map[string]bool
	type crossRecord struct {
		tool     string
		input    string
		agents   agentSet
		count    int
		hashes   map[string]bool
		readOnly bool
	}
	records := map[callKey]*crossRecord{}
	var order []callKey
	// Parallel-layer waste: spans keyed by (tool,input) for overlap pairing.
	type span struct {
		agent      string
		start, end int64
	}
	spans := map[callKey][]span{}

	for _, ev := range ordered {
		if ev.AgentID != "" {
			agents[ev.AgentID] = true
		}
		totalTokens += ev.Tokens
		switch ev.Kind {
		case CoordTool:
			if coordinationTools[ev.Tool] {
				coordTokens += ev.Tokens
			}
			key := callKey{tool: ev.Tool, input: ev.InputKey}
			if ev.AgentID != "" {
				rec := records[key]
				if rec == nil {
					rec = &crossRecord{
						tool:     ev.Tool,
						input:    ev.InputKey,
						agents:   agentSet{},
						hashes:   map[string]bool{},
						readOnly: readOnlyTools[ev.Tool],
					}
					records[key] = rec
					order = append(order, key)
				}
				rec.agents[ev.AgentID] = true
				rec.count++
				if ev.ResultHash != "" {
					rec.hashes[ev.ResultHash] = true
				}
			}
			if ev.StartMS > 0 && ev.EndMS > ev.StartMS {
				spans[key] = append(spans[key], span{agent: ev.AgentID, start: ev.StartMS, end: ev.EndMS})
			}
		case CoordDelegation:
			delegations++
			coordTokens += ev.Tokens
		case CoordDelegationResult:
			results++
			if ev.Success {
				answered++
			}
			coordTokens += ev.Tokens
		case CoordMessage, CoordReadBack:
			if ev.Kind == CoordReadBack {
				readbacks++
			}
			coordTokens += ev.Tokens
		}
	}

	// Gate: only render for sessions with actual swarm/subagent activity.
	if delegations == 0 && coordTokens == 0 && len(agents) < 2 {
		return nil
	}

	q := &CoordinationQualityReport{
		AgentCount:            len(agents),
		MessageRoundTrips:     results,
		UnansweredDelegations: delegations - results,
		CoordinationTokens:    coordTokens,
		TotalTokens:           totalTokens,
	}

	// Cross-agent duplicate groups, worst first.
	for _, key := range order {
		rec := records[key]
		if len(rec.agents) < 2 {
			continue // same-agent duplicates stay in the single-agent section
		}
		q.CrossAgentDuplicateGroups = append(q.CrossAgentDuplicateGroups, DuplicateGroup{
			Tool:      rec.tool,
			Input:     truncateDisplay(rec.input),
			Count:     rec.count,
			Repeats:   rec.count - 1,
			Identical: len(rec.hashes) == 1 && rec.hashes != nil,
			ReadOnly:  rec.readOnly,
		})
	}
	sort.SliceStable(q.CrossAgentDuplicateGroups, func(i, j int) bool {
		return q.CrossAgentDuplicateGroups[i].Repeats > q.CrossAgentDuplicateGroups[j].Repeats
	})

	// Delegation chain integrity: recovery x read-back coverage.
	if delegations > 0 {
		q.DelegationRecoveryRate = float64(answered) / float64(delegations)
		q.ReadBackCoverage = float64(readbacks) / float64(delegations)
	}
	q.DelegationChainIntegrity = q.DelegationRecoveryRate * q.ReadBackCoverage

	// Parallel-layer waste: same-key spans from different agents that
	// overlap in wall-clock time (waterfall overlap logic, cross-agent).
	for _, sp := range spans {
		for i := 0; i < len(sp); i++ {
			for j := i + 1; j < len(sp); j++ {
				a, b := sp[i], sp[j]
				if a.agent == b.agent {
					continue
				}
				lo := max64(a.start, b.start)
				hi := min64(a.end, b.end)
				if hi > lo {
					q.ParallelLayerWasteMS += hi - lo
				}
			}
		}
	}

	if totalTokens > 0 {
		q.CoordinationTokenShare = float64(coordTokens) / float64(totalTokens)
	}
	q.Findings = coordinationFindings(q)
	return q
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

// coordinationFindings renders worst-first improvement hints.
func coordinationFindings(q *CoordinationQualityReport) []string {
	var f []string
	if n := len(q.CrossAgentDuplicateGroups); n > 0 {
		worst := q.CrossAgentDuplicateGroups[0]
		f = append(f, fmt.Sprintf(
			"%d tool call group%s executed by multiple agents on the same input (worst: %s x%d) - partition work by file/area before fanning out",
			n, plural(n), worst.Tool, worst.Count))
	}
	if q.UnansweredDelegations > 0 {
		f = append(f, fmt.Sprintf(
			"%d delegation%s never returned a result - reclaim or cancel orphaned subagent work before finishing",
			q.UnansweredDelegations, plural(q.UnansweredDelegations)))
	}
	if q.DelegationRecoveryRate > 0 && q.DelegationChainIntegrity < 0.5 {
		f = append(f, fmt.Sprintf(
			"delegation chain integrity %.0f%% (recovery %.0f%% x read-back %.0f%%) - verify delegated claims against ground truth before building on them",
			q.DelegationChainIntegrity*100, q.DelegationRecoveryRate*100, q.ReadBackCoverage*100))
	}
	if q.ParallelLayerWasteMS > 0 {
		f = append(f, fmt.Sprintf(
			"~%dms of same-key work executed concurrently by different agents - assign disjoint keys or serialize to avoid paying twice",
			q.ParallelLayerWasteMS))
	}
	if q.TotalTokens > 0 && q.CoordinationTokenShare > 0.4 {
		f = append(f, fmt.Sprintf(
			"coordination traffic is %.0f%% of stream tokens - over-orchestration; delegate coarser units with fewer check-ins",
			q.CoordinationTokenShare*100))
	}
	return f
}

// CoordEventsFromMessages derives a minimal coordination event stream from a
// session message log: delegation-tool calls and their results attributed to
// the main agent. Span/timing detail requires a richer metrics feed; those
// dimensions simply stay zero for log-derived streams.
func CoordEventsFromMessages(msgs []provider.Message) []CoordEvent {
	var events []CoordEvent
	delegationToolIDs := map[string]string{} // tool_use ID -> tool name
	seq := 0
	for i := range msgs {
		for _, b := range msgs[i].Content {
			switch b.Type {
			case "tool_use":
				if delegationTools[b.ToolName] {
					delegationToolIDs[b.ToolID] = b.ToolName
					events = append(events, CoordEvent{
						Seq: seq, Kind: CoordDelegation, AgentID: "main",
						Tool: b.ToolName, InputKey: canonicalJSON(b.Input),
						Tokens: len(canonicalJSON(b.Input)) / bytesPerToken,
					})
					seq++
				} else if coordinationTools[b.ToolName] {
					events = append(events, CoordEvent{
						Seq: seq, Kind: CoordMessage, AgentID: "main",
						Tool: b.ToolName, InputKey: canonicalJSON(b.Input),
						Tokens: len(canonicalJSON(b.Input)) / bytesPerToken,
					})
					seq++
				}
			case "tool_result":
				if name, ok := delegationToolIDs[b.ToolID]; ok {
					events = append(events, CoordEvent{
						Seq: seq, Kind: CoordDelegationResult, AgentID: "main",
						Tool: name, Success: !b.IsError, ResultHash: truncateHash(b.Output),
						Tokens: len(b.Output) / bytesPerToken,
					})
					seq++
				}
			}
		}
	}
	return events
}

// EvaluateWithCoordination is Evaluate plus the cross-agent section: the
// coordination report is attached only when the event stream shows actual
// swarm/subagent activity.
func EvaluateWithCoordination(msgs []provider.Message, usage []UsageSample, events []CoordEvent) Report {
	r := Evaluate(msgs, usage)
	r.Coordination = EvaluateCoordination(events)
	return r
}

// renderCoordination formats the cross-agent section for /runreport.
func renderCoordination(b *strings.Builder, q *CoordinationQualityReport) {
	fmt.Fprintf(b, "Coordination: %d agents · %d cross-agent dup group%s", q.AgentCount,
		len(q.CrossAgentDuplicateGroups), plural(len(q.CrossAgentDuplicateGroups)))
	fmt.Fprintf(b, " · %d round trip%s (%d unanswered)",
		q.MessageRoundTrips, plural(q.MessageRoundTrips), q.UnansweredDelegations)
	fmt.Fprintf(b, " · chain integrity %.0f%%", q.DelegationChainIntegrity*100)
	if q.ParallelLayerWasteMS > 0 {
		fmt.Fprintf(b, " · ~%dms parallel waste", q.ParallelLayerWasteMS)
	}
	if q.TotalTokens > 0 {
		fmt.Fprintf(b, " · %.0f%% coord tokens", q.CoordinationTokenShare*100)
	}
	b.WriteByte('\n')
	for _, line := range q.Findings {
		fmt.Fprintf(b, "  - %s\n", line)
	}
}
