package wailskit

import (
	"sync"
	"testing"
)

func TestActiveChatBridgeRaceRepro(t *testing.T) {
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 3000; i++ {
			SetChatBridge(&ChatBridge{})
			SetChatBridge(nil)
		}
	}()

	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 3000; j++ {

				_ = ReconnectMCPServer("x")
				_ = ForceReauthMCPServer("x")
			}
		}()
	}

	wg.Wait()
	SetChatBridge(nil)
}
