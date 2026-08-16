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

// isSkipDestination reports whether an RTF control word introduces a
// destination group whose contents never belong to the document body
// (#566-A): font tables, color tables, stylesheets, embedded pictures,
// headers/footers and metadata. Their contents previously leaked into the
// extracted text (e.g. "Arial;Symbol;;;Riched20...").
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

// rtfParser holds the state of a single RTF-to-text extraction pass.
type rtfParser struct {
	raw string
	i   int // current byte offset
	n   int // len(raw)

	buf strings.Builder

	depth     int  // current brace nesting depth
	skipDepth int  // depth at which destination skipping started; -1 = off
	binSkip   int  // raw \binN bytes remaining to skip verbatim
	pendingHi rune // buffered high surrogate awaiting its low half; -1 = none
	ucSkip    int  // \ucN: substitutes to drop after each \uN
	subSkip   int  // substitutes still to drop right now
	ansiCP    int  // \ansicpg value; 0 -> 1252 semantics
}

// flushPending emits U+FFFD for a high surrogate that never received its
// low half, instead of silently dropping it (#566-B).
func (p *rtfParser) flushPending() {
	if p.pendingHi != -1 {
		p.buf.WriteRune(0xFFFD)
		p.pendingHi = -1
	}
}

// writeRune decodes surrogate pairs (#566-B): a lone high surrogate is
// buffered until the next \uN; a following low surrogate joins it via
// utf16.DecodeRune (previously each half became its own U+FFFD).
func (p *rtfParser) writeRune(r rune) {
	if p.skipDepth >= 0 {
		return
	}
	switch {
	case utf16.IsSurrogate(r):
		if (r & 0xFC00) == 0xD800 { // high surrogate
			if p.pendingHi != -1 {
				p.buf.WriteRune(0xFFFD)
			}
			p.pendingHi = r
		} else { // low surrogate
			if p.pendingHi != -1 {
				p.buf.WriteRune(utf16.DecodeRune(p.pendingHi, r))
				p.pendingHi = -1
			} else {
				p.buf.WriteRune(0xFFFD) // unpaired low
			}
		}
	default:
		p.flushPending()
		p.buf.WriteRune(r)
	}
}

// emitText writes a plain character/escape result unless a destination
// group is being skipped.
func (p *rtfParser) emitText(b byte) {
	if p.skipDepth < 0 {
		p.buf.WriteByte(b)
	}
}

// handleEscape processes the backslash-escape at p.i (which points at the
// byte after '\\'). Returns true if fully handled.
func (p *rtfParser) handleEscape() bool {
	switch c := p.raw[p.i]; c {
	case '\n', '\r':
		if p.skipDepth < 0 {
			p.buf.WriteByte('\n') // \<newline> is a paragraph break
		}
	case '~':
		p.emitText(' ') // non-breaking space
	case '-':
		p.emitText('-') // optional hyphen
	case '_':
		p.emitText('-') // non-breaking hyphen
	case '*':
		// skip destination marker — nothing to emit
	case '\'':
		// \'XX — hex-encoded character in the current \ansicpg code page.
		if p.i+2 < p.n {
			hex := p.raw[p.i+1 : p.i+3]
			if val, err := strconv.ParseUint(hex, 16, 8); err == nil {
				if p.subSkip > 0 {
					// #566(B): this byte is the \ucN substitute for the
					// preceding \uN — drop it instead of duplicating.
					p.subSkip--
				} else {
					p.writeRune(decodeCodePageByte(p.ansiCP, byte(val)))
				}
			}
			p.i += 3
			return true
		}
	case 'u':
		if p.handleUnicodeEscape() {
			return true
		}
		// Not a \uN escape — treat as keyword (\uc, \ul, ...) from 'u'.
		return false
	default:
		return false
	}
	p.i++
	return true
}

// handleUnicodeEscape processes \uN (p.i points at 'u'). Returns false if
// the 'u' is not followed by a number (then it is a plain keyword).
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
	// #566(B): parse as a full uint16 range (0..65535). ParseInt(bits=16)
	// rejected high surrogates (55357 etc.), silently dropping emoji halves.
	if val, err := strconv.ParseInt(numStr, 10, 32); err == nil {
		p.writeRune(rune(uint16(val & 0xFFFF)))
	}
	// #566(B): the next ucSkip characters (usually a literal '?' or \'XX
	// bytes) are ANSI substitutes for this \uN and must not also appear.
	p.subSkip = p.ucSkip
	// A control word's single trailing delimiter space is consumed syntax,
	// not text (RTF spec): `\u233 ?` means "é" + substitute, not "é" + " ?".
	if k < p.n && p.raw[k] == ' ' {
		k++
	}
	p.i = k
	return true
}

// applyKeyword performs the side effects of a control word.
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
		// \ansicpgNNNN sets the code page for \'XX escapes (#547).
		if v, err := strconv.Atoi(param); err == nil {
			p.ansiCP = v
		}
	case "uc":
		// #566(B): \ucN sets how many substitutes follow each \uN.
		if v, err := strconv.Atoi(param); err == nil && v >= 0 && v <= 10 {
			p.ucSkip = v
		}
	case "bin":
		// #566(A): \binN embeds N raw binary bytes — skip verbatim.
		if v, err := strconv.Atoi(param); err == nil && v > 0 {
			p.binSkip = v
		}
	}
	// #566(A): entering a destination whose contents are not body text —
	// drop everything until its group closes.
	if p.skipDepth < 0 && isSkipDestination(keyword) {
		p.skipDepth = p.depth
	}
}

// handleControlWord reads keyword + numeric parameter starting at p.i and
// applies it.
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
	// Any control word terminates a pending substitute sequence.
	p.subSkip = 0
	// Skip one optional space after keyword.
	if p.i < p.n && p.raw[p.i] == ' ' {
		p.i++
	}
}

// skipBinary consumes pending \binN bytes without copying them.
func (p *rtfParser) skipBinary() {
	chunk := p.binSkip
	if chunk > p.n-p.i {
		chunk = p.n - p.i
	}
	p.i += chunk
	p.binSkip -= chunk
}

// parse runs the state machine over the whole input.
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
			p.i++ // skip backslash
			if !p.handleEscape() {
				p.handleControlWord()
			}
		case ch == '\n' || ch == '\r':
			// Bare newlines are ignored in RTF text content.
			p.i++
		default:
			// A literal '?' right after \uN is the ANSI substitute (#566-B)
			// — consume it without emitting.
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

// rtfExtractor extracts plain text from Rich Text Format files.
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
		ucSkip:    1, // RTF spec default: one substitute after each \uN
	}
	return TextResult{
		Text:   p.parse(),
		Format: "rtf",
	}, nil
}
