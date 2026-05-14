package markdownx

import (
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

// MarkdownWidget renders markdown with proper Fyne widgets.
// Uses VBox layout — no absolute positioning.
//
// Streaming: AppendChunk accumulates text and debounces re-render.
// Only the last block is rebuilt during streaming.
type MarkdownWidget struct {
	widget.BaseWidget
	mu sync.Mutex

	buffer    strings.Builder
	vbox      *fyne.Container
	lastCount int // number of blocks last rendered

	debounceMu    sync.Mutex
	debounceTimer *time.Timer
}

func NewMarkdownWidget() *MarkdownWidget {
	w := &MarkdownWidget{
		vbox: container.NewVBox(),
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
	w.fullRebuild()
}

// AppendChunk appends streaming text with debounced re-render.
func (w *MarkdownWidget) AppendChunk(chunk string) {
	w.mu.Lock()
	w.buffer.WriteString(chunk)
	w.mu.Unlock()
	w.scheduleRebuild()
}

// Content returns accumulated text.
func (w *MarkdownWidget) Content() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buffer.String()
}

// CreateRenderer returns a renderer that reports 0 min width so the
// parent layout controls width. Content wraps to fit whatever width it gets.
func (w *MarkdownWidget) CreateRenderer() fyne.WidgetRenderer {
	return &mdRenderer{widget: w}
}

// mdRenderer wraps the VBox and overrides MinSize to report 0 width.
type mdRenderer struct {
	widget *MarkdownWidget
}

func (r *mdRenderer) Destroy() {}

func (r *mdRenderer) Layout(size fyne.Size) {
	r.widget.vbox.Resize(size)
}

func (r *mdRenderer) MinSize() fyne.Size {
	ms := r.widget.vbox.MinSize()
	return fyne.NewSize(0, ms.Height)
}

func (r *mdRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.widget.vbox}
}

func (r *mdRenderer) Refresh() {
	r.widget.vbox.Refresh()
}

// fullRebuild re-parses and re-renders everything.
func (w *MarkdownWidget) fullRebuild() {
	w.mu.Lock()
	text := w.buffer.String()
	w.mu.Unlock()
	text = closeOpenCodeBlocks(text)

	blocks := parseBlocks(text)
	objs := make([]fyne.CanvasObject, 0, len(blocks))
	for _, b := range blocks {
		obj := renderBlock(b)
		if obj != nil {
			objs = append(objs, obj)
		}
	}

	w.vbox.Objects = objs
	w.lastCount = len(objs)
	w.vbox.Refresh()
}

// streamingRebuild only re-renders the last block for streaming.
func (w *MarkdownWidget) streamingRebuild() {
	w.mu.Lock()
	text := w.buffer.String()
	w.mu.Unlock()
	text = closeOpenCodeBlocks(text)

	blocks := parseBlocks(text)
	total := len(blocks)

	// If block count changed, do full rebuild.
	if total != w.lastCount {
		w.fullRebuild()
		return
	}

	// Only update last block.
	if total == 0 {
		return
	}
	lastBlock := blocks[total-1]
	obj := renderBlock(lastBlock)
	if obj == nil {
		return
	}

	if len(w.vbox.Objects) > 0 {
		w.vbox.Objects[len(w.vbox.Objects)-1] = obj
	} else {
		w.vbox.Objects = []fyne.CanvasObject{obj}
	}
	w.vbox.Refresh()
}

func (w *MarkdownWidget) scheduleRebuild() {
	w.debounceMu.Lock()
	defer w.debounceMu.Unlock()
	if w.debounceTimer != nil {
		w.debounceTimer.Stop()
	}
	// Fast streaming: 50ms debounce for responsiveness.
	// Slow streaming: same — the debounce ensures we don't render
	// more often than needed regardless of chunk frequency.
	w.debounceTimer = time.AfterFunc(50*time.Millisecond, func() {
		fyne.Do(func() {
			w.streamingRebuild()
		})
	})
}
