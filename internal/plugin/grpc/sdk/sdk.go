package sdk

import (
	"github.com/topcheer/ggcode/sdk/plugin"
)

type (
	ToolSpec     = plugin.ToolSpec
	Context      = plugin.Context
	Result       = plugin.Result
	ResultImage  = plugin.ResultImage
	ToolProvider = plugin.ToolProvider
)

var Serve = plugin.Serve

var MustGetEnv = plugin.MustGetEnv
