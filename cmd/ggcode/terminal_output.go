package main

import (
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

func writeCLIText(w io.Writer, text string) (int, error) {
	if file, ok := w.(*os.File); ok && term.IsTerminal(int(file.Fd())) {
		text = normalizeTTYLineEndings(text)
	}
	return io.WriteString(w, text)
}

func normalizeTTYLineEndings(text string) string {
	var b strings.Builder
	b.Grow(len(text) + strings.Count(text, "\n"))
	for i := 0; i < len(text); i++ {
		if text[i] == '\n' {
			if i == 0 || text[i-1] != '\r' {
				b.WriteByte('\r')
			}
		}
		b.WriteByte(text[i])
	}
	return b.String()
}
