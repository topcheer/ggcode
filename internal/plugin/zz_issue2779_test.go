package plugin

import (
	"context"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/mcp"
	"github.com/topcheer/ggcode/internal/tool"
)

func TestIssue2779InstallPropagatesHandlers(t *testing.T) {
	mgr := NewMCPManager(nil, tool.NewRegistry(), "")
	var sampled mcp.SamplingHandler = func(ctx context.Context, params mcp.SamplingParams) (*mcp.SamplingResult, error) {
		return nil, nil
	}
	var elicited mcp.ElicitationHandler = func(ctx context.Context, params mcp.ElicitationParams) (*mcp.ElicitationResult, error) {
		return nil, nil
	}
	mgr.SetSamplingHandler(sampled)
	mgr.SetElicitationHandler(elicited)

	err := mgr.Install(context.Background(), config.MCPServerConfig{
		Name:    "srv-2779",
		Command: "true",
	})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}

	p := mgr.pluginByName("srv-2779")
	if p == nil {
		t.Fatal("installed plugin not found in manager")
	}
	if p.samplingHandler == nil {
		t.Fatal("sampling handler not propagated to runtime-installed plugin - reverse sampling requests will fail with -32601 (#2779: Install bypassed newPluginFromConfig)")
	}
	if p.elicitationHandler == nil {
		t.Fatal("elicitation handler not propagated to runtime-installed plugin (#2779)")
	}
}
