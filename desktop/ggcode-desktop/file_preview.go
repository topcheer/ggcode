package main

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	chromaLexers "github.com/alecthomas/chroma/v2/lexers"
)

// imageExts lists file extensions that should be previewed as images.
var imageExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true,
	".svg": true, ".webp": true, ".bmp": true, ".ico": true,
}

// markdownExts lists file extensions rendered as Markdown.
var markdownExts = map[string]bool{
	".md": true, ".markdown": true, ".mdown": true, ".mkd": true,
}

// maxPreviewSize is the maximum file size to read for preview (1 MB).
const maxPreviewSize = 1 << 20

// FilePreview shows a read-only preview of a file in the main content area.
type FilePreview struct {
	app      *App
	filePath string
	scroll   *container.Scroll
	onClose  func()
}

// NewFilePreview creates a new file preview for the given path.
func NewFilePreview(app *App, filePath string, targetLine int, onClose func()) *FilePreview {
	fp := &FilePreview{
		app:      app,
		filePath: filePath,
		onClose:  onClose,
	}
	content := fp.build(targetLine)
	fp.scroll = container.NewScroll(content)
	return fp
}

// Widget returns the Fyne canvas object for this preview.
func (fp *FilePreview) Widget() fyne.CanvasObject {
	// Header bar with filename and close button
	relPath := fp.filePath
	if fp.app.dc != nil && fp.app.dc.WorkDir != "" {
		if rel, err := filepath.Rel(fp.app.dc.WorkDir, fp.filePath); err == nil {
			relPath = rel
		}
	}

	closeBtn := widget.NewButtonWithIcon("", theme.CancelIcon(), func() {
		if fp.onClose != nil {
			fp.onClose()
		}
	})

	header := container.NewHBox(
		widget.NewIcon(theme.FileIcon()),
		widget.NewLabelWithStyle(relPath, fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		layout.NewSpacer(),
		closeBtn,
	)

	return container.NewBorder(header, nil, nil, nil, fp.scroll)
}

// build creates the preview content based on file type.
func (fp *FilePreview) build(targetLine int) fyne.CanvasObject {
	ext := strings.ToLower(filepath.Ext(fp.filePath))

	// Image preview
	if imageExts[ext] {
		return fp.buildImagePreview()
	}

	// Read file content
	info, err := os.Stat(fp.filePath)
	if err != nil {
		return fp.buildError(fmt.Sprintf("Cannot access file: %v", err))
	}
	if info.IsDir() {
		return fp.buildError("Cannot preview a directory")
	}
	if info.Size() > maxPreviewSize {
		return fp.buildError(fmt.Sprintf("File too large (%.1f MB)", float64(info.Size())/(1<<20)))
	}

	data, err := os.ReadFile(fp.filePath)
	if err != nil {
		return fp.buildError(fmt.Sprintf("Cannot read file: %v", err))
	}

	// Binary check
	if isBinaryData(data) {
		return fp.buildBinaryInfo(info)
	}

	content := strings.ReplaceAll(string(data), "\r\n", "\n")

	// Markdown preview
	if markdownExts[ext] {
		return fp.buildMarkdownPreview(content)
	}

	// Syntax highlighted code
	return fp.buildCodePreview(fp.filePath, content, targetLine)
}

// buildImagePreview shows an image file.
func (fp *FilePreview) buildImagePreview() fyne.CanvasObject {
	file, err := os.Open(fp.filePath)
	if err != nil {
		return fp.buildError(fmt.Sprintf("Cannot open image: %v", err))
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, 10<<20)) // max 10MB for images
	if err != nil {
		return fp.buildError(fmt.Sprintf("Cannot read image: %v", err))
	}

	img := &canvas.Image{}
	img.Resource = fyne.NewStaticResource(filepath.Base(fp.filePath), data)
	img.FillMode = canvas.ImageFillContain
	img.SetMinSize(fyne.NewSize(400, 300))

	// Image info
	info, _ := os.Stat(fp.filePath)
	sizeStr := ""
	if info != nil {
		sizeStr = formatSize(info.Size())
	}
	infoLabel := widget.NewLabel(fmt.Sprintf("%s (%s)", filepath.Base(fp.filePath), sizeStr))
	infoLabel.Alignment = fyne.TextAlignCenter

	return container.NewVBox(
		layout.NewSpacer(),
		container.NewCenter(img),
		container.NewCenter(infoLabel),
		layout.NewSpacer(),
	)
}

