package security

import (
	"strings"
	"testing"
)

// Table set mirrors ATR-2026-00259 "ANSI Escape Code Terminal Injection"
// true positives / true negatives / evasion tests, plus C1 and literal-form
// coverage. Raw-byte attacks must be REMOVED; dangerous literal forms must
// become "[ansi-filtered]"; benign literal color codes must survive.
func TestSanitizeTerminalForDisplay_ATR2026_00259(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantSub []string // must be absent after sanitization
		keepSub []string // must still be present
	}{
		{
			name:    "ATR-TP1: OSC title hijack",
			input:   "\x1b]0;evil title\x07Normal looking output",
			wantSub: []string{"\x1b]0;", "evil title"},
			keepSub: []string{"Normal looking output"},
		},
		{
			name:    "ATR-TP2: clear screen + home cursor",
			input:   "Output follows\x1b[2J\x1b[HGreat choice!",
			wantSub: []string{"\x1b[2J", "\x1b[H"},
			keepSub: []string{"Output follows", "Great choice!"},
		},
		{
			name:    "ATR-TP3: OSC 8 phishing hyperlink",
			input:   "Click here: \x1b]8;;https://evil.example.com\x07safe-looking text\x1b]8;;\x07",
			wantSub: []string{"evil.example.com", "\x1b]8"},
			keepSub: []string{"safe-looking text"},
		},
		{
			name:    "ATR-TP5: alternate screen buffer",
			input:   "\x1b[?1049h Switched to alternate screen buffer",
			wantSub: []string{"\x1b[?1049h"},
			keepSub: []string{"Switched to alternate"},
		},
		{
			name:    "hide cursor + mouse + bracketed paste modes",
			input:   "\x1b[?25l\x1b[?1000h\x1b[?2004hplain",
			wantSub: []string{"\x1b[?25l", "\x1b[?1000h", "\x1b[?2004h"},
			keepSub: []string{"plain"},
		},
		{
			name:    "OSC 52 clipboard overwrite",
			input:   "\x1b]52;c;" + strings.Repeat("QQ==", 4) + "\x07done",
			wantSub: []string{"\x1b]52"},
			keepSub: []string{"done"},
		},
		{
			name:    "DCS payload stripped",
			input:   "\x1bP+q544e\x1b\\after",
			wantSub: []string{"\x1bP", "544e"},
			keepSub: []string{"after"},
		},
		{
			name:    "ATR-evasion: unicode-escape literal OSC form",
			input:   `\u001b]0;hidden\u0007payload`,
			wantSub: []string{"]0;hidden"},
			keepSub: []string{"payload", ansiFilteredMarker},
		},
		{
			name:    "literal cursor-home form filtered",
			input:   `printf '\x1b[H\x1b[2J' && echo pwned`,
			wantSub: []string{`\x1b[H`},
			keepSub: []string{"echo pwned", ansiFilteredMarker},
		},
		{
			name:    "literal octal OSC opener filtered",
			input:   `\033]0;fake prompt;`,
			wantSub: []string{"]0;fake"},
			keepSub: []string{ansiFilteredMarker},
		},
		{
			name:    "TN1: plain text untouched",
			input:   "Normal tool output without any escape sequences",
			keepSub: []string{"Normal tool output"},
		},
		{
			name:    "TN2: documentation mention of \\x1b untouched",
			input:   `Documentation explains that \x1b stands for ESC in ASCII table`,
			keepSub: []string{`\x1b stands for ESC`},
		},
		{
			name:    "TN3: literal SGR color helpers preserved (regression)",
			input:   "const c = { dim: (s) => `\\x1b[2m${s}\\x1b[0m`, ok: (s) => `\\x1b[32m${s}\\x1b[0m` };",
			keepSub: []string{`\x1b[2m`, `\x1b[32m`, `\x1b[0m`},
		},
		{
			name:    "TN4: color-flag mention without payload",
			input:   "git log --color=always output:\\nauthor Alice",
			keepSub: []string{"git log --color=always"},
		},
		{
			name:    "foreign SGR color stripped from raw output",
			input:   "\x1b[31mred\x1b[0m plain",
			wantSub: []string{"\x1b[31m", "\x1b[0m"},
			keepSub: []string{"red", " plain"},
		},
		{
			name:    "C1 CSI and C1 OSC (UTF-8 decoded) stripped",
			input:   "\u009b2J\u009d0;title\u009ctail",
			wantSub: []string{"\u009b", "\u009d", "0;title"},
			keepSub: []string{"tail"},
		},
		{
			name:    "CR overwrite trick neutralized",
			input:   "all good\rERROR: injected",
			wantSub: []string{"\r"},
			keepSub: []string{"all good", "ERROR: injected"},
		},
		{
			name:    "newline and tab preserved",
			input:   "line1\n\tindented\x1b[31m\nline3",
			keepSub: []string{"line1\n\tindented\nline3"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SanitizeTerminalForDisplay(tt.input)
			for _, bad := range tt.wantSub {
				if strings.Contains(got, bad) {
					t.Errorf("sanitized output still contains %q; got %q", bad, got)
				}
			}
			for _, good := range tt.keepSub {
				if !strings.Contains(got, good) {
					t.Errorf("sanitized output lost %q; got %q", good, got)
				}
			}
		})
	}
}

func TestSanitizeTerminalForDisplay_FastPathByteIdentical(t *testing.T) {
	clean := "plain output\n\twith tab\nand line, no escapes"
	if got := SanitizeTerminalForDisplay(clean); got != clean {
		t.Errorf("clean content must pass through byte-identical, got %q", got)
	}
}

// The sanitizer must compose with secret redaction without interfering:
// an escape-wrapped secret is still masked, and redaction must not be
// confused by stripped sequences.
func TestSanitizeComposesWithRedactForDisplay(t *testing.T) {
	key := "sk-" + strings.Repeat("a", 30)
	in := "\x1b]0;pwned\x07key=" + key + "\x1b[0m"
	out := RedactForDisplay(SanitizeTerminalForDisplay(in))
	if strings.Contains(out, key) {
		t.Errorf("secret leaked through composition: %q", out)
	}
	if strings.Contains(out, "pwned") {
		t.Errorf("OSC payload leaked through composition: %q", out)
	}
	if !strings.Contains(out, "****") {
		t.Errorf("expected masked key in %q", out)
	}
}

func TestSanitizeTerminalForDisplay_InputNotMutated(t *testing.T) {
	in := "\x1b[2Jpayload"
	_ = SanitizeTerminalForDisplay(in)
	if in != "\x1b[2Jpayload" {
		t.Errorf("input was mutated: %q", in)
	}
}
