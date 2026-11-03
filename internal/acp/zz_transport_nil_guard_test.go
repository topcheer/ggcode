package acp

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestSendRequestNilTransportReturnsError(t *testing.T) {
	c := &Client{}
	done := make(chan error, 1)
	go func() {
		_, err := c.sendRequest(context.Background(), "session/prompt", nil, time.Second)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("sendRequest on nil transport: expected error, got nil")
		}
		if !strings.Contains(err.Error(), "transport closed") {
			t.Fatalf("sendRequest on nil transport: unexpected error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("sendRequest on nil transport: did not return (panic recovered silently?)")
	}
}
