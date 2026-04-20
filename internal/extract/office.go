package extract

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/young2j/oxmltotext/docxtotext"
	"github.com/young2j/oxmltotext/pptxtotext"
	"github.com/young2j/oxmltotext/xlsxtotext"
)

// docxExtractor extracts text from DOCX files.
type docxExtractor struct{}

func (docxExtractor) Format() string { return "docx" }

func (docxExtractor) Extract(data []byte) (TextResult, error) {
	parser, err := docxtotext.OpenReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return TextResult{}, fmt.Errorf("open DOCX: %w", err)
	}
	defer parser.Close()

	text, err := parser.ExtractTexts()
	if err != nil {
		return TextResult{}, fmt.Errorf("extract DOCX text: %w", err)
	}

	return TextResult{
		Text:   strings.TrimSpace(text),
		Format: "docx",
	}, nil
}

// xlsxExtractor extracts text from XLSX files.
type xlsxExtractor struct{}

func (xlsxExtractor) Format() string { return "xlsx" }

func (xlsxExtractor) Extract(data []byte) (TextResult, error) {
	parser, err := xlsxtotext.OpenReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return TextResult{}, fmt.Errorf("open XLSX: %w", err)
	}
	defer parser.Close()

	text, err := parser.ExtractTexts()
	if err != nil {
		return TextResult{}, fmt.Errorf("extract XLSX text: %w", err)
	}

	return TextResult{
		Text:   strings.TrimSpace(text),
		Format: "xlsx",
	}, nil
}

// pptxExtractor extracts text from PPTX files.
type pptxExtractor struct{}

func (pptxExtractor) Format() string { return "pptx" }

func (pptxExtractor) Extract(data []byte) (TextResult, error) {
	parser, err := pptxtotext.OpenReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return TextResult{}, fmt.Errorf("open PPTX: %w", err)
	}
	defer parser.Close()

	text, err := parser.ExtractTexts()
	if err != nil {
		return TextResult{}, fmt.Errorf("extract PPTX text: %w", err)
	}

	return TextResult{
		Text:   strings.TrimSpace(text),
		Format: "pptx",
	}, nil
}
