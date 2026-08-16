package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/subagent"
)

// fakeNamedAgentProvider implements provider.Provider WITHOUT
// ClonableWithModel, for the #551-C unsupported-override test.
type fakeNamedAgentProvider struct{}

func (fakeNamedAgentProvider) Name() string { return "fake" }
func (fakeNamedAgentProvider) Chat(ctx context.Context, msgs []provider.Message, tools []provider.ToolDefinition) (*provider.ChatResponse, error) {
	return nil, fmt.Errorf("not implemented")
}
func (fakeNamedAgentProvider) ChatStream(ctx context.Context, msgs []provider.Message, tools []provider.ToolDefinition) (<-chan provider.StreamEvent, error) {
	return nil, fmt.Errorf("not implemented")
}
func (fakeNamedAgentProvider) CountTokens(ctx context.Context, msgs []provider.Message) (int, error) {
	return 0, nil
}

// clonableFakeProvider implements ClonableWithModel.
type clonableFakeProvider struct{ fakeNamedAgentProvider }

func (clonableFakeProvider) CloneWithModel(model string) provider.Provider {
	return clonableFakeProvider{}
}

func newNamedAgentTestEnv(t *testing.T, prov provider.Provider, available []string) (*subagent.Manager, UseNamedAgentTool) {
	t.Helper()
	ws := t.TempDir()
	store := subagent.NewTemplateStore(ws)
	mgr := subagent.NewManager(config.SubAgentConfig{})
	tool := UseNamedAgentTool{
		Store:           store,
		Manager:         mgr,
		Provider:        prov,
		Tools:           NewRegistry(),
		AvailableModels: func() []string { return available },
	}
	return mgr, tool
}

// TestIssue551C_NamedAgentModelWhitelist verifies use_namedagent rejects a
// template whose model override is not on the AvailableModels whitelist —
// the same gate spawn_agent has had (L131-148) (#551-C).
func TestIssue551C_NamedAgentModelWhitelist(t *testing.T) {
	mgr, tool := newNamedAgentTestEnv(t, clonableFakeProvider{}, []string{"glm-5.3", "glm-5.2"})

	if err := tool.Store.Save(subagent.NamedAgentTemplate{
		Name:  "probe",
		Model: "nonexistent-model",
	}); err != nil {
		t.Fatalf("Save template: %v", err)
	}

	res, err := tool.Execute(context.Background(), json.RawMessage(`{"name":"probe","task":"do thing"}`))
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError result for unknown model, got: %s", res.Content)
	}
	if !strings.Contains(res.Content, "not available") || !strings.Contains(res.Content, "nonexistent-model") {
		t.Fatalf("error message lacks model/availability info: %s", res.Content)
	}

	// No zombie sub-agent entry should be created.
	if agents := mgr.List(); len(agents) != 0 {
		t.Fatalf("rejected model left %d zombie sub-agent entries", len(agents))
	}

	// A whitelisted model passes the gate.
	if err := tool.Store.Save(subagent.NamedAgentTemplate{
		Name:  "probe-ok",
		Model: "glm-5.3",
	}); err != nil {
		t.Fatalf("Save template: %v", err)
	}
	res2, err := tool.Execute(context.Background(), json.RawMessage(`{"name":"probe-ok","task":"do thing"}`))
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if res2.IsError {
		t.Fatalf("whitelisted model rejected: %s", res2.Content)
	}
}

// TestIssue551C_NamedAgentProviderWithoutModelOverrideSupport verifies the
// provider cannot silently ignore a model override: when the provider does
// not implement ClonableWithModel, use_namedagent errors instead of
// spawning on the parent model (#551-C).
func TestIssue551C_NamedAgentProviderWithoutModelOverrideSupport(t *testing.T) {
	mgr, tool := newNamedAgentTestEnv(t, fakeNamedAgentProvider{}, []string{"glm-5.3", "glm-5.3-air"})

	if err := tool.Store.Save(subagent.NamedAgentTemplate{
		Name:  "probe-noclone",
		Model: "glm-5.3",
	}); err != nil {
		t.Fatalf("Save template: %v", err)
	}

	res, err := tool.Execute(context.Background(), json.RawMessage(`{"name":"probe-noclone","task":"do thing"}`))
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError result when provider lacks model override support, got: %s", res.Content)
	}
	if !strings.Contains(res.Content, "does not support model overrides") {
		t.Fatalf("error message lacks capability explanation: %s", res.Content)
	}
	if agents := mgr.List(); len(agents) != 0 {
		t.Fatalf("rejected override left %d zombie sub-agent entries", len(agents))
	}
}
