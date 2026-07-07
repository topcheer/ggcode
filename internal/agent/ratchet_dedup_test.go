//go:build goolm

package agent

import (
	"testing"
	"time"
)

func TestRuleSimilarity_IdenticalRules(t *testing.T) {
	a := Rule{Rule: "Run gofmt before commit", ToolPattern: "git commit"}
	b := Rule{Rule: "Run gofmt before commit", ToolPattern: "git commit"}
	if sim := ruleSimilarity(a, b); sim < 0.9 {
		t.Errorf("identical rules should have similarity >= 0.9, got %.2f", sim)
	}
}

func TestRuleSimilarity_NearDuplicate(t *testing.T) {
	a := Rule{Rule: "Run gofmt and goimports on all modified Go files before staging them for commit", ToolPattern: "git commit"}
	b := Rule{Rule: "Run gofmt -w on all modified Go files before staging for commit", ToolPattern: "git commit"}
	if sim := ruleSimilarity(a, b); sim < 0.55 {
		t.Errorf("near-duplicate rules with same tool should have similarity >= 0.55, got %.2f", sim)
	}
}

func TestRuleSimilarity_DifferentRules(t *testing.T) {
	a := Rule{Rule: "Never copy structs containing sync primitives by value", ToolPattern: "go vet"}
	b := Rule{Rule: "Run gofmt -w on all modified Go files before staging for commit", ToolPattern: "git commit"}
	if sim := ruleSimilarity(a, b); sim >= 0.55 {
		t.Errorf("unrelated rules should have low similarity, got %.2f", sim)
	}
}

func TestRuleSimilarity_ToolPatternBoost(t *testing.T) {
	a := Rule{Rule: "verify file exists before reading", ToolPattern: "read_file"}
	b := Rule{Rule: "check path is valid before reading", ToolPattern: "read_file"}
	sim := ruleSimilarity(a, b)
	if sim < 0.3 {
		t.Errorf("same-tool-pattern rules should get boost, got %.2f", sim)
	}
}

func TestRuleSimilarity_EmptyRules(t *testing.T) {
	a := Rule{}
	b := Rule{Rule: "test", ToolPattern: "x"}
	if sim := ruleSimilarity(a, b); sim != 0 {
		t.Errorf("empty rule should have 0 similarity, got %.2f", sim)
	}
}

func TestMaxTime(t *testing.T) {
	a := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	b := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	if maxTime(a, b) != b {
		t.Error("maxTime should return later time")
	}
	if maxTime(b, a) != b {
		t.Error("maxTime should be symmetric")
	}
}
