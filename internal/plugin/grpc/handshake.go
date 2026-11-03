package grpcplugin

import (
	"github.com/hashicorp/go-plugin"
)

var HandshakeConfig = plugin.HandshakeConfig{
	ProtocolVersion:  1,
	MagicCookieKey:   "GGCODE_PLUGIN",
	MagicCookieValue: "ggcode-grpc-plugin-v1",
}

const PluginName = "ggcode_tool_plugin"
