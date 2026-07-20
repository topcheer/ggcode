package util

import "regexp"

// ansiPattern matches ANSI/VT100 escape sequences commonly found in terminal output:
//   - CSI sequences (colors, cursor movement): \x1b[0;32m, \x1b[2J, \x1b[?25l
//   - OSC sequences (terminal title, hyperlinks): \x1b]0;title\x07
//   - Charset designation: \x1b(B, \x1b)0
//   - Keypad/mode sequences: \x1b=, \x1b>
//
// These sequences are invisible noise to an LLM and waste context tokens.
// Stripping them at ingestion time reduces token bloat from colored command output.
var ansiPattern = regexp.MustCompile(
	"" +
		`\x1b\[[0-9;?<=>]*[ -/]*[@-~]` + // CSI: ESC [ params intermediate-bytes final-byte
		`|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)` + // OSC: ESC ] ... BEL or ESC \
		`|\x1b[()][0-9A-Za-z]` + // Charset: ESC ( B, ESC ) 0
		`|\x1b[=>]`, // Keypad mode: ESC =, ESC >
)

// StripANSI removes ANSI/VT100 escape sequences from a string.
// This is applied to command output before it enters the agent's context,
// reducing token waste from terminal formatting codes.
//
// Example: "\x1b[32msuccess\x1b[0m" → "success"
func StripANSI(s string) string {
	if !containsESC(s) {
		return s // Fast path: no escape sequences at all
	}
	return ansiPattern.ReplaceAllString(s, "")
}

// containsESC is a fast check to avoid regex overhead for clean strings.
func containsESC(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			return true
		}
	}
	return false
}
