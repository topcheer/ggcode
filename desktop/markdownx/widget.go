// Package markdownx provides a streaming-capable Markdown rendering widget for Fyne.
//
// It parses Markdown text using goldmark and renders it as native Fyne widgets,
// including syntax-highlighted code blocks, tables, block quotes, lists, and more.
//
// The widget supports streaming input with debounced re-rendering, making it
// suitable for real-time LLM output display.
package markdownx

import (
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"
)

// MarkdownWidget renders Markdown text as native Fyne widgets.
// It supports streaming input with debounced re-rendering.
type MarkdownWidget struct {
	widget.BaseWidget
	mu sync.Mutex

	buffer    strings.Builder
	segments  []widget.RichTextSegment
	renderers *nodeRenderers

	// Streaming support
	debounceMu    sync.Mutex
	debounceTimer *time.Timer
	wrapWidth     float32
}

// NewMarkdownWidget creates a new Markdown rendering widget.
func NewMarkdownWidget() *MarkdownWidget {
	w := &MarkdownWidget{
		renderers: newNodeRenderers(),
	}
	w.ExtendBaseWidget(w)
	return w
}

// SetMarkdown replaces the widget content with new markdown text.
// Safe to call from any goroutine.
func (w *MarkdownWidget) SetMarkdown(text string) {
	w.mu.Lock()
	w.buffer.Reset()
	w.buffer.WriteString(text)
	w.mu.Unlock()

	w.reparse()
}

// AppendChunk appends a streaming text chunk and triggers debounced re-render.
// Safe to call from any goroutine.
func (w *MarkdownWidget) AppendChunk(chunk string) {
	w.mu.Lock()
	w.buffer.WriteString(chunk)
	w.mu.Unlock()

	w.scheduleDebouncedReparse()
}

// Content returns the current accumulated markdown text.
func (w *MarkdownWidget) Content() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buffer.String()
}

// CreateRenderer implements fyne.Widget.
func (w *MarkdownWidget) CreateRenderer() fyne.WidgetRenderer {
	rt := widget.NewRichText()
	rt.Wrapping = fyne.TextWrapWord
	w.mu.Lock()
	rt.Segments = w.segments
	w.mu.Unlock()
	rt.Refresh()

	return &markdownRenderer{
		widget: w,
		rt:     rt,
	}
}

// reparse re-parses the buffer and refreshes the widget.
// Must be called on the UI thread or via fyne.Do.
func (w *MarkdownWidget) reparse() {
	w.mu.Lock()
	text := w.buffer.String()
	// Fix unclosed code blocks for streaming.
	text = closeOpenCodeBlocks(text)
	segments := parseMarkdown(text, w.renderers)
	w.segments = segments
	w.mu.Unlock()

	fyne.Do(func() {
		w.Refresh()
	})
}

func (w *MarkdownWidget) scheduleDebouncedReparse() {
	w.debounceMu.Lock()
	defer w.debounceMu.Unlock()

	if w.debounceTimer != nil {
		w.debounceTimer.Stop()
	}
	w.debounceTimer = time.AfterFunc(100*time.Millisecond, func() {
		fyne.Do(func() {
			w.reparse()
		})
	})
}

// MinSize returns the minimum size of the widget.
func (w *MarkdownWidget) MinSize() fyne.Size {
	w.ExtendBaseWidget(w)
	return w.BaseWidget.MinSize()
}

// ── Renderer ────────────────────────────────────────

type markdownRenderer struct {
	widget *MarkdownWidget
	rt     *widget.RichText
}

func (r *markdownRenderer) Destroy() {}

func (r *markdownRenderer) Layout(size fyne.Size) {
	r.rt.Resize(size)
}

func (r *markdownRenderer) MinSize() fyne.Size {
	return r.rt.MinSize()
}

func (r *markdownRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.rt}
}

func (r *markdownRenderer) Refresh() {
	r.widget.mu.Lock()
	segments := make([]widget.RichTextSegment, len(r.widget.segments))
	copy(segments, r.widget.segments)
	r.widget.mu.Unlock()

	r.rt.Segments = segments
	r.rt.Refresh()
	canvas := fyne.CurrentApp().Driver().CanvasForObject(r.widget)
	if canvas != nil {
		r.rt.Resize(r.rt.Size())
	}
}
