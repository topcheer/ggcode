package extract

import (
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

func svgTextElement(name string) bool {
	switch name {
	case "text", "tspan", "title", "desc", "textPath", "a":

		return true
	}
	return false
}

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

			return TextResult{
				Text:   strings.TrimSpace(buf.String()) + fmt.Sprintf("\n[SVG decode error: %v]", err),
				Format: "svg",
			}, nil
		}
		switch t := token.(type) {
		case xml.StartElement:
			stack = append(stack, t.Name.Local)
			if svgTextElement(t.Name.Local) {

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
				continue
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
