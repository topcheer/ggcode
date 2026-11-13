// Package qrcode renders compact terminal QR codes (half-block "▀▄"
// bitmap, 1-module quiet zone). Extracted from internal/tui so non-TUI
// hosts (daemon, CLI one-shots) can print scannable QRs without pulling
// in the whole TUI dependency tree.
package qrcode

import (
	"fmt"
	"strings"

	qrcode "github.com/skip2/go-qrcode"
)

const terminalQRQuietZoneModules = 1

// RenderCompact renders content as a compact half-block terminal QR.
func RenderCompact(content string) (string, error) {
	bitmap, err := compactBitmap(content)
	if err != nil {
		return "", err
	}
	return renderBitmap(bitmap), nil
}

// Bitmap returns the quiet-zone-padded module bitmap for content.
func Bitmap(content string) ([][]bool, error) {
	return compactBitmap(content)
}

func compactBitmap(content string) ([][]bool, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil, fmt.Errorf("empty QR content")
	}
	qr, err := qrcode.New(content, qrcode.Low)
	if err != nil {
		return nil, err
	}
	qr.DisableBorder = true
	bitmap := qr.Bitmap()
	if len(bitmap) == 0 || len(bitmap[0]) == 0 {
		return nil, fmt.Errorf("empty QR bitmap")
	}
	return padBitmap(bitmap, terminalQRQuietZoneModules), nil
}

func padBitmap(bitmap [][]bool, padding int) [][]bool {
	if padding <= 0 || len(bitmap) == 0 || len(bitmap[0]) == 0 {
		return bitmap
	}
	width := len(bitmap[0]) + padding*2
	padded := make([][]bool, 0, len(bitmap)+padding*2)
	for i := 0; i < padding; i++ {
		padded = append(padded, make([]bool, width))
	}
	for _, row := range bitmap {
		paddedRow := make([]bool, width)
		copy(paddedRow[padding:], row)
		padded = append(padded, paddedRow)
	}
	for i := 0; i < padding; i++ {
		padded = append(padded, make([]bool, width))
	}
	return padded
}

func renderBitmap(bitmap [][]bool) string {
	if len(bitmap) == 0 || len(bitmap[0]) == 0 {
		return ""
	}
	if len(bitmap)%2 == 1 {
		bitmap = append(bitmap, make([]bool, len(bitmap[0])))
	}

	const (
		whiteAll   = "█"
		whiteBlack = "▀"
		blackWhite = "▄"
		blackAll   = " "
	)

	lines := make([]string, 0, len(bitmap)/2)
	for row := 0; row < len(bitmap); row += 2 {
		var b strings.Builder
		for col := 0; col < len(bitmap[row]); col++ {
			white := bitmap[row][col]
			black := bitmap[row+1][col]
			switch {
			case white && black:
				b.WriteString(whiteAll)
			case white:
				b.WriteString(whiteBlack)
			case black:
				b.WriteString(blackWhite)
			default:
				b.WriteString(blackAll)
			}
		}
		lines = append(lines, b.String())
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}
