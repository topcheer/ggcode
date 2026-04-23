package tui

import (
	"regexp"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/debug"
)

func combineCmds(cmds ...tea.Cmd) tea.Cmd {
	filtered := make([]tea.Cmd, 0, len(cmds))
	for _, cmd := range cmds {
		if cmd != nil {
			filtered = append(filtered, cmd)
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	if len(filtered) == 1 {
		return filtered[0]
	}
	return tea.Batch(filtered...)
}

// looksLikeStartupGarbage checks whether the input value accumulated before
// setProgramMsg looks like terminal response garbage rather than legitimate
// user input. Terminal responses contain ASCII control characters ([, ], digits,
// and punctuation like ;, :, /) that don't appear in normal typing.
// Normal pre-set values like "ping" contain only alphabetic characters.
func looksLikeStartupGarbage(val string) bool {
	for _, r := range val {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == ' ' || r == '-' || r == '_':
			// Common in normal text, keep going
		default:
			// Contains characters not found in normal typing:
			// ; : / [ ] $ ? = etc. — terminal response artifacts
			return true
		}
	}
	return false
}

// looksLikeTerminalResponse checks whether text appears to be a fragment of a
// terminal response that leaked through due to EscTimeout truncation.
// These fragments always contain ASCII punctuation like ;, :, /, $ combined
// with digits, which never appears in normal human keyboard input (IME input
// uses PasteMsg instead of multi-char KeyPressMsg).
func looksLikeTerminalResponse(text string) bool {
	hasDigit := false
	hasPunct := false
	for _, r := range text {
		switch {
		case r >= '0' && r <= '9':
			hasDigit = true
		case r == ';' || r == ':' || r == '/' || r == '$' || r == '?' || r == '=':
			hasPunct = true
		case r == 'r' || r == 'g' || r == 'b' || r == 'y' || r == 'R':
			// Common in "rgb:", "R" (CPR), "y" (DECRPM)
		default:
			// Contains non-terminal-response characters (e.g. CJK text)
			return false
		}
	}
	return hasDigit && hasPunct
}

func shouldIgnoreInputUpdate(msg tea.Msg, startedAt, lastResizeAt time.Time) bool {
	keyMsg, ok := msg.(tea.KeyPressMsg)
	if !ok || len(keyMsg.Text) == 0 {
		return false
	}

	raw := keyMsg.Text
	// Human keyboard input never contains ESC or control characters.
	// Any KeyPressMsg carrying these is a misparse of a terminal response.
	if strings.ContainsRune(raw, '\x1b') {
		return true
	}
	for _, r := range raw {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

func shouldIgnoreTerminalProbeKey(msg tea.KeyPressMsg) bool {
	// Fast path: normal single-character printable input is never a probe.
	if len(msg.Text) == 1 && msg.Mod == 0 {
		return false
	}

	raw := msg.Text

	// Human keyboard input never contains ESC or other control characters.
	// Terminal CSI/OSC responses often carry these when misparsed as key events.
	for _, r := range raw {
		if r == '\x1b' || unicode.IsControl(r) {
			return true
		}
	}

	// Terminal responses masquerading as Alt+key sequences:
	// - Alt+] with text like "11;rgb:0000/0000/0000" (OSC 11 color query response)
	// - Alt+\ with various CSI fragments
	// - Any Alt+key with long text (human Alt shortcuts are at most 2-3 chars)
	if msg.Mod.Contains(tea.ModAlt) {
		switch raw {
		case "]", "\\":
			return true
		default:
			// Any Alt-modified text longer than 2 chars is almost certainly
			// a misparsed terminal response (CSI/OSC fragments).
			if len(raw) > 2 {
				return true
			}
		}
	}

	// Catch CSI-like fragments even without Alt modifier.
	// Patterns: "1;1R", "?1u", "11;rgb:...", "<0;93;43m", etc.
	// These contain semicolons followed by letters or contain "rgb:".
	if strings.Contains(raw, ";") && (strings.ContainsAny(raw, "RrumM") || strings.Contains(raw, "rgb:")) {
		return true
	}

	// Mouse-like fragments without proper SGR encoding: "<0;93;43m"
	if strings.HasPrefix(raw, "<") && strings.Contains(raw, ";") {
		return true
	}

	return false
}

// isMouseFragmentChar reports whether a single character could be part of a
// fragment of an SGR mouse sequence (ESC [ < Cb ; Cx ; Cy {M|m}). When such
// sequences are split across read boundaries inside bubbletea v2's parser,
// the trailing bytes can leak through as individual single-character
// KeyPressMsg events with no modifier. Real human typing during/right after a
// mouse interaction is unlikely, so suppressing these is safe.
func isMouseFragmentChar(text string) bool {
	if len(text) == 0 {
		return false
	}
	for _, r := range text {
		switch r {
		case '<', '>', 'M', 'm', ';':
			// Always part of SGR mouse alphabet
		default:
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
}

func (m *Model) sanitizeTerminalResponseInput() {
	value := m.input.Value()
	if value == "" {
		return
	}
	// Strip any ESC sequences that leaked into input value.
	cleaned := ansiChunkPattern.ReplaceAllString(value, "")
	// Strip remaining fragments containing control characters.
	var buf strings.Builder
	for _, r := range cleaned {
		if r == '\x1b' || unicode.IsControl(r) {
			continue
		}
		buf.WriteRune(r)
	}
	cleaned = buf.String()
	if cleaned == value {
		return
	}
	debug.Log("tui", "sanitize input changed value=%q cleaned=%q", truncateStr(value, 120), truncateStr(cleaned, 120))
	m.input.SetValue(cleaned)
	m.input.SetHeight(composerWrappedHeight(m.input.Value(), m.input.Width()))
	composerCursorEnd(&m.input)
}

// terminalResponsePattern matches terminal response fragments that leak into
// the text input field as individual KeyPressMsg characters. These arrive from:
//   - OSC 11 background color query: ]11;rgb:XXXX/XXXX/XXXX
//   - CSI CPR (cursor position report): [1;1R
//   - CSI DECRPM (mode report): [?2026;2$y, [?1u
//   - CSI SGR mouse: [<0;93;43m
//   - Partial/truncated fragments: 11;rgb:..., ;1R, 35;1
//
// The pattern detects these by looking for distinctive subsequences that never
// appear in normal human typing:
//   - "rgb:" — always from OSC 11 color responses
//   - "]11;" or "11;rgb" — OSC 11 response or fragment
//   - "[<digits;" — SGR mouse encoding
//   - "[?digits" followed by letter/$ — DECRPM responses
//   - ";digitsR" — CPR response tail (e.g. ";1R", ";16R")
//   - "$y" — XTVERSION/DECRPM response tail
var terminalResponsePattern = regexp.MustCompile(
	`rgb:` +
		`|\]11;` +
		`|11;rgb` +
		`|\[<\d+;` +
		`|\[\?\d+[a-zA-Z\$]` +
		`|;\d+R` +
		`|\$\d+y` +
		`|\[\d+;\d+R` +
		`|\[\d+;\d+;\d+`)

// looksLikeTerminalResponseInput checks whether the textinput value appears to
// have accumulated terminal response garbage. Unlike the per-keystroke checks
// (shouldIgnoreTerminalProbeKey, shouldIgnoreInputUpdate) which inspect
// individual KeyPressMsg events, this function inspects the accumulated input
// value after it has been updated. This catches the case where terminal
// responses arrive as a rapid burst of individual single-character
// KeyPressMsg events that each look like legitimate typing.
//
// Returns true if the entire value looks like a terminal response (should be
// cleared), false otherwise.
func looksLikeTerminalResponseInput(val string) bool {
	if val == "" {
		return false
	}

	// Fast path: if the full terminalResponsePattern matches, it's definitely garbage.
	if terminalResponsePattern.MatchString(val) {
		return true
	}

	// Check for partial OSC/CSI patterns that build up character-by-character.
	// These are fragments that start with ] or [ followed by digits/semicolons,
	// which is how terminal responses begin before the distinctive tail arrives.
	//
	// We require the value to be ENTIRELY composed of terminal-response-like
	// characters (no letters except r/g/b/y/R/u, no CJK, no spaces between words)
	// to avoid false positives on normal input.
	if looksLikePartialTerminalSequence(val) {
		return true
	}

	return false
}

// looksLikePartialTerminalSequence checks if the entire string looks like the
// beginning of a terminal response sequence. Terminal responses consist of:
//   - ] or [ prefix characters
//   - digits and semicolons as separators
//   - lowercase letters r, g, b (from "rgb:"), y (from "$y"), u (from "?1u")
//   - uppercase R (from CPR responses), m/M (from mouse/SGR responses)
//   - / and : (from OSC 11 color specifiers like "rgb:0000/0000/0000")
//   - ? (from CSI ? sequences)
//   - $ (from DECRPM responses like "?2026;2$y")
//   - < (from SGR mouse encoding like "<0;93")
//
// Normal human typing in the input box would contain a mix of CJK characters,
// spaces between words, or diverse letters — not this narrow character set.
func looksLikePartialTerminalSequence(val string) bool {
	// Too short to be meaningful; avoid false positives on single characters.
	if len(val) < 3 {
		return false
	}

	hasStructuralPunct := false // ; : / ? $ < — terminal sequence punctuation
	allTerminalChars := true

	for _, r := range val {
		switch {
		case r >= '0' && r <= '9':
			// digits appear in both terminal responses and normal input
		case r == ']' || r == '[':
			hasStructuralPunct = true
		case r == ';' || r == ':' || r == '/' || r == '?' || r == '$' || r == '<':
			hasStructuralPunct = true
		case r == 'r' || r == 'g' || r == 'b' || r == 'y' || r == 'u':
			// Letters commonly appearing in terminal responses (rgb, y, u)
		case r == 'R' || r == 'm' || r == 'M':
			// Uppercase terminators (CPR "R", mouse "m/M")
		default:
			// Any other character (CJK, most Latin letters, spaces, etc.)
			// means this is likely normal human input.
			allTerminalChars = false
		}
	}

	return allTerminalChars && hasStructuralPunct
}

func startupInputSuppressionActive(startedAt time.Time) bool {
	if startedAt.IsZero() {
		return false
	}
	return time.Since(startedAt) <= startupInputGateWindow
}
