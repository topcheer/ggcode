package markdownx

import (
	"fmt"
	"image/color"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
)

// block is the interface for a rendered markdown block element.
type block interface {
	// render creates CanvasObjects for this block at the given width.
	// Returns objects and the block's total height.
	render(width float32) ([]fyne.CanvasObject, float32)
}

// ── HeadingBlock ───────────────────────────────────

type headingBlock struct {
	level int
	runs  []textRun
}

func (b *headingBlock) render(width float32) ([]fyne.CanvasObject, float32) {
	il := newInlineLayout(b.runs, width)
	objs := il.render(0)
	return objs, il.height() + blockSpacing
}

// ── ParagraphBlock ─────────────────────────────────

type paragraphBlock struct {
	runs []textRun
}

func (b *paragraphBlock) render(width float32) ([]fyne.CanvasObject, float32) {
	il := newInlineLayout(b.runs, width)
	objs := il.render(paraSpacing)
	return objs, il.height() + paraSpacing*2
}

// ── CodeBlock ──────────────────────────────────────

type codeBlock struct {
	lang  string
	lines []string
	color []color.Color // per-line color from chroma
}

func (b *codeBlock) render(width float32) ([]fyne.CanvasObject, float32) {
	var objects []fyne.CanvasObject

	numWidth := len(fmt.Sprintf("%d", len(b.lines)))
	y := float32(4) // top padding

	for i, line := range b.lines {
		// Line number.
		numText := fmt.Sprintf("%*d  ", numWidth, i+1)
		num := canvas.NewText(numText, colLineNum)
		num.TextStyle = fyne.TextStyle{Monospace: true}
		num.TextSize = lineNumSize
		num.Move(fyne.NewPos(8, y))
		objects = append(objects, num)

		// Code line.
		col := color.Color(colFg)
		if b.color != nil && i < len(b.color) && b.color[i] != nil {
			col = b.color[i]
		}
		code := canvas.NewText(line, col)
		code.TextStyle = fyne.TextStyle{Monospace: true}
		code.TextSize = codeSize
		code.Move(fyne.NewPos(8+measureText(numText, textStyle{monospace: true, size: lineNumSize}), y))
		objects = append(objects, code)

		lineH := fyne.MeasureText("Ag", codeSize, fyne.TextStyle{Monospace: true}).Height
		y += lineH + 2
	}

	// Background.
	bg := canvas.NewRectangle(colCodeBg)
	bgH := y + 4 // bottom padding
	bg.Resize(fyne.NewSize(width, bgH))
	bg.Move(fyne.NewPos(0, 0))

	// Language label.
	if b.lang != "" {
		langLabel := canvas.NewText(b.lang, colFgDim)
		langLabel.TextStyle = fyne.TextStyle{Italic: true}
		langLabel.TextSize = lineNumSize
		langLabel.Move(fyne.NewPos(width-measureText(b.lang, textStyle{italic: true, size: lineNumSize})-8, 2))
		objects = append(objects, langLabel)
	}

	// Background first, then text on top.
	all := []fyne.CanvasObject{bg}
	all = append(all, objects...)
	return all, bgH + blockSpacing
}

// ── ListBlock ──────────────────────────────────────

type listBlock struct {
	ordered bool
	items   []listItem
}

type listItem struct {
	runs  []textRun // inline content
	children []block // nested blocks
}

func (b *listBlock) render(width float32) ([]fyne.CanvasObject, float32) {
	var objects []fyne.CanvasObject
	y := float32(0)
	innerWidth := width - indentSize

	for i, item := range b.items {
		// Bullet or number.
		prefix := "• "
		if b.ordered {
			prefix = fmt.Sprintf("%d. ", i+1)
		}
		bullet := canvas.NewText(prefix, colFg)
		bullet.TextStyle = fyne.TextStyle{Bold: true}
		bullet.TextSize = textSize
		bullet.Move(fyne.NewPos(0, y+paraSpacing))
		objects = append(objects, bullet)

		// Item content.
		il := newInlineLayout(item.runs, innerWidth-measureText(prefix, textStyle{bold: true, size: textSize}))
		itemObjs := il.render(y + paraSpacing)
		for _, obj := range itemObjs {
			if t, ok := obj.(*canvas.Text); ok {
				t.Move(fyne.NewPos(indentSize, t.Position().Y))
			}
			objects = append(objects, obj)
		}
		y += il.height() + paraSpacing*2

		// Nested blocks.
		for _, child := range item.children {
			childObjs, childH := child.render(innerWidth)
			for _, obj := range childObjs {
				if t, ok := obj.(*canvas.Text); ok {
					t.Move(fyne.NewPos(indentSize, t.Position().Y+y))
				} else if r, ok := obj.(*canvas.Rectangle); ok {
					r.Move(fyne.NewPos(indentSize, r.Position().Y+y))
				}
				objects = append(objects, obj)
			}
			y += childH
		}
	}
	return objects, y
}

// ── BlockquoteBlock ────────────────────────────────

