package im

// Issue #974 companion tests — WhatsApp adapter:
//   - high: handleInbound must drop IsFromMe messages (self-echo ghost replies)
//   - medium: a.client lock asymmetry (all reads via currentClient snapshot)
//   - attached: inbound msgid dedup, Send during disconnect window

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

func waInboundEvent(fromMe bool, id, text string) *events.Message {
	return &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{
				IsFromMe: fromMe,
			},
			ID: types.MessageID(id),
		},
		Message: &waE2E.Message{Conversation: proto.String(text)},
	}
}

// TestWhatsAppInboundFiltersIsFromMe verifies the #974 high fix. Processing
// order in handleInbound is: IsFromMe filter → dedup mark → manager dispatch.
// A self-echo that never appears in the seen map therefore provably never
// reached the manager either (no ghost reply possible).
func TestWhatsAppInboundFiltersIsFromMe(t *testing.T) {
	a := &whatsappAdapter{name: "t", seen: map[string]time.Time{}}

	a.handleInbound(waInboundEvent(true, "self-1", "my own outgoing echo"))
	if len(a.seen) != 0 {
		t.Fatalf("IsFromMe message must be dropped before any processing; seen=%v", a.seen)
	}

	a.handleInbound(waInboundEvent(false, "peer-1", "hello from peer"))
	if len(a.seen) != 1 {
		t.Fatalf("peer message should be processed exactly once; seen=%v", a.seen)
	}
}

// TestWhatsAppInboundDedup redeliveries (reconnects / offline history sync)
// with the same msgid must be processed once.
func TestWhatsAppInboundDedup(t *testing.T) {
	a := &whatsappAdapter{name: "t", seen: map[string]time.Time{}}

	for i := 0; i < 3; i++ {
		a.handleInbound(waInboundEvent(false, "dup-1", "redelivery"))
	}
	if len(a.seen) != 1 {
		t.Fatalf("redelivered msgid must be deduplicated; seen=%v", a.seen)
	}
}

// TestWhatsAppSendWhenDisconnectedReturnsError: previously Send silently
// returned nil during disconnect windows, hiding the dropped message.
func TestWhatsAppSendWhenDisconnectedReturnsError(t *testing.T) {
	a := &whatsappAdapter{name: "t", seen: map[string]time.Time{}}
	err := a.Send(context.Background(),
		ChannelBinding{ChannelID: "8613800138000@s.whatsapp.net"},
		OutboundEvent{Kind: OutboundEventText, Text: "hi"})
	if err == nil {
		t.Fatal("Send during disconnect window must return an error, got nil")
	}
	if !strings.Contains(err.Error(), "not connected") {
		t.Fatalf("want not-connected error, got %v", err)
	}
}

// TestWhatsAppSignalSessionDoneTerminalUpgrade: when Disconnected fills the
// done buffer before LoggedOut arrives, the terminal LoggedOut error must
// upgrade the buffered signal — otherwise the adapter reconnects forever on
// a dead session and the store DB is never cleaned up.
func TestWhatsAppSignalSessionDoneTerminalUpgrade(t *testing.T) {
	done := make(chan error, 1)
	a := &whatsappAdapter{name: "t", sessionDone: done}

	a.signalSessionDone(errors.New("whatsapp disconnected"))
	a.signalSessionDone(errWhatsAppLoggedOut)

	select {
	case err := <-done:
		if !errors.Is(err, errWhatsAppLoggedOut) {
			t.Fatalf("buffered signal must upgrade to terminal error, got %v", err)
		}
	default:
		t.Fatal("expected a signal in the done channel")
	}
}

// TestWhatsAppClientSnapshotRace hammers concurrent client publishes
// (connectAndServe), snapshot reads (currentClient / handleInbound /
// publishState) and clears (markLoggedOut). Under -race the pre-#974 bare
// `a.client` reads reported DATA RACE; all read sites now go through the
// RLock snapshot and must stay clean.
func TestWhatsAppClientSnapshotRace(t *testing.T) {
	a := &whatsappAdapter{name: "race", seen: map[string]time.Time{}, storeDir: t.TempDir()}
	c1 := whatsmeow.NewClient(&store.Device{}, nil)
	c2 := whatsmeow.NewClient(&store.Device{}, nil)

	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(3)
		go func() { // publisher (connectAndServe-like swap under lock)
			defer wg.Done()
			for j := 0; j < 200; j++ {
				a.mu.Lock()
				a.client = c1
				a.mu.Unlock()
				a.mu.Lock()
				a.client = c2
				a.mu.Unlock()
			}
		}()
		go func(g int) { // readers: snapshot reads + inbound handling
			defer wg.Done()
			for j := 0; j < 200; j++ {
				if c := a.currentClient(); c != nil && c.Store == nil {
					t.Error("snapshot client must carry a store")
				}
				a.publishState(true, "connected", "")
				a.handleInbound(waInboundEvent(false, fmt.Sprintf("m-%d-%d", g, j), "x"))
			}
		}(g)
		go func() { // markLoggedOut contender (LoggedOut event handler path)
			defer wg.Done()
			for j := 0; j < 100; j++ {
				a.markLoggedOut()
			}
		}()
	}
	wg.Wait()
}
