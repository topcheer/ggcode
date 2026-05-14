package markdownx

import (
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/widget"
)

// MarkdownWidget renders Markdown using raw canvas.Text and canvas.Rectangle.
type MarkdownWidget struct {
	widget.BaseWidget
	mu sync.Mutex

	buffer strings.Builder

	objects []fyne.CanvasObject
	minSize fyne.Size
	builtW  float32 // width used for last rebuild
	inBuild bool   // prevent re-entrant rebuild

	debounceMu    sync.Mutex
	debounceTimer *time.Timer
}

func NewMarkdownWidget() *MarkdownWidget {
	w := &MarkdownWidget{}
	w.ExtendBaseWidget(w)
	return w
}

func (w *MarkdownWidget) SetMarkdown(text string) {
	w.mu.Lock()
	w.buffer.Reset()
	w.buffer.WriteString(text)
	w.mu.Unlock()
	w.doRebuild()
}

// Resize is called when the widget's parent changes its size.
// We need to re-render because our layout depends on width.
func (w *MarkdownWidget) Resize(size fyne.Size) {
	w.BaseWidget.Resize(size)
	if w.inBuild || size.Width <= 0 {
		return
	}
	if w.builtW <= 0 || abs32(size.Width-w.builtW) > 5 {
		w.doRebuild()
	}
}

func abs32(x float32) float32 {
	if x < 0 {
		return -x
	}
	return x
}

func (w *MarkdownWidget) AppendChunk(chunk string) {
	w.mu.Lock()
	w.buffer.WriteString(chunk)
	w.mu.Unlock()
	w.scheduleDebouncedRebuild()
}

func (w *MarkdownWidget) Content() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buffer.String()
}

func (w *MarkdownWidget) CreateRenderer() fyne.WidgetRenderer {
	r := &mdRenderer{widget: w}
	return r
}

func (w *MarkdownWidget) doRebuild() {
	if w.inBuild {
		return
	}
	w.inBuild = true
	defer func() { w.inBuild = false }()

	text := w.buffer.String()
	text = closeOpenCodeBlocks(text)

	newBlocks := parseBlocks(text)

	width := float32(600)
	if s := w.Size(); s.Width > 0 {
		width = s.Width
	}

	var allObjects []fyne.CanvasObject
	y := float32(0)

	for _, b := range newBlocks {
		objs, h := b.render(width)
		for _, obj := range objs {
			offsetY(obj, y)
			allObjects = append(allObjects, obj)
		}
		y += h
	}

	w.objects = allObjects
	w.minSize = fyne.NewSize(width, y)
	w.builtW = width

	// Important: Refresh triggers repaint. If renderer doesn't exist yet,
	// CreateRenderer will be called later and will see the new objects.
	w.BaseWidget.Refresh()
}

func (w *MarkdownWidget) scheduleDebouncedRebuild() {
	w.debounceMu.Lock()
	defer w.debounceMu.Unlock()
	if w.debounceTimer != nil {
		w.debounceTimer.Stop()
	}
	w.debounceTimer = time.AfterFunc(100*time.Millisecond, func() {
		fyne.Do(func() { w.doRebuild() })
	})
}

// ── Renderer ───────────────────────────────────────

type mdRenderer struct {
	widget *MarkdownWidget
}

func (r *mdRenderer) Destroy() {}

func (r *mdRenderer) Layout(size fyne.Size) {
	// Objects are absolutely positioned. Just ensure they're visible.
	for _, obj := range r.Objects() {
		obj.Show()
	}
}

func (r *mdRenderer) MinSize() fyne.Size {
	ms := r.widget.minSize
	if ms.IsZero() {
		return fyne.NewSize(100, 20)
	}
	return ms
}

func (r *mdRenderer) Objects() []fyne.CanvasObject {
	return r.widget.objects
}

func (r *mdRenderer) Refresh() {
	canvas.Refresh(r.widget)
}

// ── Position helpers ───────────────────────────────

func offsetY(obj fyne.CanvasObject, dy float32) {
	switch v := obj.(type) {
	case *canvas.Text:
		p := v.Position()
		v.Move(fyne.NewPos(p.X, p.Y+dy))
	case *canvas.Rectangle:
		p := v.Position()
		v.Move(fyne.NewPos(p.X, p.Y+dy))
	case *canvas.Line:
		v.Position1 = fyne.NewPos(v.Position1.X, v.Position1.Y+dy)
		v.Position2 = fyne.NewPos(v.Position2.X, v.Position2.Y+dy)
	}
}
