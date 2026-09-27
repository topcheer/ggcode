package im

// Behavior pins for the whatsapp Send (outbound) seams extracted in r196.
// These tests are pure/offline: no network, no whatsmeow client needed.

import (
	"context"
	"strings"
	"testing"

	"go.mau.fi/whatsmeow/types"
)

func TestWhatsappOutboundTarget(t *testing.T) {
	cases := []struct {
		name    string
		binding ChannelBinding
		want    string
	}{
		{"channelID wins", ChannelBinding{ChannelID: "chan-1", TargetID: "t-1"}, "chan-1"},
		{"targetID fallback", ChannelBinding{ChannelID: "", TargetID: "t-1"}, "t-1"},
		{"both empty", ChannelBinding{ChannelID: "", TargetID: ""}, ""},
	}
	for _, tc := range cases {
		if got := whatsappOutboundTarget(tc.binding); got != tc.want {
			t.Errorf("%s: whatsappOutboundTarget() = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestWhatsappParseTargetJID(t *testing.T) {
	jid, err := whatsappParseTargetJID("wa-test", "12025550123@s.whatsapp.net")
	if err != nil {
		t.Fatalf("valid JID rejected: %v", err)
	}
	if jid.User != "12025550123" || jid.String() != "12025550123@s.whatsapp.net" {
		t.Errorf("parsed JID = %q (string %q), want 12025550123@s.whatsapp.net", jid.User, jid.String())
	}

	_, err = whatsappParseTargetJID("wa-test", "a.b.c@server")
	if err == nil {
		t.Fatal("multi-dot JID accepted")
	}
	// Error text is behavior (verbatim wrap from the original Send).
	want := `whatsapp "wa-test": parse JID "a.b.c@server":`
	if !strings.HasPrefix(err.Error(), want) {
		t.Errorf("error = %q, want prefix %q", err.Error(), want)
	}
}

func TestWhatsappAllImagesFailed(t *testing.T) {
	cases := []struct {
		name         string
		failedImages int
		sentImage    bool
		totalImages  int
		want         bool
	}{
		{"all failed", 2, false, 2, true},
		{"some sent", 1, true, 2, false},
		{"none attempted", 0, false, 3, false},
		{"no images at all", 0, false, 0, false},
		{"sent only", 0, true, 1, false},
	}
	for _, tc := range cases {
		if got := whatsappAllImagesFailed(tc.failedImages, tc.sentImage, tc.totalImages); got != tc.want {
			t.Errorf("%s: whatsappAllImagesFailed(%d,%v,%d) = %v, want %v", tc.name, tc.failedImages, tc.sentImage, tc.totalImages, got, tc.want)
		}
	}
}

func TestWhatsappSendOutboundImages_Offline(t *testing.T) {
	a := &whatsappAdapter{name: "wa-pin"}

	// No images: nothing sent, nothing failed, no error.
	sent, failed, err := a.sendOutboundImages(context.Background(), nil, types.JID{}, nil)
	if sent || failed != 0 || err != nil {
		t.Errorf("empty images: got (%v,%d,%v), want (false,0,nil)", sent, failed, err)
	}

	// Unknown-kind image fails without touching the client (nil client safe).
	sent, failed, err = a.sendOutboundImages(context.Background(), nil, types.JID{}, []ExtractedImage{{Kind: "bogus", Data: "x"}})
	if sent {
		t.Error("unknown-kind image must not count as sent")
	}
	if failed != 1 {
		t.Errorf("failed = %d, want 1", failed)
	}
	if err != nil {
		t.Errorf("per-image failure must be absorbed (err = %v), want nil", err)
	}
}

func TestWhatsappSendOutboundChunks_EmptyNoop(t *testing.T) {
	a := &whatsappAdapter{name: "wa-pin"}

	// Empty chunk list: loop never runs, nil client never dereferenced.
	if err := a.sendOutboundChunks(context.Background(), nil, types.JID{}, nil, true); err != nil {
		t.Errorf("empty chunks: err = %v, want nil", err)
	}
}

func TestWhatsappSendOutboundChunks_CtxCancelBeforeFirstSpacing(t *testing.T) {
	a := &whatsappAdapter{name: "wa-pin"}

	// spaceFirst=true with a cancelled context and a single chunk: the loop
	// must enter the spacing select and return ctx.Err() without touching
	// the (nil) client.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := a.sendOutboundChunks(ctx, nil, types.JID{}, []string{"only"}, true)
	if err != context.Canceled {
		t.Errorf("cancelled ctx: err = %v, want context.Canceled", err)
	}
}
