package extract

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
)

type TextResult struct {
	Text   string
	Pages  int
	Format string
}

type Extractor interface {
	Extract(data []byte) (TextResult, error)
	Format() string
}

var defaultRegistry = &Registry{
	extractors: make(map[string]Extractor),
}

func init() {

	defaultRegistry.Register(".pdf", &pdfExtractor{})

	defaultRegistry.Register(".docx", &docxExtractor{})
	defaultRegistry.Register(".xlsx", &xlsxExtractor{})
	defaultRegistry.Register(".pptx", &pptxExtractor{})

	defaultRegistry.Register(".docm", &docxExtractor{})
	defaultRegistry.Register(".xlsm", &xlsxExtractor{})
	defaultRegistry.Register(".pptm", &pptxExtractor{})

	defaultRegistry.Register(".dotx", &docxExtractor{})
	defaultRegistry.Register(".dotm", &docxExtractor{})
	defaultRegistry.Register(".xltx", &xlsxExtractor{})
	defaultRegistry.Register(".xltm", &xlsxExtractor{})
	defaultRegistry.Register(".xlam", &xlsxExtractor{})
	defaultRegistry.Register(".potx", &pptxExtractor{})
	defaultRegistry.Register(".potm", &pptxExtractor{})

	defaultRegistry.Register(".odt", &odfExtractor{subFormat: "odt"})
	defaultRegistry.Register(".ods", &odfExtractor{subFormat: "ods"})
	defaultRegistry.Register(".odp", &odfExtractor{subFormat: "odp"})

	defaultRegistry.Register(".epub", &epubExtractor{})

	defaultRegistry.Register(".rtf", &rtfExtractor{})

	defaultRegistry.Register(".zip", &archiveExtractor{subFormat: "zip"})
	defaultRegistry.Register(".tar", &archiveExtractor{subFormat: "tar"})
	defaultRegistry.Register(".tar.gz", &archiveExtractor{subFormat: "tar.gz"})
	defaultRegistry.Register(".tgz", &archiveExtractor{subFormat: "tar.gz"})
	defaultRegistry.Register(".tar.bz2", &archiveExtractor{subFormat: "tar.bz2"})

	defaultRegistry.Register(".pages", &iworkExtractor{subFormat: "pages"})
	defaultRegistry.Register(".numbers", &iworkExtractor{subFormat: "numbers"})
	defaultRegistry.Register(".key", &iworkExtractor{subFormat: "key"})

	defaultRegistry.Register(".svg", &svgExtractor{})
}

type Registry struct {
	mu         sync.RWMutex
	extractors map[string]Extractor
}

func (r *Registry) Register(ext string, e Extractor) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.extractors[strings.ToLower(ext)] = e
}

func (r *Registry) Get(ext string) Extractor {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.extractors[strings.ToLower(ext)]
}

func extOf(path string) string {
	name := strings.ToLower(filepath.Base(path))
	for _, double := range []string{".tar.gz", ".tar.bz2", ".tar.xz"} {
		if strings.HasSuffix(name, double) {
			return double
		}
	}
	return strings.ToLower(filepath.Ext(path))
}

func Extract(path string, data []byte) (TextResult, error) {
	ext := extOf(path)
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

func IsDocumentFile(path string) bool {
	ext := extOf(path)
	return defaultRegistry.Get(ext) != nil
}
