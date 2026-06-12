package tui

import (
	"fmt"
	"strings"

	qrcode "github.com/skip2/go-qrcode"
)

const terminalQRQuietZoneModules = 1

func newCompactTerminalQRCode(content string) (*qrcode.QRCode, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil, fmt.Errorf("empty QR content")
	}
	qr, err := qrcode.New(content, qrcode.Low)
	if err != nil {
		return nil, err
	}
	qr.DisableBorder = true
	return qr, nil
}

func compactTerminalQRBitmap(content string) ([][]bool, error) {
	qr, err := newCompactTerminalQRCode(content)
	if err != nil {
		return nil, err
	}
	bitmap := qr.Bitmap()
	if len(bitmap) == 0 || len(bitmap[0]) == 0 {
		return nil, fmt.Errorf("empty QR bitmap")
	}
	return padTerminalQRBitmap(bitmap, terminalQRQuietZoneModules), nil
}

func padTerminalQRBitmap(bitmap [][]bool, padding int) [][]bool {
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

func renderTerminalQRBitmap(bitmap [][]bool) string {
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
			top := bitmap[row][col]
			bottom := bitmap[row+1][col]
			switch {
			case !top && !bottom:
				b.WriteString(whiteAll)
			case !top && bottom:
				b.WriteString(whiteBlack)
			case top && !bottom:
				b.WriteString(blackWhite)
			default:
				b.WriteString(blackAll)
			}
		}
		lines = append(lines, b.String())
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}

func renderCompactTerminalQRCode(content string) (string, error) {
	bitmap, err := compactTerminalQRBitmap(content)
	if err != nil {
		return "", err
	}
	return renderTerminalQRBitmap(bitmap), nil
}
