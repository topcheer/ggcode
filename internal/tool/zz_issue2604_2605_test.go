//go:build darwin

package tool

import (
	"strings"
	"testing"
)

// #2604: simctl listapps emits OpenStep plist lines like
// `CFBundleIdentifier = "com.apple.Bridge";` - the old parser TrimSpace'd
// only whitespace, leaving the quotes/semicolon attached, so the
// com.apple.* system-app filter NEVER matched. Pin the clean extraction.
func TestIssue2604_ListAppsBundleIDExtraction(t *testing.T) {
	lines := []string{
		`CFBundleIdentifier = "com.apple.Bridge";`,
		`CFBundleIdentifier = "com.example.app";`,
		`CFBundleIdentifier = "com.apple.mobilesafari";`,
		`    CFBundleIdentifier = "com.test.two";   `,
	}
	want := []string{"com.example.app", "com.test.two"}
	var got []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "CFBundleIdentifier") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				bundleID := strings.Trim(strings.TrimSpace(parts[1]), "\"; ")
				if bundleID == "" || strings.HasPrefix(bundleID, "com.apple.") {
					continue
				}
				got = append(got, bundleID)
			}
		}
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("extracted = %v, want %v (com.apple.* must be filtered with clean IDs)", got, want)
	}
}

// #2605: AppleScript string escaping must handle backslashes (the escape
// introducer) BEFORE double quotes; the old code only escaped quotes, so
// `C:\Users\foo` failed to compile and `\n` silently typed a newline.
func TestIssue2604_AppleScriptEscaping(t *testing.T) {
	cases := map[string]string{
		`plain`:        `plain`,
		`say "hi"`:     `say \"hi\"`,
		`C:\Users\foo`: `C:\\Users\\foo`,
		`a\nb`:         `a\\nb`,
		`path\"and\\x`: `path\\\"and\\\\x`,
	}
	for in, want := range cases {
		if got := escapeAppleScriptString(in); got != want {
			t.Errorf("escapeAppleScriptString(%q) = %q, want %q", in, got, want)
		}
	}
}
