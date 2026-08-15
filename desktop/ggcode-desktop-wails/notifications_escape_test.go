package main

import "testing"

// #409: apostrophes in title/body must be doubled for PowerShell
// single-quoted literals; the previous raw concatenation broke toast syntax
// and opened a PS code-injection vector.
func TestWindowsToastScriptEscapesQuotes(t *testing.T) {
	script := windowsToastScript("can't find file", "it's broken")
	if !containsStr(script, "'can''t find file'") {
		t.Errorf("title not escaped: %s", script)
	}
	if !containsStr(script, "'it''s broken'") {
		t.Errorf("body not escaped: %s", script)
	}
	// Injection attempt must stay inert inside the literal: the apostrophe
	// is doubled ('x'''; ...), so the raw unescaped closing sequence 'x';
	// (quote+semicolon that would terminate the literal early) must not
	// appear in the script.
	evil := windowsToastScript("x'; Remove-Item -Recurse C:\\; 'y", "")
	if containsStr(evil, "'x';") {
		t.Errorf("injection not neutralized: %s", evil)
	}
	if !containsStr(evil, "'x''") {
		t.Errorf("apostrophe not doubled in escaped title: %s", evil)
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && indexStr(s, sub) >= 0)
}

func indexStr(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
