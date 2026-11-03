package extract

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf16"

	"golang.org/x/text/encoding/charmap"
)

func decodeCodePageByte(cp int, b byte) rune {
	var decoder *charmap.Charmap
	switch cp {
	case 1250:
		decoder = charmap.Windows1250
	case 1251:
		decoder = charmap.Windows1251
	case 1252, 0:
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

		return rune(b)
	default:
		decoder = charmap.Windows1252
	}
	return decoder.DecodeByte(b)
}

func isSkipDestination(kw string) bool {
	switch kw {
	case "fonttbl", "colortbl", "stylesheet", "info", "pict", "object",
		"header", "footer", "headerl", "headerr", "headerf",
		"footerl", "footerr", "footerf",
		"footnote", "annotation", "listtable", "listoverridetable",
		"generator", "rsidtbl", "themedata", "datastore", "xmlnstbl",
		"filetbl", "latentstyles", "_mmathPr":
		return true
	}
	return false
}

type rtfParser struct {
	raw string
	i   int
	n   int

	buf strings.Builder

	depth     int
	skipDepth int
	binSkip   int
	pendingHi rune
	ucSkip    int
	subSkip   int
	ansiCP    int
}

func (p *rtfParser) flushPending() {
	if p.pendingHi != -1 {
		p.buf.WriteRune(0xFFFD)
		p.pendingHi = -1
	}
}

func (p *rtfParser) writeRune(r rune) {
	if p.skipDepth >= 0 {
		return
	}
	switch {
	case utf16.IsSurrogate(r):
		if (r & 0xFC00) == 0xD800 {
			if p.pendingHi != -1 {
				p.buf.WriteRune(0xFFFD)
			}
			p.pendingHi = r
		} else {
			if p.pendingHi != -1 {
				p.buf.WriteRune(utf16.DecodeRune(p.pendingHi, r))
				p.pendingHi = -1
			} else {
				p.buf.WriteRune(0xFFFD)
			}
		}
	default:
		p.flushPending()
		p.buf.WriteRune(r)
	}
}

func (p *rtfParser) emitText(b byte) {
	if p.skipDepth < 0 {
		p.buf.WriteByte(b)
	}
}

func (p *rtfParser) handleEscape() bool {
	switch c := p.raw[p.i]; c {
	case '\n', '\r':
		if p.skipDepth < 0 {
			p.buf.WriteByte('\n')
		}
	case '~':
		p.emitText(' ')
	case '-':
		p.emitText('-')
	case '_':
		p.emitText('-')
	case '*':

	case '\'':

		if p.i+2 < p.n {
			hex := p.raw[p.i+1 : p.i+3]
			if val, err := strconv.ParseUint(hex, 16, 8); err == nil {
				if p.subSkip > 0 {

					p.subSkip--
				} else {
					p.writeRune(decodeCodePageByte(p.ansiCP, byte(val)))
				}
				p.i += 3
				return true
			}

			p.i += 2
			return true
		}
	case 'u':
		if p.handleUnicodeEscape() {
			return true
		}

		return false
	default:
		return false
	}
	p.i++
	return true
}

func (p *rtfParser) handleUnicodeEscape() bool {
	if p.i+1 >= p.n {
		return false
	}
	c := p.raw[p.i+1]
	if c != '-' && (c < '0' || c > '9') {
		return false
	}
	k := p.i + 1
	if p.raw[k] == '-' {
		k++
	}
	for k < p.n && p.raw[k] >= '0' && p.raw[k] <= '9' {
		k++
	}
	numStr := p.raw[p.i+1 : k]

	if val, err := strconv.ParseInt(numStr, 10, 32); err == nil {
		p.writeRune(rune(uint16(val & 0xFFFF)))

		p.subSkip = p.ucSkip
	}

	if k < p.n && p.raw[k] == ' ' {
		k++
	}
	p.i = k
	return true
}

func (p *rtfParser) applyKeyword(keyword, param string) {
	switch keyword {
	case "par", "line", "row":
		if p.skipDepth < 0 {
			p.buf.WriteByte('\n')
		}
	case "tab":
		if p.skipDepth < 0 {
			p.buf.WriteByte('\t')
		}
	case "sect":
		if p.skipDepth < 0 {
			p.buf.WriteString("\n\n")
		}
	case "ansicpg":

		if v, err := strconv.Atoi(param); err == nil {
			p.ansiCP = v
		}
	case "uc":

		if v, err := strconv.Atoi(param); err == nil && v >= 0 && v <= 10 {
			p.ucSkip = v
		}
	case "bin":

		if v, err := strconv.Atoi(param); err == nil && v > 0 {
			p.binSkip = v
		}
	}

	if p.skipDepth < 0 && isSkipDestination(keyword) {
		p.skipDepth = p.depth
	}
}

func (p *rtfParser) handleControlWord() {
	j := p.i
	for j < p.n && p.raw[j] >= 'a' && p.raw[j] <= 'z' {
		j++
	}
	keyword := p.raw[p.i:j]
	p.i = j

	paramStart := p.i
	if p.i < p.n && p.raw[p.i] == '-' {
		p.i++
	}
	for p.i < p.n && p.raw[p.i] >= '0' && p.raw[p.i] <= '9' {
		p.i++
	}
	param := p.raw[paramStart:p.i]

	p.applyKeyword(keyword, param)

	p.subSkip = 0

	if p.i < p.n && p.raw[p.i] == ' ' {
		p.i++
	}
}

func (p *rtfParser) skipBinary() {
	chunk := p.binSkip
	if chunk > p.n-p.i {
		chunk = p.n - p.i
	}
	p.i += chunk
	p.binSkip -= chunk
}

func (p *rtfParser) parse() string {
	for p.i < p.n {
		if p.binSkip > 0 {
			p.skipBinary()
			continue
		}
		switch ch := p.raw[p.i]; {
		case ch == '{':
			p.depth++
			p.subSkip = 0
			p.i++
		case ch == '}':
			if p.depth > 0 {
				p.depth--
			}
			if p.skipDepth >= 0 && p.depth < p.skipDepth {
				p.skipDepth = -1
			}
			p.subSkip = 0
			p.i++
		case ch == '\\' && p.i+1 < p.n:
			p.i++
			if !p.handleEscape() {
				p.handleControlWord()
			}
		case ch == '\n' || ch == '\r':

			p.i++
		default:

			if p.subSkip > 0 && ch == '?' {
				p.subSkip--
				p.i++
				continue
			}
			if p.depth > 0 && p.skipDepth < 0 {
				p.flushPending()
				p.buf.WriteByte(ch)
			}
			p.i++
		}
	}
	p.flushPending()
	return strings.TrimSpace(p.buf.String())
}

type rtfExtractor struct{}

func (rtfExtractor) Format() string { return "rtf" }

func (rtfExtractor) Extract(data []byte) (TextResult, error) {
	raw := string(data)
	if !strings.HasPrefix(strings.TrimSpace(raw), "{\\rtf") {
		return TextResult{}, fmt.Errorf("not a valid RTF file")
	}
	p := &rtfParser{
		raw:       raw,
		n:         len(raw),
		skipDepth: -1,
		pendingHi: -1,
		ucSkip:    1,
	}
	return TextResult{
		Text:   p.parse(),
		Format: "rtf",
	}, nil
}
