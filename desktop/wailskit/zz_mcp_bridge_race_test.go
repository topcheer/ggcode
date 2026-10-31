package wailskit

import (
	"sync"
	"testing"
)

// TestActiveChatBridgeRaceRepro reproduces the data race between
// SetChatBridge (writer, holds activeChatBridgeMu) and the mcp.go/config.go
// readers that historically borrowed globalMu -- a different lock -- so no
// happens-before edge existed between write and read.
//
// Real-world trigger: a workspace-switch teardown goroutine calls
// SetChatBridge (sessions.go) while the frontend MCP panel concurrently
// invokes ListMCPServers / ReconnectMCPServer (Wails-bound App methods on
// other goroutines). Under -race this test fails on unfixed code and passes
// once every reader uses GetChatBridge().
func TestActiveChatBridgeRaceRepro(t *testing.T) {
	var wg sync.WaitGroup

	// Writer: teardown-style SetChatBridge churn (switch / shutdown).
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 3000; i++ {
			SetChatBridge(&ChatBridge{})
			SetChatBridge(nil)
		}
	}()

	// Readers: same read patterns the MCP panel entry points use.
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 3000; j++ {
				// These historically read activeChatBridge under
				// globalMu.RLock() in mcp.go.
				_ = ReconnectMCPServer("x")
				_ = ForceReauthMCPServer("x")
			}
		}()
	}

	wg.Wait()
	SetChatBridge(nil) // restore
}
