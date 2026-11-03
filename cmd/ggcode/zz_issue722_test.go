package main

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"os"
	"strings"
	"testing"
)

func TestReadStdinEnforcesLimit(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer r.Close()

	const limit = int64(64)
	origLimit := stdinMaxBytes
	stdinMaxBytes = limit
	defer func() { stdinMaxBytes = origLimit }()

	if _, err := w.WriteString(strings.Repeat("a", 512)); err != nil {
		t.Fatalf("write: %v", err)
	}
	w.Close()

	withStdinPipe(t, r, func() {
		data, err := readStdin()
		if err == nil {
			t.Fatalf("readStdin accepted oversized input (%d bytes)", len(data))
		}
		if !strings.Contains(err.Error(), fmt.Sprintf("%d", limit)) {
			t.Fatalf("error %q does not mention the %d byte limit", err.Error(), limit)
		}
		if !strings.Contains(err.Error(), "exceeded") {
			t.Fatalf("error %q does not read as an over-limit error", err.Error())
		}
	})
}

func TestReadStdinWithinLimitUnaffected(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	if _, err := w.WriteString("hello pipe"); err != nil {
		t.Fatalf("write: %v", err)
	}
	w.Close()

	withStdinPipe(t, r, func() {
		data, err := readStdin()
		if err != nil {
			t.Fatalf("readStdin: %v", err)
		}
		if string(data) != "hello pipe" {
			t.Fatalf("data = %q, want %q", string(data), "hello pipe")
		}
	})
	r.Close()
}

func TestBuildPipePromptRejectsBinaryText(t *testing.T) {
	binary := []byte{0x00, 0x01, 0x02, 0xff, 0xfe, 0xfd, 0x80}
	_, _, err := buildPipePrompt("hi", binary)
	if err == nil {
		t.Fatal("buildPipePrompt accepted invalid UTF-8 stdin")
	}
	if !strings.Contains(err.Error(), "UTF-8") && !strings.Contains(err.Error(), "binary") {
		t.Fatalf("error %q does not explain the binary/UTF-8 problem", err.Error())
	}
}

func TestBuildPipePromptSmallTextUnaffected(t *testing.T) {
	prompt, blocks, err := buildPipePrompt("hi", []byte("some context"))
	if err != nil {
		t.Fatalf("buildPipePrompt: %v", err)
	}
	if blocks != nil {
		t.Fatalf("expected nil blocks, got %v", blocks)
	}
	if prompt != "some context\n\nhi" {
		t.Fatalf("prompt = %q", prompt)
	}
}

func TestBuildPipePromptImageStdinBypassesUTF8Check(t *testing.T) {
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	prompt, blocks, err := buildPipePrompt("describe", b.Bytes())
	if err != nil {
		t.Fatalf("buildPipePrompt: %v", err)
	}
	if blocks == nil {
		t.Fatalf("expected image blocks; prompt=%q", prompt)
	}
}
