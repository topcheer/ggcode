package markdownx

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/yuin/goldmark/ast"
)

// blockquoteSegment renders a block quote with left border.
type blockquoteSegment struct {
	n   *ast.Blockquote
	src string
	r   *nodeRenderers
}

func newBlockquoteSegment(n *ast.Blockquote, src string, r *nodeRenderers) *blockquoteSegment {
	return &blockquoteSegment{n: n, src: src, r: r}
}

func (s *blockquoteSegment) Inline() bool              { return false }
func (s *blockquoteSegment) Textual() string           { return extractText(s.n, s.src) }
func (s *blockquoteSegment) Update(fyne.CanvasObject)  {}
func (s *blockquoteSegment) Select(_, _ fyne.Position) {}
func (s *blockquoteSegment) SelectedText() string      { return "" }
func (s *blockquoteSegment) Unselect()                 {}

func (s *blockquoteSegment) Visual() fyne.CanvasObject {
	var segs []widget.RichTextSegment
	walkAST(s.n, s.src, s.r, &segs, defaultStyle())

	rt := widget.NewRichText(segs...)
	rt.Wrapping = fyne.TextWrapWord

	// Left border bar.
	bar := canvas.NewRectangle(theme.PrimaryColor())
	bar.SetMinSize(fyne.NewSize(3, 0))

	bg := canvas.NewRectangle(color.RGBA{R: 60, G: 60, B: 60, A: 40})

	content := container.NewStack(bg,
		container.New(layout.NewBorderLayout(nil, nil, bar, nil),
			bar,
			container.NewPadded(rt),
		),
	)
	return content
}
