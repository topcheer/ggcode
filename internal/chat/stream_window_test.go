package chat

import (
	"fmt"
	"strings"
	"testing"
)

// Window tests for renderStreamingMarkdown: once a streamed document
// exceeds the streaming window, rendering must (a) show a hidden-lines
// marker, (b) keep the visible tail, and (c) keep per-block cache reuse
// across chunks between sticky-trim advances.

func longDoc(paragraphs int) string {
	var sb strings.Builder
	for i := 0; i < paragraphs; i++ {
		fmt.Fprintf(&sb, "Paragraph %d with some **bold** text.\n\n- item a\n- item b\n\n", i)
	}
	return sb.String()
}

// docLinesOverBudget returns a document whose line count exceeds the
// pre-parse trim budget (maxStreamingTotalLines*3), forcing the window.
func docLinesOverBudget() string {
	// 5 lines per paragraph x N paragraphs; pick N so lines > 1800.
	return longDoc(2000) // ~12000 lines
}

func TestStreamingWindowShowsHiddenMarkerAndKeepsTail(t *testing.T) {
	doc := docLinesOverBudget()
	rendered, cache := renderStreamingMarkdown(doc, 80, nil)
	if !strings.Contains(rendered, "earlier lines hidden while streaming") {
		t.Fatal("windowed render must show the hidden-lines marker")
	}
	if !strings.Contains(rendered, "Paragraph 1999") {
		t.Fatal("windowed render must keep the document tail visible")
	}
	if strings.Contains(rendered, "Paragraph 0 with") {
		t.Fatal("content far outside the window must be dropped")
	}
	if cache.trimStart <= 0 {
		t.Fatalf("cache.trimStart = %d, want > 0 after trimming", cache.trimStart)
	}
}

func TestStreamingWindowWithinBudgetNoMarker(t *testing.T) {
	doc := longDoc(10) // ~60 lines, far below budget
	rendered, _ := renderStreamingMarkdown(doc, 80, nil)
	if strings.Contains(rendered, "hidden while streaming") {
		t.Fatal("short documents must not show the hidden-lines marker")
	}
	if !strings.Contains(rendered, "Paragraph 0") {
		t.Fatal("short documents must render from the start")
	}
}

func TestStreamingWindowStickyTrimKeepsCacheReuse(t *testing.T) {
	doc := docLinesOverBudget()
	_, cache1 := renderStreamingMarkdown(doc, 80, nil)
	trimAfterFirst := cache1.trimStart

	// Append a few chunks worth of text WITHOUT crossing the next sticky
	// advance threshold (budget/4 = 450 lines). The trimStart must stay
	// put and the cached prefix blocks must be reused.
	chunk := " more streamed tail text\n\n"
	for i := 0; i < 5; i++ {
		doc += chunk
	}
	_, cache2 := renderStreamingMarkdown(doc, 80, &cache1)
	if cache2.trimStart != trimAfterFirst {
		t.Fatalf("trimStart advanced prematurely: %d -> %d (sticky hysteresis violated)",
			trimAfterFirst, cache2.trimStart)
	}
	// The retained suffix blocks are byte-stable, so rendered entries must
	// carry over: compare the shared prefix of the rendered slices.
	shared := len(cache1.rendered)
	if len(cache2.rendered) < shared {
		shared = len(cache2.rendered)
	}
	if shared == 0 {
		t.Fatal("expected at least one shared cached block")
	}
	for i := 0; i < shared-1; i++ { // last block grows by design; skip it
		if cache1.rendered[i] != cache2.rendered[i] {
			t.Fatalf("block %d re-rendered despite sticky trim (cache reuse broken)", i)
		}
	}
}

func TestStreamingWindowFinishedRenderIsComplete(t *testing.T) {
	// The window only applies while streaming; a finished item renders the
	// full document. Guard the contract at the renderer level: non-nil
	// cache reuse is a streaming-only path, and renderStreamingMarkdown's
	// callers fall back to markdown.Render for finished items. Here we
	// verify the marker never appears once text fits the budget again via
	// SetFinished's full-render path (messages.go).
	doc := docLinesOverBudget()
	a := NewAssistantItem("a1", DefaultStyles())
	a.SetText(doc)
	streamed, _ := renderStreamingMarkdown(doc, 80, nil)
	if !strings.Contains(streamed, "hidden while streaming") {
		t.Fatal("sanity: doc should be windowed while streaming")
	}
	a.SetFinished()
	full := a.Render(80)
	if strings.Contains(full, "hidden while streaming") {
		t.Fatal("finished render must not contain the streaming window marker")
	}
	if !strings.Contains(full, "Paragraph 0 with") {
		t.Fatal("finished render must include the full document head")
	}
}
