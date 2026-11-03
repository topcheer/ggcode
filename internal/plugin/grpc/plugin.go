package grpcplugin

import (
	"context"

	"github.com/hashicorp/go-plugin"
	pb "github.com/topcheer/ggcode/internal/plugin/grpc/proto"
	"google.golang.org/grpc"
)

type GRPCPlugin struct {
	plugin.NetRPCUnsupportedPlugin
}

func (p *GRPCPlugin) GRPCServer(_ *plugin.GRPCBroker, s *grpc.Server) error {

	return nil
}

func (p *GRPCPlugin) GRPCClient(_ context.Context, _ *plugin.GRPCBroker, c *grpc.ClientConn) (interface{}, error) {
	return pb.NewToolServiceClient(c), nil
}
