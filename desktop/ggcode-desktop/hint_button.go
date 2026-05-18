package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// hintWrap wraps any widget to show a tooltip on mouse hover.
// It uses desktop.Hoverable interface to detect mouse in/out.
type hintWrap struct {
	widget.BaseWidget
	child fyne.Widget
	hint  string

	popover  *fyne.Container
	tipBg    *canvas.Rectangle
	tipText  *canvas.Text
	inited   bool
}

func newHintWrap(child fyne.Widget, hint string) *hintWrap {
	hw := &hintWrap{
		child: child,
		hint:  hint,
	}
	hw.ExtendBaseWidget(hw)
	return hw
}

func (hw *hintWrap) CreateRenderer() fyne.WidgetRenderer {
	hw.tipText = canvas.NewText(hw.hint, theme.ForegroundColor())
	hw.tipText.TextSize = 11

	hw.tipBg = canvas.NewRectangle(theme.DisabledColor())
	hw.tipBg.CornerRadius = 4
	hw.tipBg.Hide()

	padded := container.NewPadded(hw.tipText)
	padded.Hide()

	hw.popover = container.NewStack(hw.tipBg, padded)
	hw.popover.Hide()

	stack := container.NewWithoutLayout(hw.child, hw.popover)
	hw.inited = true
	return widget.NewSimpleRenderer(stack)
}

func (hw *hintWrap) MouseIn(ev *desktop.MouseEvent) {
	if !hw.inited {
		return
	}
	cs := hw.child.Size()
	ts := hw.popover.MinSize()
	x := (cs.Width - ts.Width) / 2
	y := -ts.Height - 4
	hw.popover.Move(fyne.NewPos(x, y))
	hw.popover.Show()
	hw.tipBg.Show()
	hw.popover.Refresh()
}

func (hw *hintWrap) MouseOut() {
	if !hw.inited {
		return
	}
	hw.popover.Hide()
	hw.tipBg.Hide()
	hw.popover.Refresh()
}

func (hw *hintWrap) MouseDown(*desktop.MouseEvent) {}

func (hw *hintWrap) MouseUp(*desktop.MouseEvent) {}

func (hw *hintWrap) MouseMoved(*desktop.MouseEvent) {}

func (hw *hintWrap) Cursor() desktop.Cursor {
	return desktop.PointerCursor
}

var _ desktop.Hoverable = (*hintWrap)(nil)
