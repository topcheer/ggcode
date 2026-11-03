package session

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func imgBlock(mime, data string) provider.ContentBlock {
	return provider.ContentBlock{Type: "image", ImageMIME: mime, ImageData: data}
}

func txtBlock(s string) provider.ContentBlock {
	return provider.ContentBlock{Type: "text", Text: s}
}

func TestIssue3351ImageOnlyUserMsgSurvivesDedupe(t *testing.T) {
	in := []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{imgBlock("image/png", "aGF4eQ==")}},
		{Role: "assistant", Content: []provider.ContentBlock{txtBlock("got it")}},
		{Role: "user", Content: []provider.ContentBlock{imgBlock("image/png", "ZGlmZmVyZW50")}},
	}
	out := dedupeUserMsgs(in)
	if len(out) != 3 {
		t.Fatalf("image-only turns dropped: got %d msgs, want 3", len(out))
	}
	if out[0].Content[0].Type != "image" || out[0].Content[0].ImageData != "aGF4eQ==" {
		t.Fatalf("first image-only turn mutated or missing: %+v", out[0])
	}
}

func TestIssue3351IdenticalImageReFireStillCollapses(t *testing.T) {
	img := imgBlock("image/jpeg", "cmVwZWF0LWRhdGE=")
	turn := provider.Message{Role: "user", Content: []provider.ContentBlock{img}}
	in := []provider.Message{turn, {Role: "assistant", Content: []provider.ContentBlock{txtBlock("a")}}, turn, {Role: "assistant", Content: []provider.ContentBlock{txtBlock("b")}}, turn}
	out := dedupeUserMsgs(in)
	userCount := 0
	for _, m := range out {
		if m.Role == "user" {
			userCount++
		}
	}
	if userCount != 1 {
		t.Fatalf("identical image re-fire not collapsed: got %d user msgs, want 1", userCount)
	}
}

func TestIssue3351SameTextDifferentImageKeptSeparately(t *testing.T) {
	a := provider.Message{Role: "user", Content: []provider.ContentBlock{txtBlock("check this"), imgBlock("image/png", "QUFB")}}
	b := provider.Message{Role: "user", Content: []provider.ContentBlock{txtBlock("check this"), imgBlock("image/png", "QkJC")}}
	in := []provider.Message{a, {Role: "assistant", Content: []provider.ContentBlock{txtBlock("x")}}, b}
	out := dedupeUserMsgs(in)
	userCount := 0
	for _, m := range out {
		if m.Role == "user" {
			userCount++
		}
	}
	if userCount != 2 {
		t.Fatalf("same-text different-image turns collapsed: got %d user msgs, want 2", userCount)
	}
}

func TestIssue3351TextDedupeSemanticsUnchanged(t *testing.T) {
	dup := provider.Message{Role: "user", Content: []provider.ContentBlock{txtBlock("same prompt")}}
	in := []provider.Message{dup, {Role: "assistant", Content: []provider.ContentBlock{txtBlock("r")}}, dup}
	out := dedupeUserMsgs(in)
	userCount := 0
	for _, m := range out {
		if m.Role == "user" {
			userCount++
		}
	}
	if userCount != 1 {
		t.Fatalf("text duplicate dedupe regressed: got %d user msgs, want 1", userCount)
	}

	tr := provider.Message{Role: "user", Content: []provider.ContentBlock{txtBlock("result"), {Type: "tool_result", ToolID: "t1", Output: "o"}}}
	out = dedupeUserMsgs([]provider.Message{tr, tr})
	if len(out) != 2 {
		t.Fatalf("tool_result carrier must never participate in dedupe: got %d, want 2", len(out))
	}

	empty := provider.Message{Role: "user"}
	out = dedupeUserMsgs([]provider.Message{empty, empty})
	if len(out) != 0 {
		t.Fatalf("genuinely empty user write should still be dropped: got %d, want 0", len(out))
	}
}

func TestIssue3351SignaturePrefixDisambiguates(t *testing.T) {
	a := provider.Message{Role: "user", Content: []provider.ContentBlock{imgBlock("image/png", strings.Repeat("A", 64))}}
	b := provider.Message{Role: "user", Content: []provider.ContentBlock{imgBlock("image/png", strings.Repeat("B", 64))}}
	out := dedupeUserMsgs([]provider.Message{a, {Role: "assistant", Content: []provider.ContentBlock{txtBlock("x")}}, b})
	userCount := 0
	for _, m := range out {
		if m.Role == "user" {
			userCount++
		}
	}
	if userCount != 2 {
		t.Fatalf("same-length distinct images merged: got %d user msgs, want 2", userCount)
	}
}
