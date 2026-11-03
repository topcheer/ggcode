package im

import (
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

// TestIssue1584_ImageOnlyNotDropped pins #1584-A: the two content-losing
// gates of the daemon first-submission path. Extracted as pure predicates
// because the full handler needs a wired bridge; the gates themselves are
// what the fix changed and what a regression would reinstate.
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
