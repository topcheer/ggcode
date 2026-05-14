package markdownx

import (
	"image/color"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/yuin/goldmark/ast"
	east "github.com/yuin/goldmark/extension/ast"
)

// tableSegment renders a GFM table.
type tableSegment struct {
	headers []string
	rows    [][]string
}

func newTableSegment(n *east.Table, src string, r *nodeRenderers) *tableSegment {
	ts := &tableSegment{}

	for child := n.FirstChild(); child != nil; child = child.NextSibling() {
		switch row := child.(type) {
		case *east.TableHeader:
			ts.headers = ts.extractCells(row, src)
		case *east.TableRow:
			ts.rows = append(ts.rows, ts.extractCells(row, src))
		}
	}

	return ts
}

func (ts *tableSegment) extractCells(node ast.Node, src string) []string {
	var cells []string
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		if cell, ok := child.(*east.TableCell); ok {
			text := strings.TrimSpace(extractText(cell, src))
			cells = append(cells, text)
		}
	}
	return cells
}

func (s *tableSegment) Inline() bool { return false }
func (s *tableSegment) Textual() string {
	var sb strings.Builder
	if len(s.headers) > 0 {
		sb.WriteString(strings.Join(s.headers, " "))
		sb.WriteString(" ")
	}
	for _, row := range s.rows {
		sb.WriteString(strings.Join(row, " "))
		sb.WriteString(" ")
	}
	return sb.String()
}
func (s *tableSegment) Update(fyne.CanvasObject)  {}
func (s *tableSegment) Select(_, _ fyne.Position) {}
func (s *tableSegment) SelectedText() string      { return "" }
func (s *tableSegment) Unselect()                 {}

func (s *tableSegment) Visual() fyne.CanvasObject {
	if len(s.headers) == 0 && len(s.rows) == 0 {
		return widget.NewLabel("")
	}

	numCols := len(s.headers)
	for _, row := range s.rows {
		if len(row) > numCols {
			numCols = len(row)
		}
	}
	if numCols == 0 {
		return widget.NewLabel("")
	}

	// Calculate column widths.
	widths := make([]int, numCols)
	for i, h := range s.headers {
		if runeWidth(h) > widths[i] {
			widths[i] = runeWidth(h)
		}
	}
	for _, row := range s.rows {
		for i, cell := range row {
			w := runeWidth(cell)
			if i < numCols && w > widths[i] {
				widths[i] = w
			}
		}
	}

	var objects []fyne.CanvasObject

	// Header row with bold + background.
	for i := 0; i < numCols; i++ {
		text := s.cellText(i, s.headers, widths)
		l := widget.NewLabelWithStyle(text, fyne.TextAlignLeading, fyne.TextStyle{Bold: true, Monospace: true})
		bg := canvas.NewRectangle(colorWithAlpha(theme.PrimaryColor(), 30))
		objects = append(objects, container.NewStack(bg, l))
	}

	// Data rows.
	for ri, row := range s.rows {
		for i := 0; i < numCols; i++ {
			text := s.cellText(i, row, widths)
			l := widget.NewLabel(text)
			l.TextStyle.Monospace = true
			// Alternate row background.
			if ri%2 == 1 {
				bg := canvas.NewRectangle(colorWithAlpha(theme.DisabledColor(), 20))
				objects = append(objects, container.NewStack(bg, l))
			} else {
				objects = append(objects, l)
			}
		}
	}

	grid := container.NewGridWithColumns(numCols, objects...)
	return container.NewPadded(grid)
}

func (s *tableSegment) cellText(col int, row []string, widths []int) string {
	if col >= len(row) || col >= len(widths) {
		return ""
	}
	cell := row[col]
	pad := widths[col] - runeWidth(cell)
	if pad < 0 {
		pad = 0
	}
	return cell + strings.Repeat(" ", pad)
}

// colorWithAlpha applies alpha to a color.Color.
func colorWithAlpha(c color.Color, alpha uint8) color.RGBA {
	r, g, b, _ := c.RGBA()
	return color.RGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8), A: alpha}
}