// buildCodePreview shows source code in a read-only Entry with line numbers.
func (fp *FilePreview) buildCodePreview(path, content string, targetLine int) fyne.CanvasObject {
	// Build line-numbered content
	lines := strings.Split(content, "\n")
	// Remove trailing empty line from final newline
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	maxLine := len(lines)
	lineNumWidth := len(fmt.Sprintf("%d", maxLine))
	fmtStr := fmt.Sprintf("%%%dd  %%s", lineNumWidth)

	var numbered strings.Builder
	for i, line := range lines {
		numbered.WriteString(fmt.Sprintf(fmtStr, i+1, line))
		if i < len(lines)-1 {
			numbered.WriteByte('\n')
		}
	}

	entry := widget.NewEntry()
	entry.MultiLine = true
	entry.Wrapping = fyne.TextWrapOff
	entry.TextStyle = fyne.TextStyle{Monospace: true}
	entry.SetText(numbered.String())
	entry.Disable() // read-only

	// If target line specified, scroll to it after layout
	if targetLine > 0 && targetLine <= len(lines) {
		go func() {
			time.Sleep(100 * time.Millisecond)
			fyne.Do(func() {
				// Calculate scroll position based on line height
				lineHeight := theme.TextSize()
				offset := float32(targetLine-1) * lineHeight * 1.5
				if scroll := fp.findScroll(); scroll != nil {
					scroll.Offset.Y = offset
					scroll.Refresh()
				}
			})
		}()
	}

	return entry
}

// findScroll walks up to find the parent Scroll container.
func (fp *FilePreview) findScroll() *container.Scroll {
	if fp.scroll != nil {
		return fp.scroll
	}
	return nil
}

// buildMarkdownPreview renders Markdown as rich text, with Mermaid diagram support.
func (fp *FilePreview) buildMarkdownPreview(content string) fyne.CanvasObject {
	// Split content by mermaid code blocks
	parts := splitMarkdownAndMermaid(content)

	var objects []fyne.CanvasObject
	for _, part := range parts {
		if part.isMermaid {
			objects = append(objects, fp.buildMermaidDiagram(part.content))
		} else {
			md := newMD(part.content)
			objects = append(objects, md)
		}
	}

	return container.NewVBox(objects...)
}

// mermaidPart represents a section of markdown content (either text or mermaid).
type mermaidPart struct {
	content   string
	isMermaid bool
}

// splitMarkdownAndMermaid splits markdown content into text and mermaid blocks.
var mermaidBlockRe = regexp.MustCompile("(?s)```mermaid\\s*\n(.*?)```")

func splitMarkdownAndMermaid(content string) []mermaidPart {
	var parts []mermaidPart
	lastEnd := 0
	for _, m := range mermaidBlockRe.FindAllStringSubmatchIndex(content, -1) {
		// Text before the mermaid block
		if m[0] > lastEnd {
			text := strings.TrimSpace(content[lastEnd:m[0]])
			if text != "" {
				parts = append(parts, mermaidPart{content: text, isMermaid: false})
			}
		}
		// Mermaid block content
		mermaidContent := content[m[2]:m[3]]
		parts = append(parts, mermaidPart{content: strings.TrimSpace(mermaidContent), isMermaid: true})
		lastEnd = m[1]
	}
	// Remaining text after last mermaid block
	if lastEnd < len(content) {
		text := strings.TrimSpace(content[lastEnd:])
		if text != "" {
			parts = append(parts, mermaidPart{content: text, isMermaid: false})
		}
	}
	if len(parts) == 0 {
		parts = append(parts, mermaidPart{content: content, isMermaid: false})
	}
	return parts
}

