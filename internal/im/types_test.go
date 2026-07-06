package im

import (
	"testing"
)

func TestInboundMessageProviderContentBuildsImageAndTranscript(t *testing.T) {
	msg := InboundMessage{
		Text: "look at this",
		Attachments: []Attachment{
			{Kind: AttachmentImage, Name: "shot.png", Path: "/tmp/shot.png", MIME: "image/png", DataBase64: "ZGF0YQ=="},
			{Kind: AttachmentVoice, Transcript: "voice transcript"},
		},
	}

	blocks := msg.ProviderContent()
	if len(blocks) != 4 {
		t.Fatalf("expected 4 content blocks, got %d", len(blocks))
	}
	if blocks[0].Type != "text" || blocks[0].Text != "look at this" {
		t.Fatalf("unexpected first block: %#v", blocks[0])
	}
	if blocks[1].Type != "text" || blocks[1].Text != "[Attached shot.png path: /tmp/shot.png]" {
		t.Fatalf("unexpected image path hint block: %#v", blocks[1])
	}
	if blocks[2].Type != "image" || blocks[2].ImageMIME != "image/png" {
		t.Fatalf("unexpected image block: %#v", blocks[2])
	}
	if blocks[3].Type != "text" || blocks[3].Text != "voice transcript" {
		t.Fatalf("unexpected transcript block: %#v", blocks[3])
	}
}

// TestProviderContentAutoDetectsMIME verifies that when an image attachment
// has DataBase64 but no MIME type (as seen with WeCom callbacks), the MIME
// type is auto-detected from the decoded image data so the LLM receives
// the image as a proper ImageBlock instead of silently dropping it.
func TestProviderContentAutoDetectsMIME(t *testing.T) {
	// Minimal valid 1x1 transparent PNG (67 bytes)
	b64 := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAAC0lEQVR42mNk+M9QDwADhgGAWjR9AwAAAABJRU5ErkJggg=="

	msg := InboundMessage{
		Text: "WeCom image without MIME",
		Attachments: []Attachment{
			{Kind: AttachmentImage, DataBase64: b64}, // no MIME set
		},
	}

	blocks := msg.ProviderContent()
	// Should have: text block + image block (MIME auto-detected as image/png)
	hasImageBlock := false
	for _, b := range blocks {
		if b.Type == "image" {
			hasImageBlock = true
			if b.ImageMIME != "image/png" {
				t.Errorf("expected auto-detected MIME image/png, got %s", b.ImageMIME)
			}
		}
	}
	if !hasImageBlock {
		t.Fatal("expected an image block from base64 data with auto-detected MIME, got none")
	}
}
