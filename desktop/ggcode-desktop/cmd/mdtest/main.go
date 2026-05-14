package main

import (
	"fmt"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/topcheer/ggcode/desktop/markdownx"
)

func main() {
	a := app.New()
	w := a.NewWindow("MarkdownX Full Test")

	md := markdownx.NewMarkdownWidget()
	status := widget.NewLabel("Ready")

	scroll := container.NewVScroll(md)
	scroll.SetMinSize(fyne.NewSize(600, 500))

	// Test buttons for each element type.
	headingBtn := widget.NewButton("Headings", func() {
		md.SetMarkdown("# Heading 1\n\n## Heading 2\n\n### Heading 3\n\n#### Heading 4\n")
		status.SetText("Headings")
	})
	paraBtn := widget.NewButton("Paragraph", func() {
		md.SetMarkdown("This is a paragraph with **bold**, *italic*, and `code span` text.\n")
		status.SetText("Paragraph")
	})
	codeBtn := widget.NewButton("Code Block", func() {
		md.SetMarkdown("```go\npackage main\n\nimport \"fmt\"\n\nfunc main() {\n    fmt.Println(\"Hello\")\n}\n```\n")
		status.SetText("Code Block")
	})
	listBtn := widget.NewButton("List", func() {
		md.SetMarkdown("- Item one\n- Item two\n- Item three\n\n1. First\n2. Second\n3. Third\n")
		status.SetText("Lists")
	})
	tableBtn := widget.NewButton("Table", func() {
		md.SetMarkdown("| Name | Age | City |\n|------|-----|------|\n| Alice | 30 | NYC |\n| Bob | 25 | LA |\n| Charlie | 35 | SF |\n")
		status.SetText("Table")
	})
	quoteBtn := widget.NewButton("Blockquote", func() {
		md.SetMarkdown("> This is a quote\n> With multiple lines\n> That wraps around nicely\n")
		status.SetText("Blockquote")
	})
	hrBtn := widget.NewButton("HR", func() {
		md.SetMarkdown("Above\n\n---\n\nBelow\n")
		status.SetText("HR")
	})
	allBtn := widget.NewButton("All Elements", func() {
		md.SetMarkdown(allMarkdown)
		status.SetText("All Elements")
	})

	// Streaming tests.
	slowBtn := widget.NewButton("Slow Stream", func() {
		go slowStream(md, status)
	})
	fastBtn := widget.NewButton("Fast Stream", func() {
		go fastStream(md, status)
	})

	buttons := container.NewGridWithColumns(4,
		headingBtn, paraBtn, codeBtn, listBtn,
		tableBtn, quoteBtn, hrBtn, allBtn,
		slowBtn, fastBtn,
	)

	content := container.NewBorder(
		container.NewVBox(buttons, status),
		nil, nil, nil,
		scroll,
	)

	w.SetContent(content)
	w.Resize(fyne.NewSize(800, 700))
	w.ShowAndRun()
}

var allMarkdown = `# Full Markdown Test

## Text Formatting

Normal text with **bold**, *italic*, ` + "`code`" + `, and **_bold italic_**.

## Code Block

` + "```python" + `
def fibonacci(n):
    """Calculate the nth Fibonacci number."""
    if n <= 1:
        return n
    return fibonacci(n - 1) + fibonacci(n - 2)

for i in range(10):
    print(f"F({i}) = {fibonacci(i)}")
` + "```" + `

## Lists

- Bullet item 1
- Bullet item 2
- Bullet item 3

1. Numbered item 1
2. Numbered item 2
3. Numbered item 3

## Table

| Language | Year | Creator |
|----------|------|---------|
| Python | 1991 | Guido van Rossum |
| Go | 2009 | Rob Pike |
| Rust | 2010 | Graydon Hoare |

## Blockquote

> The best way to predict the future is to invent it.
> -- Alan Kay

---

End of test.`

// slowStream simulates slow streaming (one char at a time, 20ms delay).
func slowStream(md *markdownx.MarkdownWidget, status *widget.Label) {
	text := "# Slow Stream\n\nThis text arrives **slowly**.\n\n- Item 1\n- Item 2\n\n```\ncode block\nline 2\n```\n"
	for i := 1; i <= len(text); i++ {
		chunk := string([]rune(text)[:i])
		_ = chunk
		// Append just the new character.
		if i == 1 {
			fyne.Do(func() { md.SetMarkdown(string([]rune(text)[0])) })
		} else {
			fyne.Do(func() { md.AppendChunk(string([]rune(text)[i-1])) })
		}
		time.Sleep(20 * time.Millisecond)
	}
	fyne.Do(func() { status.SetText(fmt.Sprintf("Slow stream done (%d chars)", len(text))) })
}

// fastStream simulates fast streaming (whole words, 5ms delay).
func fastStream(md *markdownx.MarkdownWidget, status *widget.Label) {
	words := strings.Split(allMarkdown, " ")
	for i, word := range words {
		chunk := word
		if i > 0 {
			chunk = " " + chunk
		}
		fyne.Do(func() { md.AppendChunk(chunk) })
		time.Sleep(5 * time.Millisecond)
	}
	fyne.Do(func() { status.SetText(fmt.Sprintf("Fast stream done (%d words)", len(words))) })
}