type blockquoteBlock struct {
	children []block
}

func (b *blockquoteBlock) render(width float32) ([]fyne.CanvasObject, float32) {
	innerWidth := width - quoteIndent - quoteBarWidth
	var objects []fyne.CanvasObject
	y := float32(4)

	// Left bar.
	bar := canvas.NewRectangle(colQuoteBar)
	bar.SetMinSize(fyne.NewSize(quoteBarWidth, 0))
	objects = append(objects, bar)

	// Background.
	bg := canvas.NewRectangle(colQuoteBg)

	// Children.
	for _, child := range b.children {
		childObjs, childH := child.render(innerWidth)
		for _, obj := range childObjs {
			if t, ok := obj.(*canvas.Text); ok {
				t.Move(fyne.NewPos(quoteIndent+quoteBarWidth, t.Position().Y+y))
			} else if r, ok := obj.(*canvas.Rectangle); ok {
				r.Move(fyne.NewPos(quoteIndent+quoteBarWidth, r.Position().Y+y))
			}
			objects = append(objects, obj)
		}
		y += childH
	}

	totalH := y + 4
	bar.Resize(fyne.NewSize(quoteBarWidth, totalH))
	bg.Resize(fyne.NewSize(width, totalH))
	bg.Move(fyne.NewPos(0, 0))

	// bg and bar first.
	all := []fyne.CanvasObject{bg, bar}
	all = append(all, objects...)
	return all, totalH + blockSpacing
}

// ── TableBlock ─────────────────────────────────────

type tableBlock struct {
	headers []string
	rows    [][]string
}

func (b *tableBlock) render(width float32) ([]fyne.CanvasObject, float32) {
	numCols := len(b.headers)
	for _, row := range b.rows {
		if len(row) > numCols {
			numCols = len(row)
		}
	}
	if numCols == 0 {
		return nil, 0
	}

	// Calculate column widths.
	colWidths := make([]float32, numCols)
	for i, h := range b.headers {
		w := measureText(h, textStyle{bold: true, monospace: true, size: codeSize})
		if w > colWidths[i] {
			colWidths[i] = w
		}
	}
	for _, row := range b.rows {
		for i, cell := range row {
			w := measureText(cell, textStyle{monospace: true, size: codeSize})
			if i < numCols && w > colWidths[i] {
				colWidths[i] = w
			}
		}
	}
	// Add padding.
	for i := range colWidths {
		colWidths[i] += 8
	}

	var objects []fyne.CanvasObject
	y := float32(0)
	// Header row.
	x := float32(0)
	for i := 0; i < numCols; i++ {
		text := b.cellText(i, b.headers, colWidths)
		t := canvas.NewText(text, colFgBold)
		t.TextStyle = fyne.TextStyle{Bold: true, Monospace: true}
		t.TextSize = codeSize
		t.Move(fyne.NewPos(x+4, y+2))
		bg := canvas.NewRectangle(colTblHead)
		bg.Move(fyne.NewPos(x, y))
		bg.Resize(fyne.NewSize(colWidths[i], rowHeight()))
		objects = append(objects, bg, t)
		x += colWidths[i]
	}
	y += rowHeight()

	// Data rows.
	for ri, row := range b.rows {
		for i := 0; i < numCols; i++ {
			text := b.cellText(i, row, colWidths)
			t := canvas.NewText(text, colFg)
			t.TextStyle = fyne.TextStyle{Monospace: true}
			t.TextSize = codeSize
			t.Move(fyne.NewPos(x+4, y+2))
			if ri%2 == 1 {
				bg := canvas.NewRectangle(colTblAlt)
				bg.Move(fyne.NewPos(x, y))
				bg.Resize(fyne.NewSize(colWidths[i], rowHeight()))
				objects = append(objects, bg)
			}
			objects = append(objects, t)
			x += colWidths[i]
		}
		y += rowHeight()
	}

	return objects, y + blockSpacing
}

func (b *tableBlock) cellText(col int, row []string, widths []float32) string {
	if col >= len(row) {
		return ""
	}
	cell := row[col]
	pad := int(widths[col] - measureText(cell, textStyle{monospace: true, size: codeSize}) - 4)
	if pad < 0 {
		pad = 0
	}
	return cell + strings.Repeat(" ", pad/2)
}

func rowHeight() float32 {
	return fyne.MeasureText("Ag", codeSize, fyne.TextStyle{Monospace: true}).Height + 4
}

// ── HRBlock ────────────────────────────────────────

type hrBlock struct{}

func (b *hrBlock) render(width float32) ([]fyne.CanvasObject, float32) {
	line := canvas.NewLine(colHR)
	line.StrokeWidth = 1
	line.Resize(fyne.NewSize(width, 1))
	line.Move(fyne.NewPos(0, 4))
	return []fyne.CanvasObject{line}, 10 + blockSpacing
}

// Ensure fmt and strings used.
var _ = fmt.Sprintf
var _ = strings.Repeat
