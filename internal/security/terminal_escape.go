package security

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

const ansiFilteredMarker = "[ansi-filtered]"

var (
	rawStringSeq = regexp.MustCompile(`\x1b\][^\x07\x1b\x9c]*(?:\x07|\x1b\\)` +
		`|\x1b[P^_][^\x07\x1b\x9c]*(?:\x1b\\|\x9c)`)

	rawCSI = regexp.MustCompile(`\x1b\[[0-9;:<=>?]*[ -/]*[@-~]`)

	rawShortEscape = regexp.MustCompile(`\x1b[()#%][0-9A-Za-z@BG]` +
		`|\x1b[=>78DEHMcZ]` +
		`|\x1b`)

	literalDangerousEscape = regexp.MustCompile(
		`(?i)(?:\\x1b|\\u001b|\\033)(?:\[[0-9;]*[HfJK]|\][0-9]+;)`)
)

func SanitizeTerminalForDisplay(content string) string {
	if !mayContainEscape(content) {
		return content
	}
	out := rawStringSeq.ReplaceAllString(content, "")

	out = stripRawC1Sequences(out)
	out = rawCSI.ReplaceAllString(out, "")
	out = rawShortEscape.ReplaceAllString(out, "")
	out = literalDangerousEscape.ReplaceAllString(out, ansiFilteredMarker)
	return dropControlRunes(out)
}

func stripRawC1Sequences(s string) string {

	hasC1Candidate := false
	for i := 0; i < len(s); i++ {
		if c := s[i]; c >= 0x90 && c <= 0x9f {
			hasC1Candidate = true
			break
		}
	}
	if !hasC1Candidate {
		return s
	}

	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		intro := r
		if size == 1 && r == utf8.RuneError {
			intro = rune(s[i])
		}
		switch intro {
		case 0x9b:
			j := i + size
			for j < len(s) && isCSIParamByte(s[j]) {
				j++
			}
			for j < len(s) && s[j] >= 0x20 && s[j] <= 0x2f {
				j++
			}
			if j < len(s) && s[j] >= 0x40 && s[j] <= 0x7e {
				i = j + 1
				continue
			}

			b.WriteString(s[i : i+size])
			i += size
		case 0x90, 0x98, 0x9d, 0x9e, 0x9f:
			j := i + size
			terminated := false
			for j < len(s) {
				r2, sz2 := utf8.DecodeRuneInString(s[j:])
				if size1Standalone(r2, sz2, s[j]) == 0x9c || r2 == 0x9c || r2 == 0x07 {
					j += sz2
					terminated = true
					break
				}
				j += sz2
			}
			if terminated {
				i = j
				continue
			}

			b.WriteString(s[i : i+size])
			i += size
		default:

			b.WriteString(s[i : i+size])
			i += size
		}
	}
	return b.String()
}

func size1Standalone(r rune, size int, raw byte) rune {
	if size == 1 && r == utf8.RuneError {
		return rune(raw)
	}
	return -1
}

func isCSIParamByte(c byte) bool {
	return (c >= '0' && c <= '9') || c == ';' || c == ':' || c == '<' || c == '=' || c == '>' || c == '?'
}

func mayContainEscape(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == 0x1b || c == '\\' ||
			(c < 0x20 && c != '\n' && c != '\t') ||
			(c >= 0x7f && c < 0xa0) {
			return true
		}
	}
	return false
}

func dropControlRunes(s string) string {
	if !strings.ContainsFunc(s, isDroppableControl) {
		return s
	}
	return strings.Map(func(r rune) rune {
		if isDroppableControl(r) {
			return -1
		}
		return r
	}, s)
}

func isDroppableControl(r rune) bool {
	if r == '\n' || r == '\t' {
		return false
	}
	return r < 0x20 || (r >= 0x7f && r < 0xa0)
}
