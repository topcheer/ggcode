//go:build goolm

package wailskit

import (
	"testing"
)

func TestIssue616_RefreshNoopWithoutBridge(t *testing.T) {
	SetChatBridge(nil)
	refreshRunningProviderAfterAuth()
}

func TestIssue616_RefreshHookWired(t *testing.T) {
	b := &ChatBridge{}
	SetChatBridge(b)
	defer SetChatBridge(nil)

	refreshRunningProviderAfterAuth()
}
