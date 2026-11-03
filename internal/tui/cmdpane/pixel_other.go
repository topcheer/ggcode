//go:build !unix

package cmdpane

func getPixelSize() (width, height int) {
	return 0, 0
}
