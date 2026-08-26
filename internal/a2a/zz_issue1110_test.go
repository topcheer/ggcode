package a2a

import (
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/tool"
)

// TestIssue1110_StopBeforeStartDoesNotBlock guards #1110: Stop() must return
// when Start() was never called. CLI startup cleanup paths (OAuth2/OIDC/mTLS
// validation failures in cmd/ggcode) call Stop before Start and used to
// block forever on a done channel nobody closes.
func TestIssue1110_StopBeforeStartDoesNotBlock(t *testing.T) {
	handler := NewTaskHandler(t.TempDir(), nil, tool.NewRegistry())
	srv := NewServer(ServerConfig{Port: 0}, handler)
	done := make(chan struct{})
	go func() {
		srv.Stop()
		close(done)
	}()
	select {
	case <-done:
		// Stop returned - fixed.
	case <-time.After(2 * time.Second):
		t.Fatal("CONFIRMED: Stop() blocks forever when Start was never called (#1110)")
	}
}
