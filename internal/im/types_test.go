package im

import "testing"

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
