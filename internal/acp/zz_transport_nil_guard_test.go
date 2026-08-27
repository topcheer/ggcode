package acp

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestSendRequestNilTransportReturnsError verifies the nil-transport guard in
// sendRequest. Close() nils c.transport after waiting only for the readLoop;
// the prompt-send goroutine (acp.sendPromptRequest) can still be entering
// sendRequest at that point. Bare c.transport derefs nil-derefed there while
// every sibling (writeNotification/writeResponse/writeError) used
// transportSnapshot. The guard must return an error, not panic.
func TestSendRequestNilTransportReturnsError(t *testing.T) {
	c := &Client{} // zero value: transport == nil
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
