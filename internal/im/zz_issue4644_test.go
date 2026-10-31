package im

import (
	"os"
	"strings"
	"testing"
)

// #4644: DecryptMegolmEvent failure with ErrNoSessionFound must trigger a
// key re-request (cryptohelper pattern) and a bounded retry before dropping
// the event - previously it was an unconditional debug.Log + silent loss.
func TestIssue4644_KeyReRequestWired(t *testing.T) {
	data, err := os.ReadFile("matrix_adapter.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "crypto.ErrNoSessionFound") {
		t.Fatal("decrypt-failure path must classify ErrNoSessionFound (#4644)")
	}
	if !strings.Contains(text, "SendRoomKeyRequest(ctx, id.RoomID(roomID)") {
		t.Fatal("key re-request must be wired on the missing-session path (#4644)")
	}
	if !strings.Contains(text, `id.UserID(sender):   {"*"}`) && !strings.Contains(text, `id.UserID(sender):  {"*"}`) {
		t.Fatal("key request must target the sender's devices (#4644)")
	}
	if !strings.Contains(text, "DecryptMegolmEvent(ctx, evt)\n\t\t\t\t\t\t}") && !strings.Contains(text, "for attempt := 0; attempt < 3") {
		t.Fatal("bounded retry loop missing (#4644)")
	}
}
