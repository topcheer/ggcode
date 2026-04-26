package extract

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/ledongthuc/pdf"
)

// pdfExtractor extracts text from PDF files.
type pdfExtractor struct{}

func (pdfExtractor) Format() string { return "pdf" }

func (pdfExtractor) Extract(data []byte) (TextResult, error) {
	reader, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return TextResult{}, fmt.Errorf("open PDF: %w", err)
	}

	numPages := reader.NumPage()
	var buf strings.Builder

	for i := 1; i <= numPages; i++ {
		page := reader.Page(i)
		if page.V.IsNull() {
			continue
		}
		text, err := page.GetPlainText(nil)
		if err != nil {
			continue
		}
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		if buf.Len() > 0 {
			buf.WriteString("\n\n")
		}
		buf.WriteString(text)
	}

	return TextResult{
		Text:   buf.String(),
		Pages:  numPages,
		Format: "pdf",
	}, nil
}
