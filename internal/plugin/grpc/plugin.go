package grpcplugin

import (
	"context"

	"github.com/hashicorp/go-plugin"
	pb "github.com/topcheer/ggcode/internal/plugin/grpc/proto"
	"google.golang.org/grpc"
)

// GRPCPlugin implements plugin.GRPCPlugin for the host side.
// It tells go-plugin how to create the gRPC client for ToolService.
type GRPCPlugin struct {
	plugin.NetRPCUnsupportedPlugin
}

func (p *GRPCPlugin) GRPCServer(_ *plugin.GRPCBroker, s *grpc.Server) error {
	// Host side never serves — only the plugin process implements the server.
	return nil
}

func (p *GRPCPlugin) GRPCClient(_ context.Context, _ *plugin.GRPCBroker, c *grpc.ClientConn) (interface{}, error) {
	return pb.NewToolServiceClient(c), nil
}
