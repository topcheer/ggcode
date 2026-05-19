package markdownx

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image/color"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

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
		if b.lang == "mermaid" {
			return renderMermaidBlock(b)
		}
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

	// Join all lines into a single text block for RichText rendering.
	codeText := strings.Join(b.lines, "\n")

	codeSeg := &widget.TextSegment{
		Style: widget.RichTextStyle{
			TextStyle: fyne.TextStyle{Monospace: true},
		},
		Text: codeText,
	}

	rt := widget.NewRichText(codeSeg)
	rt.Wrapping = fyne.TextWrapBreak

	// Dark background.
	bg := canvas.NewRectangle(colCodeBg)
	bg.SetMinSize(fyne.NewSize(0, 0))

	inner := container.NewStack(bg, container.New(layout.NewCustomPaddedLayout(4, 4, 8, 8), rt))
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

// ── Mermaid Diagram ────────────────────────────────────

func renderMermaidBlock(b *mdBlock) fyne.CanvasObject {
	mermaidCode := strings.Join(b.lines, "\n")

	placeholder := widget.NewLabel("Loading diagram...")
	placeholder.Alignment = fyne.TextAlignCenter

	img := &canvas.Image{}
	img.FillMode = canvas.ImageFillContain
	img.SetMinSize(fyne.NewSize(600, 400))
	img.Hide()

	wrapper := container.NewStack(placeholder, img)

	go func() {
		pngData, err := fetchMermaidPNG(mermaidCode)
		if err != nil {
			fyne.Do(func() {
				placeholder.SetText(fmt.Sprintf("Diagram unavailable: %v", err))
				placeholder.Refresh()
			})
			return
		}
		if len(pngData) < 8 || !bytes.HasPrefix(pngData, []byte("\x89PNG")) {
			fyne.Do(func() {
				placeholder.SetText("Diagram rendering returned invalid data")
				placeholder.Refresh()
			})
			return
		}
		img.Resource = fyne.NewStaticResource("mermaid.png", pngData)
		fyne.Do(func() {
			placeholder.Hide()
			img.Show()
			img.Refresh()
			wrapper.Refresh()
		})
	}()

	return padded(6, 6, 0, 0, wrapper)
}

// fetchMermaidPNG tries kroki.io first, then mermaid.ink as fallback.
func fetchMermaidPNG(mermaidCode string) ([]byte, error) {
	// Backend 1: kroki.io
	if data, err := fetchKroki(mermaidCode); err == nil {
		return data, nil
	}
	// Backend 2: mermaid.ink
	if data, err := fetchMermaidInk(mermaidCode); err == nil {
		return data, nil
	}
	return nil, fmt.Errorf("all mermaid backends failed")
}

func fetchKroki(mermaidCode string) ([]byte, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Post("https://kroki.io/mermaid/png", "text/plain", strings.NewReader(mermaidCode))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("kroki returned %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

func fetchMermaidInk(mermaidCode string) ([]byte, error) {
	encoded := base64.URLEncoding.EncodeToString([]byte(mermaidCode))
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get("https://mermaid.ink/img/" + encoded)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("mermaid.ink returned %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// Ensure imports.
var _ = color.RGBA{}
var _ = fyne.MeasureText
var _ = theme.ColorNameForeground
