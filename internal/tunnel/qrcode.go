package tunnel

import (
	"fmt"
	"strings"

	"github.com/skip2/go-qrcode"
)

const terminalQRQuietZoneModules = 1

func newTerminalQRCode(content string) (*qrcode.QRCode, error) {
	qr, err := qrcode.New(strings.TrimSpace(content), qrcode.Low)
	if err != nil {
		return nil, fmt.Errorf("qr generate: %w", err)
	}
	qr.DisableBorder = true
	return qr, nil
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

// QRCodeForURL generates a QR code string for the given URL using terminal-friendly block characters.
// Returns a string that can be printed directly to a terminal.
func QRCodeForURL(url string) (string, error) {
	qr, err := newTerminalQRCode(url)
	if err != nil {
		return "", err
	}

	return renderTerminalQRBitmap(padTerminalQRBitmap(qr.Bitmap(), terminalQRQuietZoneModules)), nil
}

// QRCodeLines returns the QR code as a slice of strings (one per line).
func QRCodeLines(url string) ([]string, error) {
	s, err := QRCodeForURL(url)
	if err != nil {
		return nil, err
	}
	return strings.Split(s, "\n"), nil
}

// QRCodePNG generates a PNG image of the QR code for the given URL.
// Returns raw PNG bytes suitable for displaying in an image widget.
func QRCodePNG(url string) ([]byte, error) {
	png, err := qrcode.Encode(url, qrcode.Low, 256)
	if err != nil {
		return nil, fmt.Errorf("qr png: %w", err)
	}
	return png, nil
}
