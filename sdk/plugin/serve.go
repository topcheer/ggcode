// Package plugin provides the public Go SDK for developing ggcode gRPC plugins.
//
// This package is the public entry point for plugin developers. It is located
// outside the internal/ directory so external modules can import it.
//
// Quick start:
//
//	package main
//
//	import (
//		"encoding/json"
//		"github.com/topcheer/ggcode/sdk/plugin"
//	)
//
//	type myPlugin struct{}
//
//	func (p *myPlugin) ListTools() []plugin.ToolSpec {
//		return []plugin.ToolSpec{{
//			Name:        "hello",
//			Description: "Says hello",
//			Parameters:  json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}}}`),
//		}}
//	}
//
//	func (p *myPlugin) Execute(toolName string, input json.RawMessage, ctx plugin.Context) (*plugin.Result, error) {
//		return &plugin.Result{Content: "Hello!"}, nil
//	}
//
//	func (p *myPlugin) Shutdown() {}
//
//	func main() {
//		plugin.Serve(&myPlugin{})
//	}
package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/hashicorp/go-plugin"
	pb "github.com/topcheer/ggcode/internal/plugin/grpc/proto"
	"google.golang.org/grpc"
)

// ToolSpec describes a single tool that a plugin provides.
type ToolSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"` // JSON Schema
	Categories  []string        `json:"categories,omitempty"`
}

// Context provides runtime information from the host.
type Context struct {
	WorkingDir string
	SessionID  string
	Extra      map[string]string
}

// Result is the return value of Execute.
type Result struct {
	Content             string
	IsError             bool
	Images              []ResultImage
	SuggestedWorkingDir string
}

// ResultImage carries an image in the tool result.
type ResultImage struct {
	Mime   string
	Base64 string
	Width  int
	Height int
}

// ToolProvider is the interface plugin developers implement.
type ToolProvider interface {
	ListTools() []ToolSpec
	Execute(toolName string, input json.RawMessage, ctx Context) (*Result, error)
	Shutdown()
}

// --- internal gRPC server implementation ---

type toolServer struct {
	pb.UnimplementedToolServiceServer
	provider ToolProvider
}

func (s *toolServer) ListTools(_ context.Context, _ *pb.ListToolsRequest) (*pb.ListToolsResponse, error) {
	specs := s.provider.ListTools()
	tools := make([]*pb.ToolDefinition, len(specs))
	for i, spec := range specs {
		tools[i] = &pb.ToolDefinition{
			Name:        spec.Name,
			Description: spec.Description,
			Parameters:  spec.Parameters,
			Categories:  spec.Categories,
		}
	}
	return &pb.ListToolsResponse{Tools: tools}, nil
}

func (s *toolServer) Execute(_ context.Context, req *pb.ExecuteRequest) (*pb.ExecuteResponse, error) {
	toolCtx := Context{
		WorkingDir: req.Context["working_dir"],
		SessionID:  req.Context["session_id"],
		Extra:      req.Context,
	}
	result, err := s.provider.Execute(req.ToolName, json.RawMessage(req.Input), toolCtx)
	if err != nil {
		return &pb.ExecuteResponse{
			Content: fmt.Sprintf("plugin execute error: %v", err),
			IsError: true,
		}, nil
	}
	if result == nil {
		return &pb.ExecuteResponse{Content: ""}, nil
	}
	resp := &pb.ExecuteResponse{
		Content:             result.Content,
		IsError:             result.IsError,
		SuggestedWorkingDir: result.SuggestedWorkingDir,
	}
	for _, img := range result.Images {
		resp.Images = append(resp.Images, &pb.ResultImage{
			Mime:   img.Mime,
			Base64: img.Base64,
			Width:  int32(img.Width),
			Height: int32(img.Height),
		})
	}
	return resp, nil
}

func (s *toolServer) Shutdown(_ context.Context, _ *pb.ShutdownRequest) (*pb.ShutdownResponse, error) {
	s.provider.Shutdown()
	return &pb.ShutdownResponse{}, nil
}

// --- serve ---

type grpcPluginWrapper struct {
	plugin.NetRPCUnsupportedPlugin
	provider ToolProvider
}

func (g *grpcPluginWrapper) GRPCServer(_ *plugin.GRPCBroker, s *grpc.Server) error {
	pb.RegisterToolServiceServer(s, &toolServer{provider: g.provider})
	return nil
}

func (g *grpcPluginWrapper) GRPCClient(_ context.Context, _ *plugin.GRPCBroker, _ *grpc.ClientConn) (interface{}, error) {
	return nil, fmt.Errorf("client side not implemented in SDK")
}

// HandshakeConfig is shared between host and plugin.
var HandshakeConfig = plugin.HandshakeConfig{
	ProtocolVersion:  1,
	MagicCookieKey:   "GGCODE_PLUGIN",
	MagicCookieValue: "ggcode-grpc-plugin-v1",
}

// Serve starts the plugin process, listening for gRPC connections from the host.
// This function blocks until the host disconnects.
func Serve(provider ToolProvider) {
	plugin.Serve(&plugin.ServeConfig{
		HandshakeConfig: HandshakeConfig,
		Plugins: map[string]plugin.Plugin{
			"ggcode_tool_plugin": &grpcPluginWrapper{provider: provider},
		},
		GRPCServer: plugin.DefaultGRPCServer,
	})
}

// MustGetEnv returns the value of an environment variable or exits.
func MustGetEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		fmt.Fprintf(os.Stderr, "required environment variable %s is not set\n", key)
		os.Exit(1)
	}
	return v
}
