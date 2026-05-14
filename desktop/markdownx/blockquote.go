package markdownx

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
)

// newBlockquote creates a styled blockquote with left border and background.
func newBlockquote(children []fyne.CanvasObject) fyne.CanvasObject {
	if len(children) == 0 {
		return nil
	}

	// Left border bar.
	bar := canvas.NewRectangle(color.RGBA{R: 60, G: 120, B: 216, A: 255})
	bar.SetMinSize(fyne.NewSize(3, 0))

	content := container.NewVBox(children...)

	// Background.
	bg := canvas.NewRectangle(color.RGBA{R: 50, G: 50, B: 50, A: 30})

	inner := container.NewStack(bg,
		container.NewHBox(bar, container.NewPadded(content)),
	)
	return container.New(layout.NewCustomPaddedLayout(4, 4, 0, 0), inner)
}

// Ensure layout import.
var _ = layout.NewCustomPaddedLayout
