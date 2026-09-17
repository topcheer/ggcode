package im

import (
	"strings"
	"testing"
)

// sa-41: IM push re-emits tool output to external clients; redactResult is
// the display choke point and must also neutralize terminal escape
// sequences (ATR-2026-00259) so OSC/CSI payloads never leave the process.
func TestRedactResultSanitizesTerminalEscapes(t *testing.T) {
	out := redactResult("\x1b]0;evil title\x07hello\x1b[2Jworld")
	if strings.Contains(out, "\x1b") {
		t.Errorf("raw escape leaked through redactResult: %q", out)
	}
	if strings.Contains(out, "evil title") {
		t.Errorf("OSC payload leaked through redactResult: %q", out)
	}
	if !strings.Contains(out, "hello") || !strings.Contains(out, "world") {
		t.Errorf("benign content lost: %q", out)
	}
}
