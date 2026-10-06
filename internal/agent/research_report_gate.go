package agent

import "fmt"

// Research-report gate (r365; r343 deep-research third component). Deep-
// research production systems (OpenAI DR / Claude Research architecture
// writeups, DRA roadmap arXiv 2506.18096) close the multi-hop loop with a
// synthesis step: once enough retrieval has happened, findings must be
// consolidated - per-sub-question findings, evidence with sources,
// conflicts between sources, and remaining gaps - instead of streaming
// raw link summaries at the user.
//
// Like the r357 final-turn evidence gate, this is behavior-triggered and
// deterministic: it fires ONCE per run, only when the overseer flagged
// research mode AND the run accumulated >= 4 successful web_search /
// web_fetch calls (a genuine multi-hop pattern), and only at a stop turn
// (no tool calls = the agent is about to answer). Non-research runs are
// never touched; if the agent stops again the stop is allowed.

// researchReportGateMinCalls is the multi-hop threshold: at least this many
// successful web_search + web_fetch calls before synthesis is demanded.
const researchReportGateMinCalls = 4

// researchReportGate decides whether the agent's stop should be gated for a
// structured synthesis pass. Pure function over run state; returns the
// message to inject (empty string = allow the stop).
func researchReportGate(researchMode bool, retrievalCalls, minCalls int, gateAlreadyFired bool) string {
	if gateAlreadyFired || !researchMode || retrievalCalls < minCalls {
		return ""
	}
	return fmt.Sprintf(
		"[Research Synthesis Gate] You made %d successful search/fetch calls this run - "+
			"before answering, consolidate them into a structured synthesis: "+
			"(1) findings per sub-question, (2) evidence with source URLs for each finding, "+
			"(3) conflicts between sources (state which source says what), "+
			"(4) remaining gaps and what a further search would target - or say the evidence "+
			"is sufficient and answer. Do not paste raw result lists; synthesize. "+
			"(This gate fires once per run.)",
		retrievalCalls)
}
