package plugin

import (
	"testing"
)

func TestSetMCPDisabled(t *testing.T) {
	// Should not panic
	SetMCPDisabled("test-server", true)
	SetMCPDisabled("test-server", false)
}

func TestMCPPluginAdapter_Nil(t *testing.T) {
	p := &MCPPlugin{}
	if p.Adapter() != nil {
		t.Error("expected nil adapter for uninitialized plugin")
	}
}

func TestMCPPluginIsConnected(t *testing.T) {
	p := &MCPPlugin{}
	if p.IsConnected() {
		t.Error("expected false for uninitialized plugin")
	}
}

func TestMCPPluginStatus(t *testing.T) {
	p := &MCPPlugin{}
	// Default status is empty
	s := p.Status()
	_ = s // may be empty, just verify no panic
}

func TestMCPPluginLastError(t *testing.T) {
	p := &MCPPlugin{}
	if p.LastError() != "" {
		t.Error("expected empty last error for clean plugin")
	}
}
