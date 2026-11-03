package agent

import (
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestEnsureMessagesSendableStripsInlineUserImages(t *testing.T) {
	a := &Agent{supportsVision: false}
	msgs := []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{
			provider.TextBlock("look at this"),
			provider.ImageBlock("image/png", "aW1n"),
		}},
	}
	out := a.ensureMessagesSendable(msgs)
	if len(out) != 1 {
		t.Fatalf("expected 1 message, got %d", len(out))
	}
	for _, b := range out[0].Content {
		if b.Type == "image" {
			t.Fatal("image block survived the vision gate")
		}
	}
	if len(out[0].Content) != 1 || out[0].Content[0].Text != "look at this" {
		t.Fatalf("text block must survive: %#v", out[0].Content)
	}

	v := &Agent{supportsVision: true}
	outV := v.ensureMessagesSendable(msgs)
	if len(outV) != 1 || len(outV[0].Content) != 2 {
		t.Fatalf("vision agent must keep image block: %#v", outV)
	}

	pure := []provider.Message{{Role: "user", Content: []provider.ContentBlock{
		provider.ImageBlock("image/jpeg", "anVwbg=="),
	}}}
	outPure := a.ensureMessagesSendable(pure)
	if len(outPure) != 1 || len(outPure[0].Content) == 0 {
		t.Fatalf("pure-image message must keep a placeholder block: %#v", outPure)
	}
	for _, b := range outPure[0].Content {
		if b.Type != "text" {
			t.Fatalf("expected text placeholder, got %q", b.Type)
		}
	}
}
