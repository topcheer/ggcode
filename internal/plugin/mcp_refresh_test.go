package plugin

import (
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/mcp"
)

func TestComputeToolsHash(t *testing.T) {
	base := []mcp.ToolDefinition{
		{Name: "a", Description: "A tool", InputSchema: []byte(`{"type":"object"}`)},
		{Name: "b", Description: "B tool", InputSchema: []byte(`{"type":"object"}`)},
	}
	reordered := []mcp.ToolDefinition{base[1], base[0]}
	if computeToolsHash(base) != computeToolsHash(reordered) {
		t.Fatal("hash must be order-independent")
	}
	if computeToolsHash(base) == computeToolsHash([]mcp.ToolDefinition{base[0]}) {
		t.Fatal("removing a tool must change the hash")
	}
	edited := append([]mcp.ToolDefinition(nil), base...)
	edited[0].Description = "A tool (edited)"
	if computeToolsHash(base) == computeToolsHash(edited) {
		t.Fatal("description edit must change the hash")
	}
	editedSchema := append([]mcp.ToolDefinition(nil), base...)
	editedSchema[1].InputSchema = []byte(`{"type":"object","properties":{"x":{"type":"string"}}}`)
	if computeToolsHash(base) == computeToolsHash(editedSchema) {
		t.Fatal("schema edit must change the hash")
	}
}

func TestRefreshToolsNowThrottled(t *testing.T) {
	p := NewMCPPlugin(config.MCPServerConfig{Name: "srv", Type: "stdio", Command: "true"})
	p.mu.Lock()
	p.connected = true
	p.client = mcp.NewClientFromConfig(p.cfg)
	p.lastRefreshAt = time.Now()
	p.mu.Unlock()
	outcome, count := p.refreshToolsNow()
	if outcome != RefreshThrottled {
		t.Fatalf("expected RefreshThrottled, got %v", outcome)
	}
	if count != 0 {
		t.Fatalf("throttled refresh must not report a count, got %d", count)
	}
}

func TestRefreshToolsNowNotConnected(t *testing.T) {
	p := NewMCPPlugin(config.MCPServerConfig{Name: "srv", Type: "stdio", Command: "true"})
	if outcome, _ := p.refreshToolsNow(); outcome != RefreshNotConnected {
		t.Fatalf("expected RefreshNotConnected, got %v", outcome)
	}
	p.mu.Lock()
	p.connected = true
	p.closed = true
	p.mu.Unlock()
	if outcome, _ := p.refreshToolsNow(); outcome != RefreshNotConnected {
		t.Fatalf("closed plugin must read as not connected, got %v", outcome)
	}
}

func TestRefreshToolsNowNilClient(t *testing.T) {
	p := NewMCPPlugin(config.MCPServerConfig{Name: "srv", Type: "stdio", Command: "true"})
	p.mu.Lock()
	p.connected = true
	p.mu.Unlock()
	if outcome, _ := p.refreshToolsNow(); outcome != RefreshNotConnected {
		t.Fatalf("nil client must read as not connected, got %v", outcome)
	}
}
