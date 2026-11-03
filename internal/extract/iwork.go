package extract

import (
	"archive/zip"
	"bytes"
	"fmt"
	"github.com/topcheer/ggcode/internal/util"
	"strings"
)

type iworkExtractor struct {
	subFormat string
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
