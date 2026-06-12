package im

import "testing"

func TestBuildInboundTextIncludesTextAndImageHint(t *testing.T) {
	msg := InboundMessage{
		Text: "fallback",
		Attachments: []Attachment{
			{Kind: AttachmentImage, Name: "diagram.png", MIME: "image/png", DataBase64: "AAAA"},
		},
	}
	got := BuildInboundText(msg)
	want := "fallback\n\n[Attached image from remote IM]"
	if got != want {
		t.Fatalf("BuildInboundText = %q, want %q", got, want)
	}
}

func TestBuildInboundTextFallsBackToPlainText(t *testing.T) {
	msg := InboundMessage{Text: " hello "}
	if got := BuildInboundText(msg); got != "hello" {
		t.Fatalf("BuildInboundText = %q, want hello", got)
	}
}
