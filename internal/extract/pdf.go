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
			// #1729 case 2: a null page object vanished silently while
			// Pages still reported the total - the page-level twin of the
			// masquerade #1542 fixed for corrupt pages (#686/#682 family).
			fmt.Fprintf(&buf, "\n[page %d empty: page object missing]", i)
			continue
		}
		text, err := page.GetPlainText(nil)
		if err != nil {
			// #1542: a corrupt page silently vanished - a 50-page doc became
			// 49 with no page number and no marker while Pages still reported
			// the total, the exact masquerade svg/tar/zip already fixed
			// (#686/#682). Flag it honestly.
			// #1729 case 1: ledongthuc/pdf's Page(n) takes 1-BASED numbers
			// (page.go num-- comment) - i is already the true page number;
			// the old i+1 reported page numPages+1 for a corrupt LAST page.
			fmt.Fprintf(&buf, "\n[page %d unreadable: %v]", i, err)
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
