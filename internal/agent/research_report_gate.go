package agent

import "fmt"

const researchReportGateMinCalls = 4

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
