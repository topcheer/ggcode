package lanchat

// Regression probe for #3033: the ackTracker sync.Map was written on every
// UDP ack envelope but never read anywhere - dead code that grew without
// bound (~150-200B per fragment ack, linear over weeks of daemon uptime).
// It is removed; reliable delivery stays with udp_transport.go's bounded
// acks channel map. This probe pins the envelope-swallowing semantics.

import (
	"net"
	"reflect"
	"testing"
)

// TestIssue3033_AckEnvelopeSwallowedQuietly: an ack envelope must be
// consumed silently (no panic, no dispatch) - Hub zero value is enough
// because the ack branch returns before touching any state.
func TestIssue3033_AckEnvelopeSwallowedQuietly(t *testing.T) {
	h := &Hub{}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("ack envelope must not panic: %v", r)
		}
	}()
	h.handleUDPEnvelope(udpEnvelope{Type: "ack", ACKID: "ack-123"}, &net.UDPAddr{})
}

// TestIssue3033_TrackerGone: compile-time pin - the Hub struct no longer
// carries the tracker. Uses reflect to assert no field named ackTracker,
// so accidental reintroduction fails loudly.
func TestIssue3033_TrackerGone(t *testing.T) {
	//nolint:revive // reflect type walk
	ht := reflect.TypeOf(Hub{})
	for i := 0; i < ht.NumField(); i++ {
		if ht.Field(i).Name == "ackTracker" {
			t.Fatal("#3033: ackTracker must stay removed - it is unread dead state")
		}
	}
}
