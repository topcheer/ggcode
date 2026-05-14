package markdownx

import (
	"fmt"
	"image/color"
	"net/url"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// renderBlock creates a Fyne CanvasObject for a parsed block.
func renderBlock(b *mdBlock) fyne.CanvasObject {
	switch b.kind {
	case blockHeading:
		return renderHeading(b)
	case blockParagraph:
		return renderParagraph(b)
	case blockCode:
		return renderCodeBlock(b)
	case blockList:
		return renderList(b)
	case blockBlockquote:
		return renderBlockquote(b)
	case blockTable:
		return renderTableBlock(b)
	case blockHR:
		return renderHR()
	}
	return widget.NewLabel("???")
}

// ── Heading ────────────────────────────────────────

func renderHeading(b *mdBlock) fyne.CanvasObject {
	rt := widget.NewRichText(headingSegments(b.content, b.level)...)
	rt.Wrapping = fyne.TextWrapWord
	return padded(6, 2, 0, 0, rt)
}

// ── Paragraph ──────────────────────────────────────

func renderParagraph(b *mdBlock) fyne.CanvasObject {
	if len(b.runs) > 0 {
		return renderInlineRuns(b.runs)
	}
	// Fallback: plain text
	rt := widget.NewRichText(&widget.ParagraphSegment{Texts: []widget.RichTextSegment{normalSeg(b.content)}})
	rt.Wrapping = fyne.TextWrapWord
	return padded(2, 2, 0, 0, rt)
}

func renderInlineRuns(runs []inlineRun) fyne.CanvasObject {
	var segs []widget.RichTextSegment
	for _, r := range runs {
		switch {
		case r.code:
			segs = append(segs, codeSpanSeg(r.text))
		case r.bold && r.italic:
			segs = append(segs, &widget.TextSegment{Text: r.text, Style: widget.RichTextStyle{
				Inline: true, TextStyle: fyne.TextStyle{Bold: true, Italic: true}}})
		case r.bold:
			segs = append(segs, boldSeg(r.text))
		case r.italic:
			segs = append(segs, italicSeg(r.text))
		case r.link != "":
			u, _ := url.Parse(r.link)
			if u != nil {
				segs = append(segs, &widget.HyperlinkSegment{Text: r.text, URL: u})
			} else {
				segs = append(segs, normalSeg(r.text))
			}
		default:
			segs = append(segs, normalSeg(r.text))
		}
	}
	rt := widget.NewRichText(&widget.ParagraphSegment{Texts: segs})
	rt.Wrapping = fyne.TextWrapWord
	return padded(2, 2, 0, 0, rt)
}

// ── Code Block ─────────────────────────────────────

func renderCodeBlock(b *mdBlock) fyne.CanvasObject {
	if len(b.lines) == 0 {
		return nil
	}

	numWidth := len(fmt.Sprintf("%d", len(b.lines)))

	// Per-line rendering with syntax colors.
	var lineObjs []fyne.CanvasObject
	for i, line := range b.lines {
		// Line number.
		numText := fmt.Sprintf("%*d  ", numWidth, i+1)
		numLabel := canvas.NewText(numText, color.RGBA{R: 100, G: 100, B: 100, A: 255})
		numLabel.TextStyle = fyne.TextStyle{Monospace: true}
		numLabel.TextSize = 13

		// Code text.
		codeColor := color.RGBA{R: 220, G: 220, B: 220, A: 255} // default
		if b.colors != nil && i < len(b.colors) && b.colors[i] != nil {
			codeColor = b.colors[i].(color.RGBA)
		}
		codeLabel := canvas.NewText(line, codeColor)
		codeLabel.TextStyle = fyne.TextStyle{Monospace: true}
		codeLabel.TextSize = 13

		row := container.NewHBox(numLabel, codeLabel)
		lineObjs = append(lineObjs, row)
	}

	codeVBox := container.NewVBox(lineObjs...)

	// Dark background.
	bg := canvas.NewRectangle(colCodeBg)
	bg.SetMinSize(fyne.NewSize(0, 0))

	inner := container.NewStack(bg, container.New(layout.NewCustomPaddedLayout(4, 4, 8, 8), codeVBox))
	return container.New(layout.NewCustomPaddedLayout(4, 4, 0, 0), inner)
}

// ── List ───────────────────────────────────────────

func renderList(b *mdBlock) fyne.CanvasObject {
	return renderListWithIndent(b, 0)
}

func renderListWithIndent(b *mdBlock, indentLevel int) fyne.CanvasObject {
	indent := float32(indentLevel) * 20
	var items []fyne.CanvasObject
	for i, item := range b.items {
		prefix := "• "
		if b.ordered {
			prefix = fmt.Sprintf("%d. ", i+1)
		}

		bullet := widget.NewLabelWithStyle(prefix, fyne.TextAlignTrailing, fyne.TextStyle{Bold: true})
		bulletRow := container.NewHBox(bullet)

		content := widget.NewLabel(strings.TrimSpace(item.text))
		content.Wrapping = fyne.TextWrapWord

		var rowChildren []fyne.CanvasObject
		rowChildren = append(rowChildren, container.NewBorder(nil, nil, bulletRow, nil, content))

		// Nested sub-list.
		if item.children != nil {
			rowChildren = append(rowChildren, renderListWithIndent(item.children, indentLevel+1))
		}

		items = append(items, container.NewVBox(rowChildren...))
	}
	inner := container.NewVBox(items...)
	return container.New(layout.NewCustomPaddedLayout(2, 2, indent+16, 0), inner)
}

// ── Blockquote ─────────────────────────────────────

func renderBlockquote(b *mdBlock) fyne.CanvasObject {
	var children []fyne.CanvasObject
	for _, child := range b.children {
		children = append(children, renderBlock(child))
	}

	bar := canvas.NewRectangle(colQuoteBar)
	bar.SetMinSize(fyne.NewSize(3, 0))

	bg := canvas.NewRectangle(colQuoteBg)

	content := container.NewVBox(children...)
	inner := container.NewStack(bg, container.NewBorder(nil, nil, bar, nil, content))
	return container.New(layout.NewCustomPaddedLayout(4, 4, 0, 0), inner)
}

// ── Table ──────────────────────────────────────────

func renderTableBlock(b *mdBlock) fyne.CanvasObject {
	numCols := len(b.headers)
	for _, row := range b.rows {
		if len(row) > numCols {
			numCols = len(row)
		}
	}
	if numCols == 0 {
		return nil
	}

	var cells []fyne.CanvasObject

	// Header row.
	for i := 0; i < numCols; i++ {
		text := cellText(i, b.headers)
		label := widget.NewLabelWithStyle(text, fyne.TextAlignLeading, fyne.TextStyle{Bold: true, Monospace: true})
		label.Wrapping = fyne.TextWrapWord
		bg := canvas.NewRectangle(colTblHead)
		cells = append(cells, container.NewStack(bg, container.NewPadded(label)))
	}

	// Data rows.
	for ri, row := range b.rows {
		for i := 0; i < numCols; i++ {
			text := cellText(i, row)
			label := widget.NewLabel(text)
			label.TextStyle = fyne.TextStyle{Monospace: true}
			label.Wrapping = fyne.TextWrapWord
			if ri%2 == 1 {
				bg := canvas.NewRectangle(colTblAlt)
				cells = append(cells, container.NewStack(bg, container.NewPadded(label)))
			} else {
				cells = append(cells, container.NewPadded(label))
			}
		}
	}

	grid := container.NewGridWithColumns(numCols)
	grid.Objects = cells
	grid.Refresh()
	return container.New(layout.NewCustomPaddedLayout(4, 4, 0, 0), grid)
}

func cellText(col int, row []string) string {
	if col >= len(row) {
		return ""
	}
	return row[col]
}

// ── HR ─────────────────────────────────────────────

func renderHR() fyne.CanvasObject {
	line := canvas.NewLine(theme.DisabledColor())
	line.StrokeWidth = 1
	return container.NewPadded(line)
}

// ── Helper ─────────────────────────────────────────

func padded(top, bottom, left, right float32, obj fyne.CanvasObject) fyne.CanvasObject {
	return container.New(layout.NewCustomPaddedLayout(top, bottom, left, right), obj)
}

// Ensure imports.
var _ = color.RGBA{}
var _ = fyne.MeasureText
var _ = theme.ColorNameForeground
