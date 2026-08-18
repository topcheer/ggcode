package extract

import (
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// svgTextElements is the whitelist of SVG elements whose character data is
// extracted as visible text. #682: previously ALL CharData was collected with
// no parent-element filter, so <script>/<style> JS/CSS leaked into the
// extracted text — the same function already applied a strict element
// whitelist to aria-label attributes.
func svgTextElement(name string) bool {
	switch name {
	case "text", "tspan", "title", "desc":
		return true
	}
	return false
}

// svgExtractor extracts text from SVG files.
type svgExtractor struct{}

func (svgExtractor) Format() string { return "svg" }

func (svgExtractor) Extract(data []byte) (TextResult, error) {
	var buf strings.Builder
	decoder := xml.NewDecoder(strings.NewReader(string(data)))
	var stack []string
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			// #682: decode errors (undefined entities, bare &, illegal UTF-8)
			// were silently swallowed and partial text returned as success —
			// indistinguishable from a complete extraction. Propagate now.
			return TextResult{}, fmt.Errorf("decode svg: %w", err)
		}
		switch t := token.(type) {
		case xml.StartElement:
			stack = append(stack, t.Name.Local)
			if svgTextElement(t.Name.Local) {
				// Check for aria-label attribute
				for _, attr := range t.Attr {
					if attr.Name.Local == "aria-label" && attr.Value != "" {
						if buf.Len() > 0 {
							buf.WriteByte('\n')
						}
						buf.WriteString(attr.Value)
					}
				}
			}
		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		case xml.CharData:
			if len(stack) == 0 || !svgTextElement(stack[len(stack)-1]) {
				continue // skip script/style/other non-text element content
			}
			text := strings.TrimSpace(string(t))
			if text != "" {
				if buf.Len() > 0 {
					buf.WriteByte(' ')
				}
				buf.WriteString(text)
			}
		}
	}

	return TextResult{
		Text:   strings.TrimSpace(buf.String()),
		Format: "svg",
	}, nil
}
