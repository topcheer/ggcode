package image

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
)

var ErrClipboardImageUnavailable = errors.New("clipboard does not contain an image")

var clipboardImageMIMEs = []string{
	MIMEPNG,
	MIMEJPEG,
	MIMEGIF,
	MIMEWEBP,
}

func commandOutputError(prefix string, err error, output []byte) error {
	if err == nil {
		return nil
	}
	trimmed := bytes.TrimSpace(output)
	if len(trimmed) == 0 {
		return fmt.Errorf("%s: %w", prefix, err)
	}
	return fmt.Errorf("%s: %w: %s", prefix, err, trimmed)
}

func commandAvailable(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func decodeClipboardImageData(data []byte) (Image, error) {
	if len(data) == 0 {
		return Image{}, ErrClipboardImageUnavailable
	}
	img, err := Decode(data)
	if err != nil {
		return Image{}, err
	}
	return img, nil
}
