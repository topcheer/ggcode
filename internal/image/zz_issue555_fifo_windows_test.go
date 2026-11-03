//go:build windows

package image

import (
	"errors"
)

func mkFifo(path string) error {
	return errors.New("named pipes not creatable via mkfifo on windows")
}
