package chat

import (
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"

	mdpkg "github.com/topcheer/ggcode/internal/markdown"
)

var streamMarkdown = goldmark.New(goldmark.WithExtensions(extension.GFM))

type streamRenderCache struct {
	width    int
	source   string
	blocks   []string
	rendered []string
}

func renderStreamingMarkdown(text string, width int, cache *streamRenderCache) (string, streamRenderCache) {
	normalized := normalizeStreamingMarkdown(text)
	blocks := splitMarkdownBlocks(normalized)
	if len(blocks) == 0 {
		return "", streamRenderCache{width: width, source: normalized}
	}

	next := streamRenderCache{
		width:    width,
		source:   normalized,
		blocks:   append([]string(nil), blocks...),
		rendered: make([]string, len(blocks)),
	}

	reuse := 0
	if cache != nil && cache.width == width && strings.HasPrefix(normalized, cache.source) {
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

	return strings.Join(next.rendered, "\n\n"), next
}

func normalizeStreamingMarkdown(text string) string {
	return mdpkg.Normalize(closeOpenFences(text))
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
			// with the URL. We only need the text — the child walk handles it.
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
