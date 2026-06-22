package grpcplugin

import (
	"context"
	"encoding/json"
	"fmt"

	pb "github.com/topcheer/ggcode/internal/plugin/grpc/proto"
	"github.com/topcheer/ggcode/internal/tool"
)

// GRPCAdapter wraps a gRPC tool connection as a tool.Tool.
// One adapter = one tool. A plugin that exports N tools gets N adapters
// all sharing the same underlying gRPC client.
type GRPCAdapter struct {
	client  pb.ToolServiceClient
	name    string
	desc    string
	params  json.RawMessage
	workDir string // injected at registration time
}

// NewGRPCAdapter creates an adapter for a single tool exposed by the plugin.
func NewGRPCAdapter(client pb.ToolServiceClient, def *pb.ToolDefinition, workDir string) *GRPCAdapter {
	return &GRPCAdapter{
		client:  client,
		name:    def.Name,
		desc:    def.Description,
		params:  json.RawMessage(def.Parameters),
		workDir: workDir,
	}
}

func (a *GRPCAdapter) Name() string        { return a.name }
func (a *GRPCAdapter) Description() string { return a.desc }
func (a *GRPCAdapter) Parameters() json.RawMessage {
	if len(a.params) == 0 {
		return json.RawMessage(`{"type":"object","properties":{}}`)
	}
	return a.params
}

func (a *GRPCAdapter) Execute(ctx context.Context, input json.RawMessage) (tool.Result, error) {
	resp, err := a.client.Execute(ctx, &pb.ExecuteRequest{
		ToolName: a.name,
		Input:    input,
		Context: map[string]string{
			"working_dir": a.workDir,
		},
	})
	if err != nil {
		return tool.Result{Content: fmt.Sprintf("plugin error: %v", err), IsError: true}, nil
	}

	result := tool.Result{
		Content:             resp.Content,
		IsError:             resp.IsError,
		SuggestedWorkingDir: resp.SuggestedWorkingDir,
	}

	for _, img := range resp.Images {
		result.Images = append(result.Images, tool.ResultImage{
			MIME:   img.Mime,
			Base64: img.Base64,
			Width:  int(img.Width),
			Height: int(img.Height),
		})
	}

	return result, nil
}
