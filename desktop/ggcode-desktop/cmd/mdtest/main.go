package main

import (
	"fmt"
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
	"github.com/topcheer/ggcode/desktop/markdownx"
)

func main() {
	a := app.New()
	w := a.NewWindow("MarkdownX Test")

	mdWidget := markdownx.NewMarkdownWidget()

	// Test content with all block types
	md := `# Heading 1

Normal paragraph with **bold** and *italic* and ` + "`code`" + `.

## Code Block

` + "```go" + `
package main

import "fmt"

func main() {
    fmt.Println("Hello")
}
` + "```" + `

## Unordered List

- Item one
- Item two
- Item three

## Ordered List

1. First
2. Second
3. Third

## Table

| Name | Age | City |
|------|-----|------|
| Alice | 30 | NYC |
| Bob | 25 | LA |

## Blockquote

> This is a quote
> Second line

## HR

---

End.`

	mdWidget.SetMarkdown(md)

	scroll := container.NewVScroll(mdWidget)
	scroll.SetMinSize(fyne.NewSize(600, 500))

	// Debug button to print widget state
	btn := widget.NewButton("Debug", func() {
		fmt.Printf("Widget size: %v\n", mdWidget.Size())
		fmt.Printf("Content length: %d\n", len(mdWidget.Content()))
	})

	content := container.NewBorder(btn, nil, nil, nil, scroll)
	w.SetContent(content)
	w.Resize(fyne.NewSize(700, 600))
	w.ShowAndRun()
}
