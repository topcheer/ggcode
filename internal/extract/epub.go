package extract

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"

	"golang.org/x/net/html"
)

// epubExtractor extracts text from EPUB files.
type epubExtractor struct{}

func (epubExtractor) Format() string { return "epub" }

func (epubExtractor) Extract(data []byte) (TextResult, error) {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return TextResult{}, fmt.Errorf("open EPUB archive: %w", err)
	}

	// 1. Parse META-INF/container.xml to find the OPF path
	opfPath, err := findOPFPath(r)
	if err != nil {
		return TextResult{}, err
	}

	// 2. Parse the OPF to get spine order
	spine, err := parseOPFSpine(r, opfPath)
	if err != nil {
		return TextResult{}, err
	}

	// 3. Extract text from each spine item in order
	opfDir := ""
	if idx := strings.LastIndex(opfPath, "/"); idx >= 0 {
		opfDir = opfPath[:idx+1]
	}

	var buf strings.Builder
	chapters := 0
	for _, itemPath := range spine {
		fullPath := resolvePath(opfDir, itemPath)
		text, err := extractHTMLFromZip(r, fullPath)
		if err != nil {
			continue // skip unreadable chapters
		}
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		if buf.Len() > 0 {
			buf.WriteString("\n\n")
		}
		buf.WriteString(text)
		chapters++
	}

	return TextResult{
		Text:   buf.String(),
		Pages:  chapters,
		Format: "epub",
	}, nil
}

// findOPFPath parses META-INF/container.xml to find the OPF file path.
func findOPFPath(r *zip.Reader) (string, error) {
	for _, f := range r.File {
		if f.Name == "META-INF/container.xml" {
			rc, err := f.Open()
			if err != nil {
				return "", fmt.Errorf("open container.xml: %w", err)
			}
			defer rc.Close()
			data, err := io.ReadAll(rc)
			if err != nil {
				return "", fmt.Errorf("read container.xml: %w", err)
			}
			// Simple extraction: find rootfile full-path attribute
			decoder := xml.NewDecoder(strings.NewReader(string(data)))
			for {
				token, err := decoder.Token()
				if err == io.EOF {
					break
				}
				if err != nil {
					break
				}
				if se, ok := token.(xml.StartElement); ok {
					if se.Name.Local == "rootfile" {
						for _, attr := range se.Attr {
							if attr.Name.Local == "full-path" {
								return attr.Value, nil
							}
						}
					}
				}
			}
		}
	}
	return "", fmt.Errorf("EPUB missing META-INF/container.xml or rootfile")
}

// spineItem holds a spine item with its linear order.
type spineItem struct {
	idref string
	order int
}

// parseOPFSpine reads the OPF file and returns content document paths in spine order.
func parseOPFSpine(r *zip.Reader, opfPath string) ([]string, error) {
	// Read the OPF file
	var opfData []byte
	for _, f := range r.File {
		if f.Name == opfPath {
			rc, err := f.Open()
			if err != nil {
				return nil, fmt.Errorf("open OPF: %w", err)
			}
			defer rc.Close()
			opfData, err = io.ReadAll(rc)
			if err != nil {
				return nil, fmt.Errorf("read OPF: %w", err)
			}
			break
		}
	}
	if opfData == nil {
		return nil, fmt.Errorf("OPF file not found: %s", opfPath)
	}

	// Parse manifest: id → href
	manifest := make(map[string]string)
	// Parse spine: ordered list of idrefs
	var spineItems []spineItem
	spineOrder := 0

	decoder := xml.NewDecoder(strings.NewReader(string(opfData)))
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}
		if se, ok := token.(xml.StartElement); ok {
			if se.Name.Local == "item" {
				var id, href string
				for _, attr := range se.Attr {
					switch attr.Name.Local {
					case "id":
						id = attr.Value
					case "href":
						href = attr.Value
					}
				}
				if id != "" && href != "" {
					// URL decode the href
					if decoded, err := url.QueryUnescape(href); err == nil {
						href = decoded
					}
					manifest[id] = href
				}
			} else if se.Name.Local == "itemref" {
				var idref string
				for _, attr := range se.Attr {
					if attr.Name.Local == "idref" {
						idref = attr.Value
					}
				}
				if idref != "" {
					spineItems = append(spineItems, spineItem{idref: idref, order: spineOrder})
					spineOrder++
				}
			}
		}
	}

	// Resolve spine items to paths
	result := make([]string, 0, len(spineItems))
	for _, si := range spineItems {
		if href, ok := manifest[si.idref]; ok {
			result = append(result, href)
		}
	}

	// Sort by order (they should already be in order, but be safe)
	sort.Slice(result, func(i, j int) bool {
		return spineItems[i].order < spineItems[j].order
	})

	_ = manifest // use manifest for lookups above
	return result, nil
}

// resolvePath resolves a relative path against a base directory within the ZIP.
func resolvePath(baseDir, relPath string) string {
	if strings.HasPrefix(relPath, "/") {
		return relPath[1:]
	}
	return baseDir + relPath
}

// extractHTMLFromZip reads an HTML/XHTML file from the ZIP and extracts text.
func extractHTMLFromZip(r *zip.Reader, path string) (string, error) {
	for _, f := range r.File {
		if f.Name == path {
			rc, err := f.Open()
			if err != nil {
				return "", err
			}
			defer rc.Close()
			data, err := io.ReadAll(rc)
			if err != nil {
				return "", err
			}
			return extractHTMLText(string(data)), nil
		}
	}
	// Try case-insensitive match as fallback
	lowerPath := strings.ToLower(path)
	for _, f := range r.File {
		if strings.ToLower(f.Name) == lowerPath {
			rc, err := f.Open()
			if err != nil {
				return "", err
			}
			defer rc.Close()
			data, err := io.ReadAll(rc)
			if err != nil {
				return "", err
			}
			return extractHTMLText(string(data)), nil
		}
	}
	return "", fmt.Errorf("file not found in EPUB: %s", path)
}

// extractHTMLText extracts visible text from HTML content.
func extractHTMLText(htmlContent string) string {
	doc, err := html.Parse(strings.NewReader(htmlContent))
	if err != nil {
		// Fallback: strip tags naively
		return stripTags(htmlContent)
	}

	var buf bytes.Buffer
	var extract func(*html.Node)
	extract = func(n *html.Node) {
		if n.Type == html.TextNode {
			text := strings.TrimSpace(n.Data)
			if text != "" {
				if buf.Len() > 0 && !bytes.HasSuffix(buf.Bytes(), []byte("\n")) {
					buf.WriteByte(' ')
				}
				buf.WriteString(text)
			}
		} else if n.Type == html.ElementNode {
			// Skip script, style, head
			switch n.Data {
			case "script", "style", "head", "meta", "link":
				return
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				extract(c)
			}
			// Block elements get a newline
			switch n.Data {
			case "p", "div", "h1", "h2", "h3", "h4", "h5", "h6",
				"li", "tr", "blockquote", "section", "article":
				buf.WriteByte('\n')
			case "br":
				buf.WriteByte('\n')
			}
		}
	}
	extract(doc)
	return strings.TrimSpace(buf.String())
}

// stripTags is a naive HTML tag stripper fallback.
func stripTags(s string) string {
	var buf strings.Builder
	inTag := false
	for _, r := range s {
		if r == '<' {
			inTag = true
			continue
		}
		if r == '>' {
			inTag = false
			continue
		}
		if !inTag {
			buf.WriteRune(r)
		}
	}
	return strings.TrimSpace(buf.String())
}
