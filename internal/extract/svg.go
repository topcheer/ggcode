package extract

import (
	"encoding/xml"
	"strings"
)

// svgExtractor extracts text from SVG files.
type svgExtractor struct{}

func (svgExtractor) Format() string { return "svg" }

func (svgExtractor) Extract(data []byte) (TextResult, error) {
	var buf strings.Builder
	decoder := xml.NewDecoder(strings.NewReader(string(data)))
	for {
		token, err := decoder.Token()
		if err != nil {
			break
		}
		if se, ok := token.(xml.StartElement); ok {
			switch se.Name.Local {
			case "text", "tspan", "title", "desc":
				// Check for aria-label attribute
				for _, attr := range se.Attr {
					if attr.Name.Local == "aria-label" && attr.Value != "" {
						if buf.Len() > 0 {
							buf.WriteByte('\n')
						}
						buf.WriteString(attr.Value)
					}
				}
			}
		} else if cd, ok := token.(xml.CharData); ok {
			text := strings.TrimSpace(string(cd))
			if text != "" && buf.Len() > 0 {
				buf.WriteByte(' ')
			}
			if text != "" {
				buf.WriteString(text)
			}
		}
	}

	return TextResult{
		Text:   strings.TrimSpace(buf.String()),
		Format: "svg",
	}, nil
}
