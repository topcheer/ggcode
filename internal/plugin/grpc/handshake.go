package grpcplugin

import (
	"github.com/hashicorp/go-plugin"
)

// HandshakeConfig is shared between host and plugin to prevent
// accidental connections between incompatible binaries.
var HandshakeConfig = plugin.HandshakeConfig{
	ProtocolVersion:  1,
	MagicCookieKey:   "GGCODE_PLUGIN",
	MagicCookieValue: "ggcode-grpc-plugin-v1",
}

// PluginName is the well-known name used in go-plugin's ServeConfig.
const PluginName = "ggcode_tool_plugin"
