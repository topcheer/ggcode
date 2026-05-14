package main

import (
	"fmt"
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
		go tokenStream(md, status, 2000*time.Millisecond)
	})
	fastBtn := widget.NewButton("Fast Stream", func() {
		go tokenStream(md, status, 30*time.Millisecond)
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

## Nested List

- Level 1 item A
  - Level 2 sub-item 1
  - Level 2 sub-item 2
- Level 1 item B
  - Level 2 sub-item 3
    - Level 3 deep item
    - Level 3 another
  - Level 2 sub-item 4
- Level 1 item C

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

// tokenStream simulates realistic LLM streaming: tokens of 2-5 chars arrive one by one.
// delay controls interval between tokens (slow=2s, fast=30ms).
func tokenStream(md *markdownx.MarkdownWidget, status *widget.Label, delay time.Duration) {
	fullText := "# Streaming Test\n\nThis text arrives **token by token**, just like a real LLM.\n\n- First bullet\n- Second bullet\n- Third bullet\n\n```go\nfunc main() {\n    fmt.Println(\"hello world\")\n}\n```\n\n| Name | Age |\n|------|-----|\n| Alice | 30 |\n| Bob | 25 |\n\n> A quote that\n> spans lines\n\n---\n\nDone.\n"

	// Split into realistic token-sized chunks (2-5 chars).
	var tokens []string
	runes := []rune(fullText)
	i := 0
	for i < len(runes) {
		// Token length: 2-5 chars, but don't break mid-newline.
		n := 2 + (i % 4) // varies 2..5
		end := i + n
		if end > len(runes) {
			end = len(runes)
		}
		tokens = append(tokens, string(runes[i:end]))
		i = end
	}

	for idx, tok := range tokens {
		fyne.Do(func() {
			if idx == 0 {
				md.SetMarkdown(tok)
			} else {
				md.AppendChunk(tok)
			}
			status.SetText(fmt.Sprintf("Token %d/%d (%.0f%%)", idx+1, len(tokens),
				float64(idx+1)/float64(len(tokens))*100))
		})
		time.Sleep(delay)
	}
	fyne.Do(func() { status.SetText(fmt.Sprintf("Done: %d tokens", len(tokens))) })
}
