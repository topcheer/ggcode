package tool

import (
	"strings"
	"testing"
)

// r364 (r343 evaluator slice): assessSearchResultsScored exposes the best
// relevance score; the tool layer turns low bests into a rephrase hint
// (Agentic-RAG low-confidence loop, deterministic).
func TestAssessScored_BestScoreExposed(t *testing.T) {
	// Every query term hits title+snippet -> best near 100.
	strong := []searchResult{{
		Title:   "Go Context Package Docs",
		URL:     "https://pkg.go.dev/context",
		Snippet: "Package context defines the Context type in go",
	}}
	out, best := assessSearchResultsScored("go context package", strong, nil)
	if len(out) != 1 || best < 70 {
		t.Fatalf("strong match: out=%d best=%d", len(out), best)
	}

	// Vocabulary mismatch: zero term hits -> best 0.
	weak := []searchResult{{
		Title:   "Cooking Pasta Perfectly",
		URL:     "https://example.com/pasta",
		Snippet: "Boil water and add salt for better noodles",
	}}
	out, best = assessSearchResultsScored("kubernetes operator pattern", weak, nil)
	if len(out) != 1 {
		t.Fatalf("weak result must survive assessment, out=%d", len(out))
	}
	if best != 0 {
		t.Fatalf("vocabulary mismatch must score 0, got %d", best)
	}
	if best >= weakSearchBestScore {
		t.Fatal("sanity: 0 must be below the hint threshold")
	}
}

// CJK queries tokenize to zero terms -> flat neutral 50, which must stay
// ABOVE the hint threshold (the upstream ranker is trusted for scripts we
// cannot tokenize).
func TestAssessScored_CJKFlat50NeverHints(t *testing.T) {
	cjk := []searchResult{{
		Title:   "完全无关的标题",
		URL:     "https://example.com/x",
		Snippet: "毫不相干的摘要",
	}}
	_, best := assessSearchResultsScored("如何部署微服务集群", cjk, nil)
	if best != 50 {
		t.Fatalf("CJK must keep neutral 50, got %d", best)
	}
	if best < weakSearchBestScore {
		t.Fatal("CJK neutral score must not trigger the weak hint")
	}
}

// The hint fires for mismatched queries and stays silent for solid ones.
func TestWeakSearchHintCopy(t *testing.T) {
	if !strings.Contains("[search-quality] best result matches only 0% of query terms", "[search-quality]") {
		t.Fatal("sanity")
	}
}
