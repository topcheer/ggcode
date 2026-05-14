package markdownx

import (
	"image/color"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
)

// newTable creates a styled table with header highlighting and alternating rows.
func newTable(headers []string, rows [][]string) fyne.CanvasObject {
	if len(headers) == 0 && len(rows) == 0 {
		return nil
	}

	numCols := len(headers)
	for _, row := range rows {
		if len(row) > numCols {
			numCols = len(row)
		}
	}
	if numCols == 0 {
		return nil
	}

	// Calculate column widths.
	widths := make([]int, numCols)
	for i, h := range headers {
		if runeWidth(h) > widths[i] {
			widths[i] = runeWidth(h)
		}
	}
	for _, row := range rows {
		for i, cell := range row {
			if i < numCols && runeWidth(cell) > widths[i] {
				widths[i] = runeWidth(cell)
			}
		}
	}

	var cells []fyne.CanvasObject

	// Header row.
	for i := 0; i < numCols; i++ {
		text := padCell(i, headers, widths)
		l := widget.NewLabelWithStyle(text, fyne.TextAlignLeading, fyne.TextStyle{Bold: true, Monospace: true})
		bg := canvas.NewRectangle(color.RGBA{R: 60, G: 90, B: 140, A: 80})
		cells = append(cells, container.NewStack(bg, l))
	}

	// Data rows.
	for ri, row := range rows {
		for i := 0; i < numCols; i++ {
			text := padCell(i, row, widths)
			l := widget.NewLabel(text)
			l.TextStyle = fyne.TextStyle{Monospace: true}
			if ri%2 == 1 {
				bg := canvas.NewRectangle(color.RGBA{R: 50, G: 50, B: 50, A: 40})
				cells = append(cells, container.NewStack(bg, l))
			} else {
				cells = append(cells, l)
			}
		}
	}

	grid := container.NewGridWithColumns(numCols, cells...)
	return container.New(layout.NewCustomPaddedLayout(4, 4, 0, 0), grid)
}

func padCell(col int, row []string, widths []int) string {
	if col >= len(row) {
		return strings.Repeat(" ", widths[col])
	}
	cell := row[col]
	pad := widths[col] - runeWidth(cell)
	if pad < 0 {
		pad = 0
	}
	return cell + strings.Repeat(" ", pad)
}
