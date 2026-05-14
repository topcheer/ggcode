package markdownx

import (
	"strings"
	"sync"
	"time"

	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/widget"
)

// MarkdownWidget renders Markdown using raw canvas.Text and canvas.Rectangle.
// No Fyne RichText or segment system — full control over layout and style.
//
// Designed for streaming LLM output: AppendChunk accumulates text and
// debounces re-renders. Only the last changed block is re-rendered.
type MarkdownWidget struct {
	widget.BaseWidget
	mu sync.Mutex

	buffer    strings.Builder
	vbox      *fyne.Container
	lastWidth float32

	// Parsed blocks (cached for streaming optimization).
	blocks    []block
	blockHash []string // hash per block for change detection

	// Streaming debounce.
	debounceMu    sync.Mutex
	debounceTimer *time.Timer
}

func NewMarkdownWidget() *MarkdownWidget {
	w := &MarkdownWidget{
		vbox:      fyne.NewContainerWithLayout(newBlockLayout()),
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

// Content returns the current accumulated markdown text.
func (w *MarkdownWidget) Content() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buffer.String()
}

// CreateRenderer implements fyne.Widget.
func (w *MarkdownWidget) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(w.vbox)
}

// rebuild parses the buffer and re-renders all blocks.
func (w *MarkdownWidget) rebuild() {
	w.mu.Lock()
	text := w.buffer.String()
	text = closeOpenCodeBlocks(text)
	w.mu.Unlock()

	newBlocks := parseBlocks(text)

	// Compute hashes for change detection.
	newHashes := make([]string, len(newBlocks))
	for i, b := range newBlocks {
		newHashes[i] = blockHashStr(b)
	}

	// Determine width (use cached or default).
	width := w.lastWidth
	if width <= 0 {
		width = 600
	}

	// Render all blocks, accumulating Y positions.
	var allObjects []fyne.CanvasObject
	y := float32(0)

	for _, b := range newBlocks {
		objs, h := b.render(width)
		// Offset all objects by current Y.
		for _, obj := range objs {
			switch v := obj.(type) {
			case *canvas.Text:
				v.Move(fyne.NewPos(v.Position().X, v.Position().Y+y))
			case *canvas.Rectangle:
				v.Move(fyne.NewPos(v.Position().X, v.Position().Y+y))
			}
			allObjects = append(allObjects, obj)
		}
		y += h
	}

	// Background.
	bg := canvas.NewRectangle(color.RGBA{R: 30, G: 30, B: 30, A: 0}) // transparent
	bg.Resize(fyne.NewSize(width, y))

	// Set objects: background first, then all block objects.
	w.vbox.Objects = append([]fyne.CanvasObject{bg}, allObjects...)
	w.vbox.Refresh()

	w.blocks = newBlocks
	w.blockHash = newHashes
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

// blockHashStr returns a quick hash string for a block (for change detection).
func blockHashStr(b block) string {
	switch v := b.(type) {
	case *headingBlock:
		return "h" + strings.Join(runsToHash(v.runs), "|")
	case *paragraphBlock:
		return "p" + strings.Join(runsToHash(v.runs), "|")
	case *codeBlock:
		return "c" + strings.Join(v.lines, "\n")
	case *listBlock:
		return "l"
	case *blockquoteBlock:
		return "q"
	case *tableBlock:
		return "t"
	case *hrBlock:
		return "hr"
	}
	return "?"
}

func runsToHash(runs []textRun) []string {
	var s []string
	for _, r := range runs {
		s = append(s, r.text)
	}
	return s
}

// ── blockLayout ────────────────────────────────────
// A simple layout that stacks objects vertically with no padding.
// Objects are positioned by their Move() calls already.

type blockLayout struct{}

func newBlockLayout() *blockLayout { return &blockLayout{} }

func (l *blockLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	// Objects are already positioned. Just resize backgrounds.
	for _, obj := range objects {
		if r, ok := obj.(*canvas.Rectangle); ok {
			r.Resize(size)
		}
	}
}

func (l *blockLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	// Calculate total height from object positions.
	var maxW, maxH float32
	for _, obj := range objects {
		pos := obj.Position()
		size := obj.MinSize()
		if pos.X+size.Width > maxW {
			maxW = pos.X + size.Width
		}
		if pos.Y+size.Height > maxH {
			maxH = pos.Y + size.Height
		}
	}
	return fyne.NewSize(maxW, maxH)
}
