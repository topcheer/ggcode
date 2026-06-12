//go:build windows

package image

import (
	"strings"
	"testing"
)

func TestWindowsClipboardScriptSupportsImageFileDropList(t *testing.T) {
	script := windowsClipboardImageScript(`C:\Temp\ggcode image.png`)
	for _, want := range []string{
		"ContainsImage()",
		"ContainsFileDropList()",
		"Copy-Item -LiteralPath $file",
		".webp",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("script missing %q: %s", want, script)
		}
	}
}