// buildMermaidDiagram renders a mermaid diagram by fetching from mermaid.ink API.
func (fp *FilePreview) buildMermaidDiagram(mermaidCode string) fyne.CanvasObject {
	placeholder := widget.NewLabel("Loading diagram...")
	placeholder.Alignment = fyne.TextAlignCenter

	img := &canvas.Image{}
	img.FillMode = canvas.ImageFillContain
	img.SetMinSize(fyne.NewSize(400, 300))
	img.Hide()

	wrapper := container.NewStack(placeholder)

	// Fetch diagram in background
	go func() {
		svgData, err := fetchMermaidSVG(mermaidCode)
		if err != nil {
			fyne.Do(func() {
				placeholder.SetText(fmt.Sprintf("Failed to render diagram: %v\n\n%s", err, mermaidCode))
			})
			return
		}
		img.Resource = fyne.NewStaticResource("mermaid.svg", svgData)
		fyne.Do(func() {
			placeholder.Hide()
			img.Show()
			img.Refresh()
			wrapper.Refresh()
		})
	}()

	wrapper.Objects = append(wrapper.Objects, img)
	return container.NewCenter(wrapper)
}

// fetchMermaidSVG fetches an SVG rendering of a mermaid diagram from the mermaid.ink API.
func fetchMermaidSVG(code string) ([]byte, error) {
	encoded := base64.StdEncoding.EncodeToString([]byte(code))
	url := "https://mermaid.ink/img/" + encoded + "?type=svg"

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("mermaid.ink returned status %d", resp.StatusCode)
	}

	return io.ReadAll(io.LimitReader(resp.Body, 5<<20)) // max 5MB SVG
}

// buildError shows an error message.
func (fp *FilePreview) buildError(msg string) fyne.CanvasObject {
	label := widget.NewLabelWithStyle(msg, fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	return container.NewCenter(label)
}

// buildBinaryInfo shows information about a binary file.
func (fp *FilePreview) buildBinaryInfo(info os.FileInfo) fyne.CanvasObject {
	infoLines := []string{
		fmt.Sprintf("File: %s", filepath.Base(fp.filePath)),
		fmt.Sprintf("Size: %s", formatSize(info.Size())),
		fmt.Sprintf("Type: Binary file"),
		fmt.Sprintf("Modified: %s", info.ModTime().Format("2006-01-02 15:04:05")),
	}
	label := widget.NewLabel(strings.Join(infoLines, "\n"))
	label.TextStyle = fyne.TextStyle{Monospace: true}
	return container.NewCenter(label)
}

// highlightCode uses Chroma to syntax-highlight code and returns one string per line.
func highlightCode(path, content string) []string {
	lexer := chromaLexers.Match(path)
	if lexer == nil {
		lexer = chromaLexers.Fallback
	}
	iterator, err := lexer.Tokenise(nil, content)
	if err != nil {
		return strings.Split(content, "\n")
	}

	// Chroma tokenizer already handles the lexing; for plain text display
	// we just split the original content by lines.
	// Future: implement a Fyne widget that uses Chroma tokens for per-token coloring.
	_ = iterator
	return strings.Split(content, "\n")
}

// isBinaryData checks if data appears to be binary.
func isBinaryData(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	// Check first 512 bytes for null bytes
	checkLen := len(data)
	if checkLen > 512 {
		checkLen = 512
	}
	for _, b := range data[:checkLen] {
		if b == 0 {
			return true
		}
	}
	return false
}

// formatSize formats a file size in human-readable form.
func formatSize(bytes int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)
	switch {
	case bytes >= GB:
		return fmt.Sprintf("%.1f GB", float64(bytes)/float64(GB))
	case bytes >= MB:
		return fmt.Sprintf("%.1f MB", float64(bytes)/float64(MB))
	case bytes >= KB:
		return fmt.Sprintf("%.1f KB", float64(bytes)/float64(KB))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}
