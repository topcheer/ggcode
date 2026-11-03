package im

import (
	"context"
	"os"
	"strings"
	"testing"

	"maunium.net/go/mautrix/event"
)

func TestIssue2737NoCryptoMachineSendsPlaintext(t *testing.T) {
	a := &matrixAdapter{userID: "@bot:example.org", name: "test"}
	content := &event.MessageEventContent{MsgType: event.MsgText, Body: "hello"}
	evtType, payload := a.maybeEncryptMessage(context.Background(), "!room:example.org", content)
	if evtType != event.EventMessage {
		t.Fatalf("expected event.EventMessage with nil mach, got %v", evtType)
	}
	if payload != content {
		t.Fatalf("expected identical payload passthrough, got %T", payload)
	}
}

func TestIssue2737SendPathsRouteThroughEncryption(t *testing.T) {
	src, err := os.ReadFile("matrix_adapter.go")
	if err != nil {
		t.Fatalf("read matrix_adapter.go: %v", err)
	}
	text := string(src)
	for _, fn := range []string{"func (a *matrixAdapter) sendText(", "func (a *matrixAdapter) sendImage("} {
		idx := strings.Index(text, fn)
		if idx < 0 {
			t.Fatalf("function %q not found", fn)
		}

		end := strings.Index(text[idx+len(fn):], "\nfunc ")
		if end < 0 {
			t.Fatalf("end of %q not found", fn)
		}
		body := text[idx : idx+len(fn)+end]
		if !strings.Contains(body, "a.maybeEncryptMessage(ctx, roomID, content)") {
			t.Errorf("%q does not route content through maybeEncryptMessage (#2737 regression)", fn)
		}
		if strings.Contains(body, "SendMessageEvent(ctx, id.RoomID(roomID), event.EventMessage,") {
			t.Errorf("%q still hardcodes event.EventMessage in SendMessageEvent (#2737 regression)", fn)
		}
	}

	if !strings.Contains(text, "mach.EncryptMegolmEvent(ctx, id.RoomID(roomID), event.EventMessage, content)") {
		t.Error("maybeEncryptMessage no longer wraps content with EncryptMegolmEvent (#2737 regression)")
	}
	if !strings.Contains(text, "return event.EventEncrypted, enc") {
		t.Error("maybeEncryptMessage no longer returns m.room.encrypted payload (#2737 regression)")
	}
}
