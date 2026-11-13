package qrcode

import (
	"strings"
	"testing"
)

func TestRenderCompactBasic(t *testing.T) {
	out, err := RenderCompact("https://example.etunnel.dev/#token=abc123")
	if err != nil {
		t.Fatalf("RenderCompact: %v", err)
	}
	lines := strings.Split(out, "\n")
	if len(lines) < 8 {
		t.Fatalf("QR too small: %d lines", len(lines))
	}
	// Half-block renderer: every line uses only the four half/full-block runes or space.
	for i, l := range lines {
		for _, r := range l {
			switch r {
			case '█', '▀', '▄', ' ':
			default:
				t.Fatalf("line %d has non-block rune %q", i, r)
			}
		}
	}
}

func TestRenderCompactEmpty(t *testing.T) {
	if _, err := RenderCompact("   "); err == nil {
		t.Fatal("blank content must error")
	}
}
