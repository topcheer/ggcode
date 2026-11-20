package tui

import (
	"testing"

	"github.com/topcheer/ggcode/internal/image"
)

// #5842 regression: a text-LESS image interjection mid-run used to be a
// complete no-op - drainPendingInterrupt returned only the (empty) text
// block and parked the images back into pendingImages, so the downstream
// non-blank check failed and nothing was injected. Images must travel with
// the interjection as image content blocks.
func TestDrainPendingInterrupt_ImageOnlyInterjection(t *testing.T) {
	m := newTestModel()
	imgs := []imageAttachedMsg{{img: image.Image{Data: []byte{0x89}, MIME: image.MIMEPNG, Width: 10, Height: 10}}}
	m.pending.enqueueWithImages("", imgs)

	got := m.drainPendingInterrupt(7)
	if len(got) == 0 {
		t.Fatal("image-only interjection drained to nothing (regression: silently dropped)")
	}
	hasImage := false
	for _, b := range got {
		if b.Type == "image" {
			hasImage = true
			if b.ImageMIME == "" || b.ImageData == "" {
				t.Fatalf("image block missing MIME/data: %+v", b)
			}
		}
	}
	if !hasImage {
		t.Fatalf("expected at least one image block, got %+v", got)
	}
	// The images must NOT be parked back into pendingImages - they travelled
	// with the interjection.
	if len(m.pendingImages) != 0 {
		t.Fatalf("images leaked back into pendingImages (%d), expected 0", len(m.pendingImages))
	}
}

func TestDrainPendingInterrupt_TextAndImages(t *testing.T) {
	m := newTestModel()
	imgs := []imageAttachedMsg{{img: image.Image{Data: []byte{0x89}, MIME: image.MIMEPNG, Width: 10, Height: 10}}}
	m.pending.enqueueWithImages("look at this", imgs)

	got := m.drainPendingInterrupt(7)
	hasText, hasImage := false, false
	for _, b := range got {
		switch b.Type {
		case "text":
			hasText = b.Text == "look at this"
		case "image":
			hasImage = true
		}
	}
	if !hasText || !hasImage {
		t.Fatalf("expected text+image blocks, got %+v", got)
	}
	if len(m.pendingImages) != 0 {
		t.Fatalf("images leaked back into pendingImages (%d)", len(m.pendingImages))
	}
}
