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

	// #1601-B: scanDir used to re-load files already declared in config -
	// the second Register hit the same tool names and RegisterTools'
	// first-error return aborted the whole startup, violating this
	// function's own header promise. Skip config-loaded absolute paths.
	loaded := make(map[string]bool, len(entries))
	home := config.HomeDir()
	for _, entry := range entries {
		if p := entry.Path; p != "" {
			// #1631 case 2: the dedup key must use the SAME expansion as
			// loadGoPlugin - "~/.ggcode/x.so" was keyed as "cwd/~/.ggcode/x.so"
			// (filepath.Abs does not expand ~) while the real file loaded from
			// the expanded path, so the key never matched scanDirExcluding's
			// real-absolute exclusion key and the same .so loaded twice (#1601-B
			// residue: duplicate tool Register kills startup).
			if strings.HasPrefix(p, "~/") {
				p = filepath.Join(home, p[2:])
			}
			if abs, err := filepath.Abs(p); err == nil {
				loaded[abs] = true
			}
		}
	}
	pluginDir := filepath.Join(home, ".ggcode", "plugins")
	m.scanDirExcluding(pluginDir, loaded)
}

func (m *Manager) loadEntry(entry config.PluginConfigEntry) {
	switch entry.Type {
	case "command", "cmd":
		m.loadCommandPlugin(entry)
	case "so", "go-plugin":
		m.loadGoPlugin(entry)
	case "grpc":
		// #1601-A: grpc plugins are owned by the grpc manager
		// (interactive_core wires grpcMgr separately); routing them here
		// fell to loadCommandPlugin (Path empty) and recorded a FAILURE
		// the inspector panel displayed - a healthy plugin shown as
		// broken. Record the handoff as success instead.
		// #1631 case 3: with Success=true but Error non-nil the inspector
		// panel (which appends an error line whenever Error != nil) showed
		// "error: handled by gRPC plugin manager" on every healthy grpc
		// entry - the marker must not ride the Error field.
		m.results = append(m.results, LoadResult{
			Name: entry.Name, Success: true,
		})
	default:
		if strings.HasSuffix(entry.Path, ".so") {
			m.loadGoPlugin(entry)
		} else if len(entry.Commands) > 0 {
			m.loadCommandPlugin(entry)
		} else {
			// #787: the old Contains(".") heuristic routed dotted
			// non-.so paths ('./run.sh', '~/tools/my.tool/run') to
			// plugin.Open, which can only fail. Default to the command
			// plugin unless the extension really is .so.
			if filepath.Ext(entry.Path) == ".so" {
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
		home := config.HomeDir()
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
	m.scanDirExcluding(dir, nil)
}

// scanDirExcluding walks the plugin directory, skipping paths in the
// exclude set (absolute-normalized, #1601-B).
func (m *Manager) scanDirExcluding(dir string, exclude map[string]bool) {
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
		if exclude != nil {
			if abs, err := filepath.Abs(fullPath); err == nil && exclude[abs] {
				continue
			}
		}

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
