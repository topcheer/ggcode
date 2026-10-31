package tui

import (
	"testing"

	"github.com/topcheer/ggcode/internal/im"
)

// #4818: remote IM image attachments must be decoded into the pending-image
// pipeline instead of being collapsed to a '[Attached image from remote IM]'
// placeholder by BuildInboundText - daemon-attached sessions get the full
// pixels, and TUI-attached sessions must too.
func TestIssue4818_RemoteInboundImageInjected(t *testing.T) {
	m := NewModel(nil, nil)
	before := len(m.pendingImages)

	out, _ := m.Update(remoteInboundMsg{
		Message: im.InboundMessage{
			Text: "/images",
			Envelope: im.Envelope{
				Adapter:   "tg-1",
				Platform:  im.PlatformTelegram,
				ChannelID: "c1",
			},
			Attachments: []im.Attachment{
				{
					ID:         "a1",
					Kind:       im.AttachmentImage,
					Name:       "photo.png",
					MIME:       "image/png",
					DataBase64: tinyValidPNG,
				},
			},
		},
		Response: make(chan error, 1),
	})
	m2, ok := out.(Model)
	if !ok {
		t.Fatalf("#4818: expected Model, got %T", out)
	}
	if got := len(m2.pendingImages) - before; got != 1 {
		t.Fatalf("#4818: expected 1 injected pending image, got %d (before=%d after=%d)", got, before, len(m2.pendingImages))
	}
}

// #4818: a corrupt base64 payload must not panic or break the text flow -
// the attachment is skipped and the text still routes.
func TestIssue4818_RemoteInboundCorruptImageSkipped(t *testing.T) {
	m := NewModel(nil, nil)
	before := len(m.pendingImages)

	out, _ := m.Update(remoteInboundMsg{
		Message: im.InboundMessage{
			Text: "/images (corrupt)",
			Envelope: im.Envelope{
				Adapter:   "tg-1",
				Platform:  im.PlatformTelegram,
				ChannelID: "c1",
			},
			Attachments: []im.Attachment{
				{ID: "bad", Kind: im.AttachmentImage, Name: "x.png", DataBase64: "!!!not-base64!!!"},
			},
		},
		Response: make(chan error, 1),
	})
	m2, ok := out.(Model)
	if !ok {
		t.Fatalf("#4818: expected Model, got %T", out)
	}
	if got := len(m2.pendingImages) - before; got != 0 {
		t.Fatalf("#4818: corrupt image must be skipped, got %d injected", got)
	}
}
