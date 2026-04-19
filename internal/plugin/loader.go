package plugin

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"plugin"
	"strings"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/tool"
	"gopkg.in/yaml.v3"
)

// LoadAll loads plugins from config entries and the default plugin directory.
// Individual failures are recorded but do not block startup.
func (m *Manager) LoadAll(entries []config.PluginConfigEntry) {
	for _, entry := range entries {
		m.loadEntry(entry)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	pluginDir := filepath.Join(home, ".ggcode", "plugins")
	m.scanDir(pluginDir)
}

func (m *Manager) loadEntry(entry config.PluginConfigEntry) {
	switch entry.Type {
	case "command", "cmd":
		m.loadCommandPlugin(entry)
	case "so", "go-plugin":
		m.loadGoPlugin(entry)
	default:
		if strings.HasSuffix(entry.Path, ".so") {
			m.loadGoPlugin(entry)
		} else if len(entry.Commands) > 0 {
			m.loadCommandPlugin(entry)
		} else {
			if strings.Contains(entry.Path, ".") {
				m.loadGoPlugin(entry)
			} else {
				m.loadCommandPlugin(entry)
			}
		}
	}
}

func (m *Manager) loadGoPlugin(entry config.PluginConfigEntry) {
	if entry.Path == "" {
		m.results = append(m.results, LoadResult{
			Name: entry.Name, Success: false, Error: fmt.Errorf("no path specified for Go plugin"),
		})
		return
	}

	p := entry.Path
	if strings.HasPrefix(p, "~/") {
		home, _ := os.UserHomeDir()
		p = filepath.Join(home, p[2:])
	}

	plug, err := plugin.Open(p)
	if err != nil {
		m.results = append(m.results, LoadResult{
			Name: entry.Name, Success: false, Error: fmt.Errorf("opening plugin %s: %w", p, err),
		})
		return
	}

	sym, err := plug.Lookup("New")
	if err != nil {
		m.results = append(m.results, LoadResult{
			Name: entry.Name, Success: false, Error: fmt.Errorf("lookup New in %s: %w", p, err),
		})
		return
	}

	newFn, ok := sym.(func() Plugin)
	if !ok {
		m.results = append(m.results, LoadResult{
			Name: entry.Name, Success: false, Error: fmt.Errorf("New in %s is not func() Plugin", p),
		})
		return
	}

	pInst := newFn()
	if err := pInst.Init(entry.Extra); err != nil {
		m.results = append(m.results, LoadResult{
			Name: entry.Name, Success: false, Error: fmt.Errorf("init plugin %s: %w", entry.Name, err),
		})
		return
	}

	m.plugins = append(m.plugins, pInst)
	toolNames := make([]string, len(pInst.Tools()))
	for i, t := range pInst.Tools() {
		toolNames[i] = t.Name()
	}
	m.results = append(m.results, LoadResult{
		Name: entry.Name, Success: true, Tools: toolNames,
	})
}

func (m *Manager) loadCommandPlugin(entry config.PluginConfigEntry) {
	if len(entry.Commands) == 0 {
		m.results = append(m.results, LoadResult{
			Name: entry.Name, Success: false, Error: fmt.Errorf("no commands defined for plugin %s", entry.Name),
		})
		return
	}

	name := entry.Name
	if name == "" {
		name = "command-plugin"
	}

	tools := make([]tool.Tool, 0, len(entry.Commands))
	toolNames := make([]string, 0, len(entry.Commands))
	for _, cmd := range entry.Commands {
		t := NewCommandTool(cmd.Name, cmd.Description, cmd.Execute, cmd.Args)
		tools = append(tools, t)
		toolNames = append(toolNames, cmd.Name)
	}

	p := &commandPlugin{name: name, tools: tools}
	m.plugins = append(m.plugins, p)
	m.results = append(m.results, LoadResult{
		Name: name, Success: true, Tools: toolNames,
	})
}

func (m *Manager) scanDir(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		fullPath := filepath.Join(dir, name)

		switch {
		case strings.HasSuffix(name, ".so"):
			m.loadGoPlugin(config.PluginConfigEntry{Name: strings.TrimSuffix(name, ".so"), Path: fullPath})
		case strings.HasSuffix(name, ".yaml"), strings.HasSuffix(name, ".yml"):
			m.loadDescriptor(fullPath)
		case strings.HasSuffix(name, ".json"):
			m.loadJSONDescriptor(fullPath)
		}
	}
}

func (m *Manager) loadDescriptor(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		m.results = append(m.results, LoadResult{
			Name: path, Success: false, Error: fmt.Errorf("reading %s: %w", path, err),
		})
		return
	}

	var entry config.PluginConfigEntry
	if err := yaml.Unmarshal(data, &entry); err != nil {
		m.results = append(m.results, LoadResult{
			Name: path, Success: false, Error: fmt.Errorf("parsing %s: %w", path, err),
		})
		return
	}
	m.loadEntry(entry)
}

func (m *Manager) loadJSONDescriptor(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		m.results = append(m.results, LoadResult{
			Name: path, Success: false, Error: fmt.Errorf("reading %s: %w", path, err),
		})
		return
	}

	var entry config.PluginConfigEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		m.results = append(m.results, LoadResult{
			Name: path, Success: false, Error: fmt.Errorf("parsing %s: %w", path, err),
		})
		return
	}
	m.loadEntry(entry)
}
