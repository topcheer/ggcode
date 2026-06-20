//go:build !unix

package cmdpane

// getPixelSize is a no-op on non-Unix platforms (Windows).
// Pixel dimensions are not available via TIOCGWINSZ on Windows.
func getPixelSize() (width, height int) {
	return 0, 0
}
