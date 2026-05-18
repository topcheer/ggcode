package tunnel

import (
	"fmt"
	"strings"

	"github.com/skip2/go-qrcode"
)

// QRCodeForURL generates a QR code string for the given URL using terminal-friendly block characters.
// Returns a string that can be printed directly to a terminal.
func QRCodeForURL(url string) (string, error) {
	qr, err := qrcode.New(url, qrcode.Medium)
	if err != nil {
		return "", fmt.Errorf("qr generate: %w", err)
	}

	// Get the string representation using block characters
	return qr.ToSmallString(false), nil
}

// QRCodeLines returns the QR code as a slice of strings (one per line).
func QRCodeLines(url string) ([]string, error) {
	s, err := QRCodeForURL(url)
	if err != nil {
		return nil, err
	}
	return strings.Split(s, "\n"), nil
}
