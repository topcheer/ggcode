package util

import "regexp"

var ansiPattern = regexp.MustCompile(
	"" +
		`\x1b\[[0-9;?<=>:]*[ -/]*[@-~]` +
		`|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)` +
		`|\x1b[()][0-9A-Za-z]` +
		`|\x1b[=>]`,
)

func StripANSI(s string) string {
	if !containsESC(s) {
		return s
	}
	return ansiPattern.ReplaceAllString(s, "")
}

func containsESC(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			return true
		}
	}
	return false
}
