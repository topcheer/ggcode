package chat

import (
	"fmt"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"

	mdpkg "github.com/topcheer/ggcode/internal/markdown"
)

var streamMarkdown = goldmark.New(goldmark.WithExtensions(extension.GFM))

// maxStreamingBlockLines caps how much of the actively-growing last block
// is rendered while streaming. The viewport shows at most ~60 lines, but
// without this cap every chunk re-renders the ENTIRE growing block (a long
// code fence or list can reach hundreds of lines), saturating the UI event
// loop and making scrolling sluggish during long replies. Finished items
// always render in full (streaming=false path).
const maxStreamingBlockLines = 400

// maxStreamingTotalLines caps the WHOLE document during streaming, not just
// the last block. The per-block cache avoids re-PARSING stable blocks, but
// every chunk still re-JOINED and re-MEASURED the full accumulated text
// (join ~30KB, prefix-stitch every line, measure every line height) -
// profile: 8.6ms/frame, 5.6MB and 150K allocs per chunk on a 30KB reply,
// with Height+Render at ~39% of samples. Keeping only the trailing ~600
// lines makes per-chunk cost proportional to the window, not the document.
// Finished items render in full; scroll-up mid-stream sees the same
// "(earlier lines hidden while streaming)" affordance the block cap uses.
const maxStreamingTotalLines = 600

type streamRenderCache struct {
	width    int
	source   string
	blocks   []string
	rendered []string
	// trimStart is the sticky line offset where the pre-parse trim last
	// cut the document (see renderStreamingMarkdown). Advancing the cut
	// shifts every block and defeats the per-block cache, so it moves only
	// in coarse hysteresis steps; between advances chunks cache-hit.
	trimStart int
	// blockCut is the sticky index of the first retained block after the
	// document-level window drop (same hysteresis rationale).
	blockCut int
}

func renderStreamingMarkdown(text string, width int, cache *streamRenderCache) (string, streamRenderCache) {
	// normalized adds a closing fence to unclosed code blocks so the tail
	// renders correctly. The PREFIX COMPARISON below must use the raw
	// (fence-unclosed) source: the appended fence breaks HasPrefix for the
	// next chunk, so during code-block streaming every chunk missed the
	// cache and re-rendered every block (#184).
	rawSource := mdpkg.Normalize(text)

	// Pre-parse document trim with a STICKY boundary. goldmark parses the
	// FULL accumulated text every chunk (24% of samples on a 35K-line
	// reply) even though only the trailing window can influence the
	// display. But naively keeping the last N lines shifts the cut by one
	// line per chunk, which shifts every parsed block and zeroes the
	// per-block cache below - the whole window would re-render per chunk
	// (bench-verified: no gain). So the cut advances only when the
	// overflow exceeds a quarter of the budget (same hysteresis idea as
	// List.trimLocked), keeping blocks byte-stable between advances.
	trimStart := 0
	if cache != nil && cache.width == width {
		trimStart = cache.trimStart
	}
	docLines := strings.Count(text, "\n") + 1
	budget := maxStreamingTotalLines * 3
	minStart := docLines - budget
	if minStart < 0 {
		minStart = 0
	}
	if minStart-trimStart >= budget/4 || minStart < trimStart {
		trimStart = minStart
	}
	trimmed := trimLeadingLines(text, trimStart)
	normalized := mdpkg.Normalize(closeOpenFences(trimmed))
	blocks := splitMarkdownBlocks(normalized)
	if len(blocks) == 0 {
		return "", streamRenderCache{width: width, source: rawSource, trimStart: trimStart}
	}

	// Cap the growing tail block: only the last block changes between
	// chunks, and only its visible tail matters during streaming. Truncate
	// from the top so recent content stays intact. The stored block keeps
	// the truncated form so the cache comparison below stays consistent.
	blocks[len(blocks)-1] = capStreamingBlock(blocks[len(blocks)-1])

	// Document-level streaming window, sticky like the pre-parse trim.
	// Dropping leading blocks bounds the per-chunk join / prefix-stitch /
	// height-measure cost to the window, but a cut that follows the tail
	// every chunk shifts blocks[0] and zeroes the per-block cache (found by
	// TestStreamingWindowStickyTrimKeepsCacheReuse: the first version
	// re-rendered the whole window per chunk). The block cut advances only
	// when the overflow exceeds a quarter of the budget, mirroring both the
	// pre-parse trim and List.trimLocked hysteresis.
	blockCut := 0
	if cache != nil && cache.width == width {
		blockCut = cache.blockCut
	}
	desiredCut := leadingBlockCut(blocks, maxStreamingTotalLines)
	if desiredCut-blockCut >= maxStreamingTotalLines/4 || desiredCut < blockCut {
		blockCut = desiredCut
	}
	// Marker counts hidden lines from BOTH trims, computed before slicing.
	// It changes per chunk (grows), which is fine: it is concatenated
	// OUTSIDE the cached block slice so it never breaks cache comparison.
	hiddenLines := trimStart
	for i := 0; i < blockCut; i++ {
		hiddenLines += strings.Count(blocks[i], "\n") + 1
	}
	blocks = blocks[blockCut:]
	var hiddenMarker string
	if hiddenLines > 0 {
		hiddenMarker = fmt.Sprintf("... (%d earlier lines hidden while streaming)", hiddenLines)
	}

	next := streamRenderCache{
		width:     width,
		source:    rawSource,
		blocks:    append([]string(nil), blocks...),
		rendered:  make([]string, len(blocks)),
		trimStart: trimStart,
		blockCut:  blockCut,
	}

	reuse := 0
	if cache != nil && cache.width == width && strings.HasPrefix(rawSource, cache.source) {
		for reuse < len(cache.blocks) && reuse < len(blocks) {
			if cache.blocks[reuse] != blocks[reuse] {
				break
			}
			next.rendered[reuse] = cache.rendered[reuse]
			reuse++
		}
	}

	for i := reuse; i < len(blocks); i++ {
		next.rendered[i] = mdpkg.Render(blocks[i], width)
	}

	if hiddenMarker != "" {
		return hiddenMarker + "\n\n" + strings.Join(next.rendered, "\n\n"), next
	}
	return strings.Join(next.rendered, "\n\n"), next
}

