// Package sdk is the internal alias for the public SDK.
// External plugin developers should import: github.com/topcheer/ggcode/sdk/plugin
package sdk

import (
	"github.com/topcheer/ggcode/sdk/plugin"
)

// Re-export types for internal use.
type (
	ToolSpec     = plugin.ToolSpec
	Context      = plugin.Context
	Result       = plugin.Result
	ResultImage  = plugin.ResultImage
	ToolProvider = plugin.ToolProvider
)

// Serve re-exports the public Serve function.
var Serve = plugin.Serve

// MustGetEnv re-exports the public MustGetEnv.
var MustGetEnv = plugin.MustGetEnv
