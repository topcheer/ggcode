package extract

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// odfExtractor extracts text from OpenDocument formats (.odt, .ods, .odp).
type odfExtractor struct {
	subFormat string // "odt", "ods", or "odp"
}

func (e *odfExtractor) Format() string { return e.subFormat }

func (e *odfExtractor) Extract(data []byte) (TextResult, error) {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return TextResult{}, fmt.Errorf("open ODF archive: %w", err)
	}

	// Find content.xml
	var contentFile *zip.File
	for _, f := range r.File {
		if f.Name == "content.xml" {
			contentFile = f
			break
		}
	}
	if contentFile == nil {
		return TextResult{}, fmt.Errorf("ODF archive missing content.xml")
	}

	rc, err := contentFile.Open()
	if err != nil {
		return TextResult{}, fmt.Errorf("open content.xml: %w", err)
	}
	defer rc.Close()

	content, err := io.ReadAll(rc)
	if err != nil {
		return TextResult{}, fmt.Errorf("read content.xml: %w", err)
	}

	text := extractXMLText(string(content))
	pages := 0
	// Count page breaks for ODT
	if e.subFormat == "odt" {
		pages = strings.Count(text, "\n\n") + 1
	}

	return TextResult{
		Text:   strings.TrimSpace(text),
		Pages:  pages,
		Format: e.subFormat,
	}, nil
}

// extractXMLText walks XML tokens and returns all character data content.
// It inserts newlines for block-level elements (p, h1-h6, table-row, etc.)
// and tabs for table cells.
func extractXMLText(xmlContent string) string {
	decoder := xml.NewDecoder(strings.NewReader(xmlContent))
	var buf strings.Builder

	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}

		switch t := token.(type) {
		case xml.CharData:
			s := strings.TrimSpace(string(t))
			if s != "" {
				buf.WriteString(s)
			}
		case xml.StartElement:
			switch t.Name.Local {
			case "p", "h", "h1", "h2", "h3", "h4", "h5", "h6":
				if buf.Len() > 0 {
					buf.WriteByte('\n')
				}
			case "table-row", "tr":
				if buf.Len() > 0 {
					buf.WriteByte('\n')
				}
			case "table-cell", "td", "th":
				if buf.Len() > 0 {
					buf.WriteByte('\t')
				}
			case "line-break", "br":
				buf.WriteByte('\n')
			case "tab":
				buf.WriteByte('\t')
			case "page-break":
				buf.WriteString("\n---\n")
			}
		}
	}

	return buf.String()
}
