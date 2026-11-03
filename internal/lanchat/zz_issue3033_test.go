package lanchat

import (
	"net"
	"reflect"
	"testing"
)

func TestIssue3033_AckEnvelopeSwallowedQuietly(t *testing.T) {
	h := &Hub{}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("ack envelope must not panic: %v", r)
		}
	}()
	h.handleUDPEnvelope(udpEnvelope{Type: "ack", ACKID: "ack-123"}, &net.UDPAddr{})
}

func TestIssue3033_TrackerGone(t *testing.T) {
	//nolint:revive // reflect type walk
	ht := reflect.TypeOf(Hub{})
	for i := 0; i < ht.NumField(); i++ {
		if ht.Field(i).Name == "ackTracker" {
			t.Fatal("#3033: ackTracker must stay removed - it is unread dead state")
		}
	}
}
