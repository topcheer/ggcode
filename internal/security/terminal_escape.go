package security

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// Display-time terminal escape sanitization (egress hardening).
//
// Threat model: ATR-2026-00259 "ANSI Escape Code Terminal Injection"
// (https://agentthreatrule.org/en/rules/ATR-2026-00259), OWASP Agentic
// Security ASI08:2026, OWASP LLM Top-10 LLM02:2025 (output handling),
// MITRE ATLAS AML.T0057, NVIDIA garak `ansiescape` probe.
//
// Untrusted tool output (run_command stdout, MCP server responses,
// read_file contents, web fetches) may embed raw terminal control
// sequences. The ingestion-side StripANSI (internal/util) only covers
// run_command output entering the LLM context for token hygiene; every
// other path that reaches a display surface (TUI render, streaming body,
// IM push, desktop bridge) re-emits content verbatim. A hostile tool
// result can then:
//
//   - set the terminal title or inject a fake prompt        (OSC 0/2)
//   - overwrite the clipboard                               (OSC 52)
//   - open phishing hyperlinks                              (OSC 8)
//   - clear/move the screen to hide text from human review  (CSI 2J H f K)
//   - switch to the alternate screen buffer, or enable mouse /
//     bracketed-paste modes, corrupting the surrounding UI  (CSI ?1049 ?1000 ?2004)
//   - carry DCS/APC/PM payloads for terminal-specific exploits
//
// The human-in-the-loop review path is the core agent-security concern:
// what the MODEL sees and what the HUMAN sees must not diverge
// (LLM02:2025). Sanitizing at the display boundary guarantees the two
// views agree on dangerous bytes.
//
// This module is a pure transformation applied just before rendering. It
// is NOT a detector: it produces no findings, blocks nothing upstream,
// and never touches session transcripts or agent context.

// ansiFilteredMarker replaces literal-string escape forms that encode a
// dangerous sequence (see literalDangerousEscape). Visible and greppable
// so reviewers can tell that filtering happened.
const ansiFilteredMarker = "[ansi-filtered]"

var (
	// rawStringSeq strips 7-bit escape-initiated string sequences:
	// OSC (ESC ]), DCS (ESC P), PM (ESC ^), APC (ESC _). Payload runs to
	// BEL, ST (ESC \), or the 8-bit ST (0x9c). Unterminated payloads are
	// handled by the bare-ESC fallback in rawShortEscape.
	rawStringSeq = regexp.MustCompile(`\x1b\][^\x07\x1b\x9c]*(?:\x07|\x1b\\)` +
		`|\x1b[P^_][^\x07\x1b\x9c]*(?:\x1b\\|\x9c)`)

	// NOTE: the 8-bit C1 forms (0x90/0x98/0x9d/0x9e/0x9f introducers and the
	// 0x9b CSI) are NOT regex-matched: those bytes are also UTF-8 continuation
	// bytes, and a byte-level pattern silently corrupts valid multibyte runes
	// (Cyrillic "Лa" = D0 9B 61 lost 9B 61 and emitted a bare D0). They are
	// handled rune-aware in stripC1Sequences below (#3081).
	rawCSI = regexp.MustCompile(`\x1b\[[0-9;:<=>?]*[ -/]*[@-~]`)

	// rawShortEscape strips remaining short escape functions: charset
	// designation (ESC ( B, ESC ) 0, ESC # 8), line-size (ESC % G), single
	// character functions (ESC =, ESC >, ESC 7, ESC 8, ESC c, ...), and
	// finally any bare ESC so an unterminated or unrecognized sequence can
	// never leak its introducer byte to the terminal.
	rawShortEscape = regexp.MustCompile(`\x1b[()#%][0-9A-Za-z@BG]` +
		`|\x1b[=>78DEHMcZ]` +
		`|\x1b`)

	// literalDangerousEscape neutralizes LITERAL escape forms found in
	// displayed text (source code examples, JSON payloads):
	// \x1b, \u001b, \033 — matched only when they encode a DANGEROUS
	// sequence (cursor position H/f, erase J/K, or an OSC ]N; opener),
	// mirroring ATR-2026-00259 detection condition 4. Literal SGR color
	// codes in code examples (\x1b[31m, \x1b[2m, \x1b[0m) are deliberately
	// left intact: they are a documented false-positive class, not a
	// hijack primitive. The unicode-escape evasion form (\u001b]0;...)
	// from the ATR test set is covered.
	literalDangerousEscape = regexp.MustCompile(
		`(?i)(?:\\x1b|\\u001b|\\033)(?:\[[0-9;]*[HfJK]|\][0-9]+;)`)
)

// SanitizeTerminalForDisplay neutralizes terminal control sequences in
// untrusted content immediately before it is rendered to a display surface
// (TUI tool result, streaming body, IM push, desktop bridge).
//
// Behavior:
//   - raw escape sequences (OSC/DCS/PM/APC/CSI/short escapes) are removed
//   - literal string forms encoding dangerous sequences become
//     "[ansi-filtered]"; benign literal color codes are preserved
//   - C0 control runes other than \n and \t are dropped (\r included:
//     carriage-return overwrite is a hide-from-review primitive)
//   - C1 control runes (U+007F–U+009F) are dropped
//
// The input is never mutated and nothing outside the returned display
// string is affected.
func SanitizeTerminalForDisplay(content string) string {
	if !mayContainEscape(content) {
		return content
	}
	out := rawStringSeq.ReplaceAllString(content, "")
	out = stripC1Sequences(out)
	out = stripDecodedC1Sequences(out)
	out = rawCSI.ReplaceAllString(out, "")
	out = rawShortEscape.ReplaceAllString(out, "")
	out = literalDangerousEscape.ReplaceAllString(out, ansiFilteredMarker)
	return dropControlRunes(out)
}

