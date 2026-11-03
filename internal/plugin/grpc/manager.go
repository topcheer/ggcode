package grpcplugin

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/hashicorp/go-plugin"
	"github.com/topcheer/ggcode/internal/debug"
	pb "github.com/topcheer/ggcode/internal/plugin/grpc/proto"
	"github.com/topcheer/ggcode/internal/tool"
)

type PluginInstance struct {
	Name     string
	Client   *plugin.Client
	Raw      pb.ToolServiceClient
	Adapters []*GRPCAdapter
}

type Manager struct {
	mu      sync.Mutex
	plugins map[string]*PluginInstance
	workDir string
}

func NewManager(workDir string) *Manager {
	return &Manager{
		plugins: make(map[string]*PluginInstance),
		workDir: workDir,
	}
}

type GRPCPluginConfig struct {
	Name    string
	Command []string
	Env     map[string]string
}

func (m *Manager) LoadAll(configs []GRPCPluginConfig, registry *tool.Registry) []error {
	var errs []error
	for _, cfg := range configs {
		if err := m.loadOne(cfg, registry); err != nil {
			debug.Log("plugin-grpc", "failed to load plugin %s: %v", cfg.Name, err)
			errs = append(errs, fmt.Errorf("plugin %q: %w", cfg.Name, err))
		}
	}
	return errs
}

func (m *Manager) loadOne(cfg GRPCPluginConfig, registry *tool.Registry) error {
	if len(cfg.Command) == 0 {
		return fmt.Errorf("no command specified")
	}

	cmd := exec.Command(cfg.Command[0], cfg.Command[1:]...)
	if len(cfg.Env) > 0 {

		cmd.Env = append(os.Environ(), envSlice(cfg.Env)...)
	}

	client := plugin.NewClient(&plugin.ClientConfig{
		HandshakeConfig:  HandshakeConfig,
		Plugins:          pluginMap,
		Cmd:              cmd,
		AllowedProtocols: []plugin.Protocol{plugin.ProtocolGRPC},
	})

	rpcClient, err := client.Client()
	if err != nil {
		client.Kill()
		return fmt.Errorf("connecting to plugin: %w", err)
	}

	raw, err := rpcClient.Dispense(PluginName)
	if err != nil {
		client.Kill()
		return fmt.Errorf("dispensing plugin: %w", err)
	}

	toolClient := raw.(pb.ToolServiceClient)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	resp, err := toolClient.ListTools(ctx, &pb.ListToolsRequest{})
	if err != nil {
		client.Kill()
		return fmt.Errorf("ListTools: %w", err)
	}

	m.mu.Lock()
	if old, ok := m.plugins[cfg.Name]; ok && old != nil && old.Client != nil {
		m.mu.Unlock()
		debug.Log("plugin-grpc", "config %q reloaded; retiring previous instance before re-registering", cfg.Name)
		for _, a := range old.Adapters {
			registry.Unregister(a.Name())
		}
		old.Client.Kill()
	} else {
		m.mu.Unlock()
	}

	inst := &PluginInstance{
		Name:   cfg.Name,
		Client: client,
		Raw:    toolClient,
	}

	for _, def := range resp.Tools {
		adapter := NewGRPCAdapter(toolClient, def, m.workDir)
		inst.Adapters = append(inst.Adapters, adapter)
		if err := registry.Register(adapter); err != nil {
			debug.Log("plugin-grpc", "warning: failed to register tool %s from plugin %s: %v", def.Name, cfg.Name, err)
		}
		debug.Log("plugin-grpc", "registered tool %s from plugin %s", def.Name, cfg.Name)
	}

	m.mu.Lock()

	if old, ok := m.plugins[cfg.Name]; ok && old != inst && old != nil && old.Client != nil {
		m.mu.Unlock()
		for _, a := range old.Adapters {
			registry.Unregister(a.Name())
		}
		old.Client.Kill()
		m.mu.Lock()
	}
	m.plugins[cfg.Name] = inst
	m.mu.Unlock()

	debug.Log("plugin-grpc", "plugin %s loaded with %d tools", cfg.Name, len(resp.Tools))
	return nil
}

func (m *Manager) Shutdown() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for name, inst := range m.plugins {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_, _ = inst.Raw.Shutdown(ctx, &pb.ShutdownRequest{})
		cancel()
		inst.Client.Kill()
		debug.Log("plugin-grpc", "plugin %s shut down", name)
	}
	m.plugins = make(map[string]*PluginInstance)
}

func (m *Manager) Status() []PluginStatus {
	m.mu.Lock()
	defer m.mu.Unlock()

	statuses := make([]PluginStatus, 0, len(m.plugins))
	for name, inst := range m.plugins {
		tools := make([]string, len(inst.Adapters))
		for i, a := range inst.Adapters {
			tools[i] = a.Name()
		}
		statuses = append(statuses, PluginStatus{
			Name:  name,
			Alive: !inst.Client.Exited(),
			Tools: tools,
		})
	}
	return statuses
}

type PluginStatus struct {
	Name  string
	Alive bool
	Tools []string
}

var pluginMap = map[string]plugin.Plugin{
	PluginName: &GRPCPlugin{},
}

func envSlice(env map[string]string) []string {
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}
