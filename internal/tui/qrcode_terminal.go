package tui

import (
	"github.com/topcheer/ggcode/internal/qrcode"
)

// Terminal QR rendering lives in internal/qrcode (shared with non-TUI
// hosts like the daemon); these thin wrappers keep the historical tui-
// private names so existing callers stay untouched.
func renderCompactTerminalQRCode(content string) (string, error) {
	return qrcode.RenderCompact(content)
}
