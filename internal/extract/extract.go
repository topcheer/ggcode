// Package extract provides text extraction from binary document formats.
// Supported formats: PDF, Office (docx/xlsx/pptx), OpenDocument (odt/ods/odp),
// EPUB, and RTF.
package extract

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
)

// TextResult holds extracted text and metadata about the source document.
type TextResult struct {
	Text   string // extracted plain text
	Pages  int    // page/slide count (0 if not applicable)
	Format string // format name: "pdf", "docx", "xlsx", etc.
}

// Extractor extracts text from a binary document format.
type Extractor interface {
	Extract(data []byte) (TextResult, error)
	Format() string
}

// defaultRegistry is the global extractor registry.
var defaultRegistry = &Registry{
	extractors: make(map[string]Extractor),
}

func init() {
	// PDF
	defaultRegistry.Register(".pdf", &pdfExtractor{})
	// Office
	defaultRegistry.Register(".docx", &docxExtractor{})
	defaultRegistry.Register(".xlsx", &xlsxExtractor{})
	defaultRegistry.Register(".pptx", &pptxExtractor{})
	// OpenDocument
	defaultRegistry.Register(".odt", &odfExtractor{subFormat: "odt"})
	defaultRegistry.Register(".ods", &odfExtractor{subFormat: "ods"})
	defaultRegistry.Register(".odp", &odfExtractor{subFormat: "odp"})
	// EPUB
	defaultRegistry.Register(".epub", &epubExtractor{})
	// RTF
	defaultRegistry.Register(".rtf", &rtfExtractor{})
}

// Registry maps file extensions to Extractor instances.
type Registry struct {
	mu         sync.RWMutex
	extractors map[string]Extractor
}

// Register adds an extractor for the given extension (e.g. ".pdf").
func (r *Registry) Register(ext string, e Extractor) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.extractors[strings.ToLower(ext)] = e
}

// Get returns the extractor for the given extension, or nil.
func (r *Registry) Get(ext string) Extractor {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.extractors[strings.ToLower(ext)]
}

// Extract extracts text from data based on file extension.
// Returns an error if the format is not supported or extraction fails.
func Extract(path string, data []byte) (TextResult, error) {
	ext := strings.ToLower(filepath.Ext(path))
	e := defaultRegistry.Get(ext)
	if e == nil {
		return TextResult{}, fmt.Errorf("unsupported document format: %s", ext)
	}
	result, err := e.Extract(data)
	if err != nil {
		return TextResult{}, fmt.Errorf("extract %s: %w", ext, err)
	}
	return result, nil
}

// IsDocumentFile checks if a file path looks like a supported document format.
func IsDocumentFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return defaultRegistry.Get(ext) != nil
}
