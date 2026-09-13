package session

// #1628 case A: the ID-less content fingerprint used to dedupe GLOBALLY,
// swallowing legitimate repeats (user sends "continue" twice, identical
// build outputs) in legacy ID-less sessions. Adjacent-only now: the
// double-append race still collapses, distant repeats survive.

import (
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestIssue1628DistantRepeatSurvives(t *testing.T) {
	records := []jsonlRecord{
		{Message: &provider.Message{Role: "user", Content: []provider.ContentBlock{provider.TextBlock("continue")}}},
		{Message: &provider.Message{Role: "assistant", Content: []provider.ContentBlock{provider.TextBlock("ok")}}},
		{Message: &provider.Message{Role: "user", Content: []provider.ContentBlock{provider.TextBlock("continue")}}},
	}
	out := dedupMessageRecords(records)
	if len(out) != 3 {
		t.Fatalf("distant repeat must survive, got %d", len(out))
	}
}

func TestIssue1628AdjacentDuplicateCollapses(t *testing.T) {
	records := []jsonlRecord{
		{Message: &provider.Message{Role: "user", Content: []provider.ContentBlock{provider.TextBlock("continue")}}},
		{Message: &provider.Message{Role: "user", Content: []provider.ContentBlock{provider.TextBlock("continue")}}},
	}
	out := dedupMessageRecords(records)
	if len(out) != 1 {
		t.Fatalf("adjacent double-append race must collapse, got %d", len(out))
	}
}

func TestIssue1628IDStillGlobal(t *testing.T) {
	records := []jsonlRecord{
		{Message: &provider.Message{ID: "m1", Role: "user", Content: []provider.ContentBlock{provider.TextBlock("a")}}},
		{Message: &provider.Message{Role: "assistant", Content: []provider.ContentBlock{provider.TextBlock("x")}}},
		{Message: &provider.Message{ID: "m1", Role: "user", Content: []provider.ContentBlock{provider.TextBlock("a")}}},
	}
	out := dedupMessageRecords(records)
	if len(out) != 2 {
		t.Fatalf("replayed ID must collapse globally, got %d", len(out))
	}
}
