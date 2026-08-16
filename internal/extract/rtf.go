package extract

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf16"

	"golang.org/x/text/encoding/charmap"
)

// decodeCodePageByte maps a single byte from the given ANSI code page to a
// rune. cp 0 or an unknown code page falls back to Windows-1252 (the RTF
// default per the spec). Bytes that have no mapping decode to U+FFFD.
func decodeCodePageByte(cp int, b byte) rune {
	var decoder *charmap.Charmap
	switch cp {
	case 1250:
		decoder = charmap.Windows1250
	case 1251:
		decoder = charmap.Windows1251
	case 1252, 0: // RTF default per spec
		decoder = charmap.Windows1252
	case 1253:
		decoder = charmap.Windows1253
	case 1254:
		decoder = charmap.Windows1254
	case 1255:
		decoder = charmap.Windows1255
	case 1256:
		decoder = charmap.Windows1256
	case 1257:
		decoder = charmap.Windows1257
	case 1258:
		decoder = charmap.Windows1258
	case 932, 936, 949, 950:
		// Multi-byte CJK code pages (Shift-JIS, GBK, UHC, Big5) are not
		// single-byte decodable here; fall back to the raw byte (preserves
		// prior behavior for these rare files).
		return rune(b)
	default:
		decoder = charmap.Windows1252
	}
	return decoder.DecodeByte(b)
}

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
	ansiCP := 0 // \ansicpg value; 0 -> default 1252 semantics

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
				// \'XX — hex-encoded character in the current \ansicpg code page.
				// Decode to a rune and write as UTF-8 (#547): writing the raw byte
				// produced invalid UTF-8 for any non-ASCII character (e.g. é).
				if i+2 < n {
					hex := raw[i+1 : i+3]
					if val, err := strconv.ParseUint(hex, 16, 8); err == nil {
						buf.WriteRune(decodeCodePageByte(ansiCP, byte(val)))
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
			paramStart := i
			if i < n && raw[i] == '-' {
				i++
			}
			for i < n && raw[i] >= '0' && raw[i] <= '9' {
				i++
			}
			param := raw[paramStart:i]

			// Handle known keywords that produce whitespace
			switch keyword {
			case "par", "line", "row":
				buf.WriteByte('\n')
			case "tab":
				buf.WriteByte('\t')
			case "sect":
				buf.WriteString("\n\n")
			case "ansicpg":
				// \ansicpgNNNN sets the code page for \'XX escapes (#547).
				if v, err := strconv.Atoi(param); err == nil {
					ansiCP = v
				}
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
