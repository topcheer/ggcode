package extract

import (
	"archive/zip"
	"bytes"
	"fmt"
	"github.com/topcheer/ggcode/internal/util"
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
	xmlEntries := 0
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
		xmlEntries++

		rc, err := f.Open()
		if err != nil {
			continue
		}
		content, err := util.ReadAll(rc, util.ReadLimitGeneral)
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

	// #566(G): iWork 2013+ stores body content in protobuf .iwa parts, which
	// we cannot parse. Reporting Text:"" with err:nil made such files look
	// like successfully-extracted empty documents. Say so explicitly
	// instead; .iwa-only archives are the common case for modern files.
	if buf.Len() == 0 {
		if xmlEntries == 0 {
			return TextResult{}, fmt.Errorf("%s archive contains no .xml parts (iWork 2013+ stores content in binary .iwa protobuf, which is unsupported)", e.subFormat)
		}
		return TextResult{}, fmt.Errorf("%s archive .xml parts contained no extractable text", e.subFormat)
	}

	return TextResult{
		Text:   strings.TrimSpace(buf.String()),
		Format: e.subFormat,
	}, nil
}
