//go:build windows

package image

import (
	"errors"
)

// mkFifo is unavailable on Windows; the FIFO tests skip when it errors.
func mkFifo(path string) error {
	return errors.New("named pipes not creatable via mkfifo on windows")
}
