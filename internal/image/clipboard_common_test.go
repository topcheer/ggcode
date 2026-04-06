package image

import (
	"errors"
	"strings"
	"testing"
)

func TestDecodeClipboardImageDataEmpty(t *testing.T) {
	_, err := decodeClipboardImageData(nil)
	if !errors.Is(err, ErrClipboardImageUnavailable) {
		t.Fatalf("expected ErrClipboardImageUnavailable, got %v", err)
	}
}

func TestCommandOutputErrorIncludesOutput(t *testing.T) {
	err := commandOutputError("reading clipboard image", errors.New("boom"), []byte("stderr text"))
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "stderr text") {
		t.Fatalf("expected stderr output in error, got %v", err)
	}
}
