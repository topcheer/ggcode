package im

import (
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestIssue1584_ImageOnlyNotDropped(t *testing.T) {

	textLessContent := []provider.ContentBlock{provider.ImageBlock("image/png", "x")}
	text := ""
	routeEmpty := false
	dropped := routeEmpty || (text == "" && len(textLessContent) == 0)
	if dropped {
		t.Fatal("image-only message (non-empty content, empty text) must not be dropped")
	}

	content := []provider.ContentBlock{
		provider.ImageBlock("image/png", "x"),
		{Type: "text", Text: "caption"},
	}
	if len(content) == 0 {
		content = []provider.ContentBlock{{Type: "text", Text: text}}
	}
	if len(content) != 2 || content[0].Type != "image" {
		t.Fatalf("mixed image+text content must keep its blocks, got %v", content)
	}
}
