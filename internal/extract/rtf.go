package extract

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf16"
)

// rtfExtractor extracts plain text from Rich Text Format files.
type rtfExtractor struct{}

func (rtfExtractor) Format() string { return "rtf" }

func (rtfExtractor) Extract(data []byte) (TextResult, error) {
	raw := string(data)
	if !strings.HasPrefix(strings.TrimSpace(raw), "{\\rtf") {
		return TextResult{}, fmt.Errorf("not a valid RTF file")
	}

	var buf strings.Builder
	depth := 0
	i := 0
	n := len(raw)

	for i < n {
		ch := raw[i]
		switch {
		case ch == '{':
			depth++
			i++
		case ch == '}':
			depth--
			i++
		case ch == '\\' && i+1 < n:
			i++ // skip backslash
			// Read the control word
			j := i
			// Check for special characters first
			switch raw[i] {
			case '\n', '\r':
				// \<newline> is a paragraph break
				buf.WriteByte('\n')
				i++
				continue
			case '~':
				buf.WriteByte(' ') // non-breaking space
				i++
				continue
			case '-':
				buf.WriteByte('-') // optional hyphen
				i++
				continue
			case '_':
				buf.WriteByte('-') // non-breaking hyphen
				i++
				continue
			case '*':
				i++ // skip destination marker
				continue
			case '\'':
				// \'XX — hex-encoded ANSI character
				if i+2 < n {
					hex := raw[i+1 : i+3]
					if val, err := strconv.ParseUint(hex, 16, 8); err == nil {
						buf.WriteByte(byte(val))
					}
					i += 3
					continue
				}
				i++
				continue
			case 'u':
				// \uN — Unicode character (may be negative, followed by '?')
				if i+1 < n && (raw[i+1] == '-' || (raw[i+1] >= '0' && raw[i+1] <= '9')) {
					k := i + 1
					if raw[k] == '-' {
						k++
					}
					for k < n && raw[k] >= '0' && raw[k] <= '9' {
						k++
					}
					numStr := raw[i+1 : k]
					if val, err := strconv.ParseInt(numStr, 10, 16); err == nil {
						r := utf16.Decode([]uint16{uint16(int16(val))})
						if len(r) > 0 {
							buf.WriteRune(r[0])
						}
					}
					// Skip the trailing '?' placeholder if present
					if k < n && raw[k] == '?' {
						k++
					}
					i = k
					continue
				}
				// Not a \uN escape, fall through to keyword
			}

			// Read keyword (letters only)
			for j = i; j < n && raw[j] >= 'a' && raw[j] <= 'z'; j++ {
			}
			keyword := raw[i:j]
			i = j

			// Skip numeric parameter
			if i < n && raw[i] == '-' {
				i++
			}
			for i < n && raw[i] >= '0' && raw[i] <= '9' {
				i++
			}

			// Handle known keywords that produce whitespace
			switch keyword {
			case "par", "line", "row":
				buf.WriteByte('\n')
			case "tab":
				buf.WriteByte('\t')
			case "sect":
				buf.WriteString("\n\n")
			}

			// Skip one optional space after keyword
			if i < n && raw[i] == ' ' {
				i++
			}

		case ch == '\n', ch == '\r':
			// Bare newlines are ignored in RTF text content
			i++
		default:
			// Regular text character
			if depth > 0 {
				buf.WriteByte(ch)
			}
			i++
		}
	}

	return TextResult{
		Text:   strings.TrimSpace(buf.String()),
		Format: "rtf",
	}, nil
}
