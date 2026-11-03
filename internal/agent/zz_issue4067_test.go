package agent

import "testing"

func TestIssue4067PrefixGateNoLongerHard(t *testing.T) {
	p := ResearchSubQuery{Perspective: "technical", Query: "rag systems technical details and mechanisms"}

	if subQueryCovered("rag indexing internals retrieval benchmarks", p) {
		t.Fatal("no prefix, <2 overlaps, no perspective keyword: must stay a gap")
	}

	if !subQueryCovered("implementation study of rag systems index behavior", p) {
		t.Fatal(">=2 content overlaps + perspective keyword must cover despite reworded goal prefix (#4067)")
	}

	cjk := ResearchSubQuery{Perspective: "technical", Query: "检索增强生成系统的技术细节与机制 technical details and mechanisms"}
	if !subQueryCovered("索引分片的架构与实现分析", cjk) {
		t.Fatal("CJK query + perspective keyword (架构/实现) must cover (#4067)")
	}
	if subQueryCovered("完全无关的定价调研", cjk) {
		t.Fatal("perspective keyword gate must still fire for CJK queries with no matching finding (#3999 granularity)")
	}
}
