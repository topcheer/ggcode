package image

import (
	"bytes"
	"image"
	"image/jpeg"
	"image/png"
)

// encodeJPEGBytes encodes an image as JPEG with the given quality.
func encodeJPEGBytes(img image.Image, quality int) ([]byte, error) {
	if quality <= 0 {
		quality = 85
	}
	if quality > 100 {
		quality = 100
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// encodePNGBytes encodes an image as PNG.
func encodePNGBytes(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
