package a2a

import (
	"strings"
	"testing"
)

// #2897: a fresh stateless agent must receive the full task history, not
// just the last follow-up message.
func TestBuildTranscriptPromptMultiTurn(t *testing.T) {
	msgs := []Message{
		{Role: "user", Parts: []Part{{Kind: "text", Text: "Fix the parser bug in cmd/foo.go"}}},
		{Role: "agent", Parts: []Part{{Kind: "text", Text: "确认要直接修改 cmd/foo.go 吗？"}}},
		{Role: "user", Parts: []Part{{Kind: "text", Text: "yes"}}},
	}
	got := buildTranscriptPrompt(msgs)
	if !strings.Contains(got, "Fix the parser bug in cmd/foo.go") {
		t.Errorf("original task missing from transcript: %q", got)
	}
	if !strings.Contains(got, "确认要直接修改 cmd/foo.go 吗") {
		t.Errorf("prior agent question missing: %q", got)
	}
	if !strings.Contains(got, "## Current request\nyes") {
		t.Errorf("last message must be restated as current request: %q", got)
	}
	if strings.Index(got, "Task context") > strings.Index(got, "Current request") {
		t.Errorf("context block must precede current request: %q", got)
	}
}

// Single-message histories degenerate to the bare request (old behavior).
func TestBuildTranscriptPromptSingle(t *testing.T) {
	msgs := []Message{
		{Role: "user", Parts: []Part{{Kind: "text", Text: "review internal/bar.go"}}},
	}
	got := buildTranscriptPrompt(msgs)
	// r406: single-message histories keep the payload verbatim (no
	// conversation scaffolding) but are still wrapped as untrusted peer
	// content - a lone peer message is exactly as untrusted as a transcript.
	if !strings.Contains(got, "review internal/bar.go") {
		t.Errorf("payload lost: %q", got)
	}
	if !strings.Contains(got, "<untrusted_peer_transcript>") {
		t.Errorf("single peer message must still be spotlighted, got %q", got)
	}
}

// Empty-tail messages must not leak empty current-request sections.
func TestBuildTranscriptPromptEmptyTail(t *testing.T) {
	msgs := []Message{
		{Role: "user", Parts: []Part{{Kind: "text", Text: "task"}}},
		{Role: "user", Parts: []Part{}}, // empty follow-up
	}
	got := buildTranscriptPrompt(msgs)
	if strings.Contains(got, "Current request") {
		t.Errorf("empty tail should drop the current-request header: %q", got)
	}
	if !strings.Contains(got, "task") {
		t.Errorf("context should survive: %q", got)
	}
}
