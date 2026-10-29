package agent

import "testing"

// #4067 residual: the goal-prefix probe was a HARD gate before the
// perspective-keyword check, so (a) a reworded goal failed it for every
// perspective at once, and (b) CJK goals (no spaces; probe = whole sentence
// + template tail) almost never passed. The gate now passes on prefix hit,
// >=2 content-word overlaps, or CJK-bearing queries; #3999's perspective
// granularity is preserved (perspective names: technical/comparison/
// practical/risk - see researchPerspectives).
func TestIssue4067PrefixGateNoLongerHard(t *testing.T) {
	p := ResearchSubQuery{Perspective: "technical", Query: "rag systems technical details and mechanisms"}
	// No prefix, <2 overlaps, no perspective keyword -> gap.
	if subQueryCovered("rag indexing internals retrieval benchmarks", p) {
		t.Fatal("no prefix, <2 overlaps, no perspective keyword: must stay a gap")
	}
	// Overlap path: reworded goal (no shared prefix), >=2 content words +
	// a perspective keyword -> covered (old hard gate would have gapped it).
	if !subQueryCovered("implementation study of rag systems index behavior", p) {
		t.Fatal(">=2 content overlaps + perspective keyword must cover despite reworded goal prefix (#4067)")
	}
	// CJK path: space-based probes are meaningless; coverage falls to the
	// perspective-keyword gate alone.
	cjk := ResearchSubQuery{Perspective: "technical", Query: "检索增强生成系统的技术细节与机制 technical details and mechanisms"}
	if !subQueryCovered("索引分片的架构与实现分析", cjk) {
		t.Fatal("CJK query + perspective keyword (架构/实现) must cover (#4067)")
	}
	if subQueryCovered("完全无关的定价调研", cjk) {
		t.Fatal("perspective keyword gate must still fire for CJK queries with no matching finding (#3999 granularity)")
	}
}
