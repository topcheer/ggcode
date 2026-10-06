package daemon

import (
	"os"
)

// colorEnabled reports whether ANSI color escapes should be emitted by the
// follow-mode renderer. Two conditions disable them (sa-46 terminal
// accessibility):
//  1. NO_COLOR is set (https://no-color.org, de-facto standard)  -  CI logs,
//     piped output, and dumb terminals.
//  2. The output is not a terminal  -  e.g. `ggcode follow > log.txt` would
//     otherwise embed raw escapes in the file.
//
// Emojis in catalog strings are kept: they degrade to placeholder glyphs in
// legacy fonts without breaking alignment, whereas raw escapes render as
// literal garbage in dumb terminals and screen readers announce them as
// bracket noise.
func colorEnabled() bool {
	if _, ok := os.LookupEnv("NO_COLOR"); ok {
		return false
	}
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func init() {
	if colorEnabled() {
		return
	}
	// Blank the escape vars (declared in follow.go) so every Fprintf path
	// renders plain text with zero call-site changes.
	ansiDim, ansiReset = "", ""
	ansiBold, ansiFgYellow = "", ""
	ansiFgGreen, ansiFgRed = "", ""
	ansiBgBlue = ""
	ansiClearLine = "\r"
}
