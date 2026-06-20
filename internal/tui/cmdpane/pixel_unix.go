//go:build unix

package cmdpane

import (
	"syscall"
	"unsafe"
)

// winsize mirrors struct winsize from <termios.h>.
type winsize struct {
	Row    uint16
	Col    uint16
	Xpixel uint16
	Ypixel uint16
}

// getPixelSize returns the terminal pixel dimensions via TIOCGWINSZ on /dev/tty.
// Returns (0, 0) when pixel data is unavailable (not all terminals report it).
func getPixelSize() (width, height int) {
	f, err := syscall.Open("/dev/tty", syscall.O_RDONLY, 0)
	if err != nil {
		return 0, 0
	}
	defer syscall.Close(f)

	ws := winsize{}
	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		uintptr(f),
		uintptr(syscall.TIOCGWINSZ),
		uintptr(unsafe.Pointer(&ws)),
	)
	if errno != 0 {
		return 0, 0
	}
	return int(ws.Xpixel), int(ws.Ypixel)
}
