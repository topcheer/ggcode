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

const maxStreamingBlockLines = 400

const maxStreamingTotalLines = 600

type streamRenderCache struct {
	width     int
	source    string
	blocks    []string
	rendered  []string
	trimStart int
	blockCut  int
}

func renderStreamingMarkdown(text string, width int, cache *streamRenderCache) (string, streamRenderCache) {

	rawSource := mdpkg.Normalize(text)

	trimStart := stickyTrimStart(text, cache, width)
	trimmed := trimLeadingLines(text, trimStart)
	normalized := mdpkg.Normalize(closeOpenFences(trimmed))
	blocks := splitMarkdownBlocks(normalized)
	if len(blocks) == 0 {
		return "", streamRenderCache{width: width, source: rawSource, trimStart: trimStart}
	}

	blocks[len(blocks)-1] = capStreamingBlock(blocks[len(blocks)-1])

	blockCut, hiddenLines := stickyBlockCut(blocks, trimStart, cache, width)
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

func stickyTrimStart(text string, cache *streamRenderCache, width int) int {
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
	return trimStart
}

func stickyBlockCut(blocks []string, trimStart int, cache *streamRenderCache, width int) (cut, hiddenLines int) {
	blockCut := 0
	if cache != nil && cache.width == width {
		blockCut = cache.blockCut
	}
	desiredCut := leadingBlockCut(blocks, maxStreamingTotalLines)
	if desiredCut-blockCut >= maxStreamingTotalLines/4 || desiredCut < blockCut {
		blockCut = desiredCut
	}
	hidden := trimStart
	for i := 0; i < blockCut; i++ {
		hidden += strings.Count(blocks[i], "\n") + 1
	}
	return blockCut, hidden
}

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

	if fence := missingOpenFence(kept); fence != "" {
		sb.WriteString(fence + "\n")
	}
	sb.WriteString(strings.Join(kept, "\n"))
	return sb.String()
}

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

			if v.SoftLineBreak() {
				sb.WriteByte('\n')
			}
		case *ast.String:
			sb.Write(v.Value)
		case *ast.Link:

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
