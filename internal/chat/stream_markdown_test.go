package chat

import (
	"strings"
	"testing"
)

func TestRenderStreamingMarkdownReusesStablePrefixBlocks(t *testing.T) {
	initial := "# Title\n\nFirst paragraph."
	rendered1, cache1 := renderStreamingMarkdown(initial, 80, nil)
	if len(cache1.blocks) != 2 {
		t.Fatalf("initial block count = %d, want 2", len(cache1.blocks))
	}
	firstBlock := cache1.rendered[0]

	updated := initial + "\n\nSecond paragraph."
	rendered2, cache2 := renderStreamingMarkdown(updated, 80, &cache1)
	if len(cache2.blocks) != 3 {
		t.Fatalf("updated block count = %d, want 3", len(cache2.blocks))
	}
	if cache2.rendered[0] != firstBlock {
		t.Fatal("expected unchanged prefix block render to be reused")
	}
	if !strings.Contains(rendered1, "First paragraph.") {
		t.Fatalf("initial render missing paragraph: %q", rendered1)
	}
	if !strings.Contains(rendered2, "Second paragraph.") {
		t.Fatalf("updated render missing appended paragraph: %q", rendered2)
	}
}

func TestRenderStreamingMarkdownClosesOpenFences(t *testing.T) {
	text := "```go\nfmt.Println(\"hello\")"
	rendered, cache := renderStreamingMarkdown(text, 80, nil)
	if len(cache.blocks) != 1 {
		t.Fatalf("block count = %d, want 1", len(cache.blocks))
	}
	if !strings.Contains(rendered, "fmt.Println") {
		t.Fatalf("expected rendered code block, got %q", rendered)
	}
}

func TestSplitMarkdownBlocksPreservesListMarkers(t *testing.T) {
	text := "Intro paragraph.\n\n- first item\n- second item\n"
	blocks := splitMarkdownBlocks(text)
	if len(blocks) != 2 {
		t.Fatalf("block count = %d, want 2", len(blocks))
	}
	if !strings.Contains(blocks[1], "- first item") || !strings.Contains(blocks[1], "- second item") {
		t.Fatalf("list block lost markdown markers: %q", blocks[1])
	}
}

func TestRenderStreamingMarkdownPreservesRenderedLists(t *testing.T) {
	text := "Intro paragraph.\n\n- first item\n- second item\n"
	rendered, _ := renderStreamingMarkdown(text, 80, nil)
	if !strings.Contains(rendered, "first item") || !strings.Contains(rendered, "second item") {
		t.Fatalf("expected rendered list items, got %q", rendered)
	}
}

func TestRenderStreamingMarkdownPreservesGrowingList(t *testing.T) {
	initial := "Intro paragraph.\n\n- first"
	rendered1, cache := renderStreamingMarkdown(initial, 80, nil)
	if !strings.Contains(rendered1, "first") {
		t.Fatalf("expected initial partial list content, got %q", rendered1)
	}

	updated := "Intro paragraph.\n\n- first item\n- second item\n"
	rendered2, _ := renderStreamingMarkdown(updated, 80, &cache)
	if !strings.Contains(rendered2, "first item") || !strings.Contains(rendered2, "second item") {
		t.Fatalf("expected grown list items, got %q", rendered2)
	}
}

func TestAssistantItemFinalRenderClearsStreamingCaches(t *testing.T) {
	styles := DefaultStyles()
	item := NewAssistantItem("a1", styles)
	item.SetText("# Title\n\nParagraph")
	item.SetReasoning("step 1")

	_ = item.Render(80)
	if len(item.textCache.blocks) == 0 {
		t.Fatal("expected streaming text cache to be populated")
	}
	if len(item.reasoningCache.blocks) == 0 {
		t.Fatal("expected reasoning cache to be populated")
	}

	item.SetReasoningFinished()
	item.SetFinished()
	rendered := item.Render(80)
	if !strings.Contains(rendered, "Title") || !strings.Contains(rendered, "Paragraph") {
		t.Fatalf("final render missing content: %q", rendered)
	}
	if len(item.textCache.blocks) != 0 {
		t.Fatal("expected text cache to be cleared after final render")
	}
	if len(item.reasoningCache.blocks) != 0 {
		t.Fatal("expected reasoning cache to be cleared after final render")
	}
}

func TestAssistantItemFinishedRenderPreservesLists(t *testing.T) {
	styles := DefaultStyles()
	item := NewAssistantItem("a2", styles)
	item.SetText("Intro paragraph.\n\n- first item\n- second item\n")
	item.SetFinished()

	rendered := item.Render(80)
	if !strings.Contains(rendered, "first item") || !strings.Contains(rendered, "second item") {
		t.Fatalf("finished render missing list items: %q", rendered)
	}
}
