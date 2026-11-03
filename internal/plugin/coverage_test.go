package plugin

import (
	"context"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func TestSetMCPDisabled(t *testing.T) {

	t.Setenv("HOME", t.TempDir())
	resetDisabledCache()

	if err := SetMCPDisabledIn("", "test-server", true); err != nil {
		t.Fatalf("disable should succeed: %v", err)
	}
	if !MCPDisabledIn("", "test-server") {
		t.Fatal("disabled set must be visible")
	}
	if err := SetMCPDisabledIn("", "test-server", false); err != nil {
		t.Fatalf("enable should succeed: %v", err)
	}
	if MCPDisabledIn("", "test-server") {
		t.Fatal("re-enable must clear the entry")
	}
}

func TestConnectOneRefusesDisabled(t *testing.T) {
	if err := SetMCPDisabledIn("", "test-disabled-server", true); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = SetMCPDisabledIn("", "test-disabled-server", false) }()
	p := &MCPPlugin{cfg: config.MCPServerConfig{Name: "test-disabled-server"}}
	before := p.Status()
	m := &MCPManager{}
	m.connectOne(context.Background(), p)
	if p.Status() != before {
		t.Fatal("disabled server must not even transition to pending (#1740 case 1)")
	}
}

func TestMCPPluginAdapter_Nil(t *testing.T) {
	p := &MCPPlugin{cfg: config.MCPServerConfig{Name: "test-disabled-server"}}
	if p.Adapter() != nil {
		t.Error("expected nil adapter for uninitialized plugin")
	}
}

func TestMCPPluginIsConnected(t *testing.T) {
	p := &MCPPlugin{cfg: config.MCPServerConfig{Name: "test-disabled-server"}}
	if p.IsConnected() {
		t.Error("expected false for uninitialized plugin")
	}
}

func TestMCPPluginStatus(t *testing.T) {
	p := &MCPPlugin{cfg: config.MCPServerConfig{Name: "test-disabled-server"}}

	s := p.Status()
	_ = s
}

func TestMCPPluginLastError(t *testing.T) {
	p := &MCPPlugin{cfg: config.MCPServerConfig{Name: "test-disabled-server"}}
	if p.LastError() != "" {
		t.Error("expected empty last error for clean plugin")
	}
}
