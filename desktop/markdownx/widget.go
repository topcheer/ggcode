package markdownx

import (
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

// MarkdownWidget renders Markdown as beautiful native Fyne widgets.
//
// Each block element (heading, code block, table, list, blockquote, etc.)
// is rendered as its own custom CanvasObject with full style control.
// Inline elements (bold, italic, code span, links) within paragraphs use
// properly styled TextSegments.
//
// Supports streaming input with debounced re-rendering.
type MarkdownWidget struct {
	widget.BaseWidget
	mu sync.Mutex

	buffer    strings.Builder
	renderers *nodeRenderers
	vbox      *fyne.Container

	debounceMu    sync.Mutex
	debounceTimer *time.Timer
}

func NewMarkdownWidget() *MarkdownWidget {
	w := &MarkdownWidget{
		renderers: newNodeRenderers(),
		vbox:      container.NewVBox(),
	}
	w.ExtendBaseWidget(w)
	return w
}

// SetMarkdown replaces content. Must be called on UI thread.
func (w *MarkdownWidget) SetMarkdown(text string) {
	w.mu.Lock()
	w.buffer.Reset()
	w.buffer.WriteString(text)
	w.mu.Unlock()
	w.rebuild()
}

// AppendChunk appends streaming text with debounced re-render.
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
	return widget.NewSimpleRenderer(w.vbox)
}

func (w *MarkdownWidget) rebuild() {
	w.mu.Lock()
	text := w.buffer.String()
	text = closeOpenCodeBlocks(text)
	w.mu.Unlock()

	objects := renderMarkdown(text, w.renderers)
	w.vbox.Objects = objects
	w.vbox.Refresh()
}

func (w *MarkdownWidget) scheduleDebouncedRebuild() {
	w.debounceMu.Lock()
	defer w.debounceMu.Unlock()
	if w.debounceTimer != nil {
		w.debounceTimer.Stop()
	}
	w.debounceTimer = time.AfterFunc(100*time.Millisecond, func() {
		fyne.Do(func() { w.rebuild() })
	})
}
