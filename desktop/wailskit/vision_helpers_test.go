package wailskit

import (
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestContentHasImageBlocks(t *testing.T) {
	if contentHasImageBlocks([]provider.ContentBlock{{Type: "text", Text: "hi"}}) {
		t.Error("text-only content must not report images")
	}
	if !contentHasImageBlocks([]provider.ContentBlock{
		{Type: "text", Text: "hi"},
		{Type: "image", ImageMIME: "image/png"},
	}) {
		t.Error("mixed content must report images")
	}
}

func TestStripImageBlocks(t *testing.T) {
	in := []provider.ContentBlock{
		{Type: "text", Text: "hello"},
		{Type: "image", ImageMIME: "image/png"},
		{Type: "text", Text: "world"},
	}
	out := stripImageBlocks(in)
	if len(out) != 2 || out[0].Text != "hello" || out[1].Text != "world" {
		t.Fatalf("stripImageBlocks dropped wrong blocks: %+v", out)
	}
}
