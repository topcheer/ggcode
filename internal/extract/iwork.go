package extract

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"strings"
)

// iworkExtractor extracts text from Apple iWork files (.pages, .numbers, .key).
type iworkExtractor struct {
	subFormat string // "pages", "numbers", "key"
}

func (e *iworkExtractor) Format() string { return e.subFormat }

func (e *iworkExtractor) Extract(data []byte) (TextResult, error) {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return TextResult{}, fmt.Errorf("open %s archive: %w", e.subFormat, err)
	}

	var buf strings.Builder
	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}
		// iWork stores content in .iwa (protobuf) and sometimes .xml files.
		// The most accessible text is in .xml files.
		// For newer formats, content is in .iwa which is protoc-encoded.
		// We extract what we can from XML files.
		if !strings.HasSuffix(f.Name, ".xml") {
			continue
		}

		rc, err := f.Open()
		if err != nil {
			continue
		}
		content, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			continue
		}

		text := extractXMLText(string(content))
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
		Text:   strings.TrimSpace(buf.String()),
		Format: e.subFormat,
	}, nil
}
