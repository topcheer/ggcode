package main

import (
	"encoding/json"

	ggcodeplugin "github.com/topcheer/ggcode/sdk/plugin"
)

type echoPlugin struct{}

func (p *echoPlugin) ListTools() []ggcodeplugin.ToolSpec {
	return []ggcodeplugin.ToolSpec{{
		Name:        "greet",
		Description: "Greets a person by name",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","description":"Who to greet"}},"required":["name"]}`),
		Categories:  []string{"test"},
	}}
}

func (p *echoPlugin) Execute(toolName string, input json.RawMessage, ctx ggcodeplugin.Context) (*ggcodeplugin.Result, error) {
	var args struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(input, &args)
	return &ggcodeplugin.Result{Content: "Hello, " + args.Name + "!"}, nil
}

func (p *echoPlugin) Shutdown() {}

func main() {
	ggcodeplugin.Serve(&echoPlugin{})
}