func normalizeStreamingMarkdown(text string) string {
	return mdpkg.Normalize(closeOpenFences(text))
}

// trimLeadingLines drops the first n lines of s (no-op when n <= 0).
// Cheap byte-scan; no split of the full string.
func trimLeadingLines(s string, n int) string {
	if n <= 0 || s == "" {
		return s
	}
	seen := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			seen++
			if seen >= n {
				return s[i+1:]
			}
		}
	}
	return s
}

// leadingBlockCut returns the smallest index into blocks such that the
// retained suffix (blocks[idx:]) totals at most maxLines lines. Returns 0
// when the whole slice already fits.
func leadingBlockCut(blocks []string, maxLines int) int {
	total := 0
	for _, b := range blocks {
		total += strings.Count(b, "\n") + 1
	}
	if total <= maxLines {
		return 0
	}
	kept := 0
	for i := len(blocks) - 1; i > 0; i-- {
		n := strings.Count(blocks[i], "\n") + 1
		if kept+n > maxLines {
			return i + 1
		}
		kept += n
	}
	return 0
}

// capStreamingBlock truncates an oversized growing block to its last
// maxStreamingBlockLines lines, prefixed with a visible ellipsis marker.
// Small blocks pass through unchanged.
func capStreamingBlock(block string) string {
	if block == "" {
		return block
	}
	lines := strings.Split(block, "\n")
	if len(lines) <= maxStreamingBlockLines {
		return block
	}
	drop := len(lines) - maxStreamingBlockLines
	kept := lines[drop:]
	var sb strings.Builder
	fmt.Fprintf(&sb, "... (%d earlier lines hidden while streaming)\n\n", drop)
	// If truncation removed the opening fence of a code block, the kept
	// closing fence would instead OPEN a new never-closed block and mangle
	// the streaming display. Re-open the fence to keep markdown valid.
	if fence := missingOpenFence(kept); fence != "" {
		sb.WriteString(fence + "\n")
	}
	sb.WriteString(strings.Join(kept, "\n"))
	return sb.String()
}