// stripC1Sequences removes 8-bit C1 control sequences (0x9b CSI, and the
// 0x90/0x98/0x9d/0x9e/0x9f DCS/SOS/PM/APC string-sequence introducers)
// without touching valid UTF-8 (#3081).
//
// A C1 byte can only reach a terminal as a raw standalone byte. Inside valid
// UTF-8 those bytes appear solely as continuation bytes of multibyte runes,
// so they are recognized only where a rune-start decode is invalid — never
// inside "Лa" (D0 9B 61), "Û" (C3 9B) or emoji (F0 9F 98 80).
func stripC1Sequences(s string) string {
	// Fast path: fully valid UTF-8 cannot contain a standalone C1 byte.
	if utf8.ValidString(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if c := s[i]; isC1Introducer(c) {
			if r, size := utf8.DecodeRuneInString(s[i:]); r == utf8.RuneError && size == 1 {
				i = c1SequenceEnd(s, i)
				continue // drop the whole C1 sequence
			}
		}
		_, size := utf8.DecodeRuneInString(s[i:])
		if size == 0 {
			size = 1
		}
		b.WriteString(s[i : i+size])
		i += size
	}
	return b.String()
}

func isC1Introducer(c byte) bool {
	switch c {
	case 0x90, 0x98, 0x9b, 0x9d, 0x9e, 0x9f: // DCS SOS CSI PM APC
		return true
	}
	return false
}

// stripDecodedC1Sequences removes C1 sequences that arrived as DECODED UTF-8
// runes (U+0090..U+009F, e.g. "\u009b2J" = C2 9B 32 4A). The byte-level pass
// above deliberately skips these to protect continuation bytes, so they are
// handled here at rune level: same grammar, rune classes instead of bytes.
func stripDecodedC1Sequences(s string) string {
	if !strings.ContainsFunc(s, isDecodedC1Introducer) {
		return s
	}
	rs := []rune(s)
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(rs); {
		r := rs[i]
		if !isDecodedC1Introducer(r) {
			b.WriteRune(r)
			i++
			continue
		}
		if r == '\u009b' { // CSI: params [0-9;:<=>?]*, intermediates [ -/]*, final [@-~]
			j := i + 1
			for j < len(rs) && isCSIParamRune(rs[j]) {
				j++
			}
			for j < len(rs) && rs[j] >= ' ' && rs[j] <= '/' {
				j++
			}
			if j < len(rs) && rs[j] >= '@' && rs[j] <= '~' {
				j++
			}
			i = j
			continue
		}
		// DCS/SOS/PM/APC: payload runs to C1 ST (U+009C) or BEL, inclusive.
		j := i + 1
		for j < len(rs) {
			if rs[j] == '\u009c' || rs[j] == '\u0007' {
				j++
				break
			}
			if rs[j] == '\u001b' { // 7-bit ESC: ST or a fresh sequence
				break
			}
			j++
		}
		i = j
	}
	return b.String()
}

func isCSIParamRune(r rune) bool {
	return (r >= '0' && r <= '9') || r == ';' || r == ':' ||
		r == '<' || r == '=' || r == '>' || r == '?'
}

func isDecodedC1Introducer(r rune) bool {
	switch r {
	case '\u0090', '\u0098', '\u009b', '\u009d', '\u009e', '\u009f':
		return true
	}
	return false
}

// c1SequenceEnd returns the index just past the C1 sequence starting at i.
// Unterminated sequences drop just the introducer byte (parity with the
// previous regex behavior, which also required a terminator).
func c1SequenceEnd(s string, i int) int {
	if s[i] == 0x9b {
		return c1CSIEnd(s, i)
	}
	return c1StringSeqEnd(s, i)
}

// c1CSIEnd consumes an 8-bit CSI sequence: params [0-9;:<=>?]*,
// intermediates [ -/]*, final [@-~].
func c1CSIEnd(s string, i int) int {
	j := i + 1
	for j < len(s) && isCSIParamByte(s[j]) {
		j++
	}
	for j < len(s) && s[j] >= ' ' && s[j] <= '/' {
		j++
	}
	if j < len(s) && s[j] >= '@' && s[j] <= '~' {
		return j + 1
	}
	return i + 1
}

func isCSIParamByte(c byte) bool {
	return (c >= '0' && c <= '9') || c == ';' || c == ':' ||
		c == '<' || c == '=' || c == '>' || c == '?'
}

// c1StringSeqEnd consumes an 8-bit DCS/SOS/PM/APC string sequence: payload
// runs to C1 ST (0x9c) or BEL (0x07), inclusive.
func c1StringSeqEnd(s string, i int) int {
	for j := i + 1; j < len(s); j++ {
		if s[j] == 0x9c || s[j] == 0x07 {
			return j + 1
		}
		if s[j] == 0x1b { // 7-bit ESC: ST or a fresh sequence
			return j
		}
	}
	return i + 1
}

// mayContainEscape is the fast-path scan: any 7-bit ESC byte, any C0/C1
// control byte worth dropping, or any backslash (literal escape forms).
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

// dropControlRunes removes C0 control runes (except \n, \t) and C1 control
// runes that arrived as decoded UTF-8 (e.g. "\xc2\x9b" → U+009B CSI).
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
