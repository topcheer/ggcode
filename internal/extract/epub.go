package extract

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"path"
	"strings"

	"github.com/topcheer/ggcode/internal/util"
	"golang.org/x/net/html"
)

type epubExtractor struct{}

func (epubExtractor) Format() string { return "epub" }

func (epubExtractor) Extract(data []byte) (TextResult, error) {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return TextResult{}, fmt.Errorf("open EPUB archive: %w", err)
	}

	opfPath, err := findOPFPath(r)
	if err != nil {
		return TextResult{}, err
	}

	spine, err := parseOPFSpine(r, opfPath)
	if err != nil {
		return TextResult{}, err
	}

	opfDir := ""
	if idx := strings.LastIndex(opfPath, "/"); idx >= 0 {
		opfDir = opfPath[:idx+1]
	}

	var buf strings.Builder
	chapters := 0
	missing := 0
	for _, itemPath := range spine {
		fullPath := resolvePath(opfDir, itemPath)
		text, err := extractHTMLFromZip(r, fullPath)
		if err != nil {
			missing++
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
		chapters++

		if buf.Len() > 50*1024*1024 {
			buf.WriteString("\n\n... (truncated at 50MB)")
			break
		}
	}

	if chapters == 0 {
		switch {
		case len(spine) == 0:
			return TextResult{}, fmt.Errorf("EPUB has an empty spine (no itemref entries in OPF)")
		case missing == len(spine):
			ex := spine[0]
			if len(spine) > 1 {
				ex = strings.Join(spine[:2], ", ")
			}
			return TextResult{}, fmt.Errorf("EPUB spine lists %d chapters but none exist in the archive (paths like %q - backslash or non-standard paths?)", len(spine), ex)
		default:
			return TextResult{}, fmt.Errorf("EPUB extracted no text from %d spine chapters (%d unreadable)", len(spine), missing)
		}
	}

	return TextResult{
		Text:   buf.String(),
		Pages:  chapters,
		Format: "epub",
	}, nil
}

func findOPFPath(r *zip.Reader) (string, error) {
	for _, f := range r.File {
		if f.Name == "META-INF/container.xml" {
			rc, err := f.Open()
			if err != nil {
				return "", fmt.Errorf("open container.xml: %w", err)
			}
			defer rc.Close()
			data, err := util.ReadAll(rc, util.ReadLimitGeneral)
			if err != nil {
				return "", fmt.Errorf("read container.xml: %w", err)
			}
			decoder := xml.NewDecoder(strings.NewReader(string(data)))
			for {
				token, err := decoder.Token()
				if err == io.EOF {
					break
				}
				if err != nil {

					return "", fmt.Errorf("parse container.xml: %w", err)
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

func parseOPFSpine(r *zip.Reader, opfPath string) ([]string, error) {
	var opfData []byte
	for _, f := range r.File {
		if f.Name == opfPath {
			rc, err := f.Open()
			if err != nil {
				return nil, fmt.Errorf("open OPF: %w", err)
			}
			defer rc.Close()
			opfData, err = util.ReadAll(rc, util.ReadLimitGeneral)
			if err != nil {
				return nil, fmt.Errorf("read OPF: %w", err)
			}
			break
		}
	}
	if opfData == nil {
		return nil, fmt.Errorf("OPF file not found: %s", opfPath)
	}

	manifest := make(map[string]string)

	var spineIDRefs []string

	decoder := xml.NewDecoder(strings.NewReader(string(opfData)))
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {

			return nil, fmt.Errorf("parse OPF: %w", err)
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

					if decoded, err := url.PathUnescape(href); err == nil {
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
					spineIDRefs = append(spineIDRefs, idref)
				}
			}
		}
	}

	result := make([]string, 0, len(spineIDRefs))
	for _, idref := range spineIDRefs {
		if href, ok := manifest[idref]; ok {
			result = append(result, href)
		}
	}
	return result, nil
}

func resolvePath(baseDir, relPath string) string {
	joined := relPath
	if !strings.HasPrefix(relPath, "/") {
		joined = baseDir + relPath
	}
	return strings.TrimPrefix(path.Clean(joined), "/")
}

func extractHTMLFromZip(r *zip.Reader, path string) (string, error) {
	for _, f := range r.File {
		if f.Name == path {
			rc, err := f.Open()
			if err != nil {
				return "", err
			}
			defer rc.Close()
			data, err := util.ReadAll(rc, util.ReadLimitGeneral)
			if err != nil {
				return "", err
			}
			return extractHTMLText(string(data)), nil
		}
	}

	lowerPath := strings.ToLower(path)
	for _, f := range r.File {
		if strings.ToLower(f.Name) == lowerPath {
			rc, err := f.Open()
			if err != nil {
				return "", err
			}
			defer rc.Close()
			data, err := util.ReadAll(rc, util.ReadLimitGeneral)
			if err != nil {
				return "", err
			}
			return extractHTMLText(string(data)), nil
		}
	}
	return "", fmt.Errorf("file not found in EPUB: %s", path)
}

func extractHTMLText(htmlContent string) string {
	doc, err := html.Parse(strings.NewReader(htmlContent))
	if err != nil {
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
			switch n.Data {
			case "script", "style", "head", "meta", "link":
				return
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				extract(c)
			}
			switch n.Data {
			case "p", "div", "h1", "h2", "h3", "h4", "h5", "h6",
				"li", "tr", "blockquote", "section", "article":
				buf.WriteByte('\n')
			case "br":
				buf.WriteByte('\n')
			}
		}
	}
	for c := doc.FirstChild; c != nil; c = c.NextSibling {
		extract(c)
	}
	return strings.TrimSpace(buf.String())
}

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