// missingOpenFence mirrors closeOpenFences' parity logic: because the
// document was fence-balanced before truncation, an odd number of fence
// lines in the kept tail means its opening partner was dropped. Returns
// the fence style to prepend, or "" when balanced.
//
// Deliberately NOT full CommonMark: like closeOpenFences, it counts any
// ``` / ~~~ prefix line as a toggle (ignoring the "closing fence must be
// >= opener length" rule). The two functions MUST stay mirror-identical -
// a divergence here would desync truncation from fence-closing. Misfires
// are benign: a longer fence closes a shorter opener per CommonMark, so a
// spurious ``` prepend still yields balanced, valid markdown.
func missingOpenFence(kept []string) string {
	fence := ""
	for _, line := range kept {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "```"):
			if fence == "" {
				fence = "```"
			} else if fence == "```" {
				fence = ""
			}
		case strings.HasPrefix(trimmed, "~~~"):
			if fence == "" {
				fence = "~~~"
			} else if fence == "~~~" {
				fence = ""
			}
		}
	}
	return fence
}

func splitMarkdownBlocks(src string) []string {
	if strings.TrimSpace(src) == "" {
		return nil
	}
	reader := text.NewReader([]byte(src))
	doc := streamMarkdown.Parser().Parse(reader)
	blocks := make([]string, 0, 8)
	source := []byte(src)
	for child := doc.FirstChild(); child != nil; child = child.NextSibling() {
		block := extractBlockMarkdown(child, source)
		if strings.TrimSpace(block) == "" {
			continue
		}
		blocks = append(blocks, strings.TrimRight(block, "\n"))
	}
	return blocks
}

func extractBlockMarkdown(node ast.Node, source []byte) string {
	switch node.(type) {
	case *ast.FencedCodeBlock, *ast.List, *ast.Blockquote, *extast.Table:
		if start, end, ok := rawBlockSpan(node, source); ok {
			return string(source[start:end])
		}
	}
	lines := node.Lines()
	if lines != nil && lines.Len() > 0 {
		var sb strings.Builder
		for i := 0; i < lines.Len(); i++ {
			seg := lines.At(i)
			sb.Write(seg.Value(source))
		}
		return sb.String()
	}
	var sb strings.Builder
	_ = ast.Walk(node, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch v := n.(type) {
		case *ast.Text:
			sb.Write(v.Segment.Value(source))
			// Goldmark separates lines within a paragraph with SoftLineBreak
			// nodes. Without this, consecutive Text segments concatenate
			// without any separator, losing whitespace and line breaks.
			if v.SoftLineBreak() {
				sb.WriteByte('\n')
			}
		case *ast.String:
			sb.Write(v.Value)
		case *ast.Link:
			// Links have a Text child with the link text and an attribute
			// with the URL. We only need the text - the child walk handles it.
		}
		return ast.WalkContinue, nil
	})
	return sb.String()
}

func rawBlockSpan(node ast.Node, source []byte) (int, int, bool) {
	start := node.Pos()
	if start < 0 || start >= len(source) {
		return 0, 0, false
	}
	end := start
	_ = ast.Walk(node, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if n.Type() == ast.TypeBlock {
			lines := n.Lines()
			for i := 0; i < lines.Len(); i++ {
				seg := lines.At(i)
				if seg.Stop > end {
					end = seg.Stop
				}
			}
		}
		switch v := n.(type) {
		case *ast.Text:
			if v.Segment.Stop > end {
				end = v.Segment.Stop
			}
		case *ast.String:
			if stop := v.Pos() + len(v.Value); stop > end {
				end = stop
			}
		}
		return ast.WalkContinue, nil
	})
	if end <= start {
		return 0, 0, false
	}
	return start, end, true
}

func closeOpenFences(text string) string {
	lines := strings.Split(text, "\n")
	fence := ""
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "```"):
			if fence == "" {
				fence = "```"
			} else if fence == "```" {
				fence = ""
			}
		case strings.HasPrefix(trimmed, "~~~"):
			if fence == "" {
				fence = "~~~"
			} else if fence == "~~~" {
				fence = ""
			}
		}
	}
	if fence == "" {
		return text
	}
	if strings.HasSuffix(text, "\n") {
		return text + fence
	}
	return text + "\n" + fence
}
