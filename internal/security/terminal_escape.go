package security

import (
	"regexp"
	"strings"
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

	// rawStringSeqC1 strips the 8-bit C1 encodings of the same string
	// sequences: DCS/SOS/OSC/PM/APC introducers (0x90/0x98/0x9d/0x9e/0x9f)
	// terminated by C1 ST (0x9c) or BEL. Terminals that accept the C1 form
	// execute these exactly like their 7-bit counterparts.
	rawStringSeqC1 = regexp.MustCompile(`[\x90\x98\x9d\x9e\x9f][^\x07\x9c]*(?:\x9c|\x07)`)

	// rawCSI strips CSI sequences, 7-bit (ESC [) and 8-bit C1 (0x9b),
	// wholesale — including benign SGR color. Display surfaces apply their
	// own styling; any foreign escape, even a lone color code, can leave
	// the frame in a state the surrounding renderer did not author.
	rawCSI = regexp.MustCompile(`\x1b\[[0-9;:<=>?]*[ -/]*[@-~]` +
		`|\x9b[0-9;:<=>?]*[ -/]*[@-~]`)

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
	out = rawStringSeqC1.ReplaceAllString(out, "")
	out = rawCSI.ReplaceAllString(out, "")
	out = rawShortEscape.ReplaceAllString(out, "")
	out = literalDangerousEscape.ReplaceAllString(out, ansiFilteredMarker)
	return dropControlRunes(out)
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
