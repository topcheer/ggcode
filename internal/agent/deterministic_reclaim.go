package agent

import (
	"github.com/topcheer/ggcode/internal/debug"
)

// passiveReclaimer is the subset of *context.Manager that reclaims context
// tokens deterministically - no LLM call required. The four transforms
// (CompactSupersededReads, ClearOldToolResults, ClearOldToolUseInputs,
// CompactOldReasoningBlocks) replace stale block payloads with short
// tool-aware placeholders instead of removing blocks, so tool_use/tool_result
// pairing stays intact and provider replay remains protocol-valid.
type passiveReclaimer interface {
	CompactOldReasoningBlocks() int
	CompactSupersededReads() int
	ClearOldToolResults(keepN int) int
	ClearOldToolUseInputs() int
}

// reclaimKeepRecentToolResults is how many of the most recent tool results
// stay intact when the deterministic reclaim pass clears older ones. The
// newest results are the ones the model is most likely to still need
// verbatim; older ones keep only their tool-aware placeholder summary.
const reclaimKeepRecentToolResults = 3

// deterministicReclaim runs the four lossless context-recovery transforms in
// quality order - targeted superseded-read compaction first, then blanket
// clearing of older tool results, then truncation of the tool_use inputs
// paired with cleared results, then stale reasoning blocks - and returns the
// number of tokens actually freed.
//
// The pass is "free": it costs no LLM call and preserves strictly more
// information than the alternatives that run at the same trigger points
// (LLM summarization costs a round trip and rewrites history; oldest-group
// truncation destroys whole messages). It is only invoked at context-pressure
// points (auto-compact threshold crossing, PTL recovery, pre-send guard), so
// provider-side prompt caches are never broken on ordinary turns.
//
// Safety: the pass is skipped while a precompact is running - live messages
// must not be mutated concurrently with the async summarizer reading them.
func (a *Agent) deterministicReclaim(reason string) int {
	a.mu.RLock()
	running := a.precompact != nil
	a.mu.RUnlock()
	if running {
		debug.Log("agent", "%s: deterministic reclaim SKIP (precompact running)", reason)
		return 0
	}
	cm, ok := a.contextManager.(passiveReclaimer)
	if !ok {
		return 0
	}
	before := a.contextManager.TokenCount()
	if before == 0 {
		return 0
	}
	reads := cm.CompactSupersededReads()
	results := cm.ClearOldToolResults(reclaimKeepRecentToolResults)
	inputs := cm.ClearOldToolUseInputs()
	reasoning := cm.CompactOldReasoningBlocks()
	freed := before - a.contextManager.TokenCount()
	if freed > 0 {
		debug.Log("agent", "%s: deterministic reclaim freed %d tokens (superseded_reads=%d tool_results=%d tool_inputs=%d reasoning=%d) %d→%d",
			reason, freed, reads, results, inputs, reasoning, before, a.contextManager.TokenCount())
		a.maybeSaveCheckpoint()
	}
	return freed
}

// maybeReclaimOverThreshold runs the deterministic reclaim pass and reports
// whether it brought the context back under the auto-compact threshold, which
// lets the caller skip scheduling an LLM summarization entirely.
func (a *Agent) maybeReclaimOverThreshold(reason string) (reclaimed int, underThreshold bool) {
	reclaimed = a.deterministicReclaim(reason)
	if reclaimed <= 0 {
		return 0, false
	}
	return reclaimed, a.contextManager.TokenCount() < a.contextManager.AutoCompactThreshold()
}
