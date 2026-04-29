# Instance-Level Configuration Design

## Problem

1. **No instance-level config**: Only A2A has `.ggcode/a2a.yaml` override. All other config fields (model, IM, MCP, knight, etc.) are global-only.
2. **Save() leaks merged config**: `Config.Save()` does `yaml.Marshal(c)` which serializes the entire in-memory config (including A2A overrides from `.ggcode/a2a.yaml`) back to the global file.
3. **TUI/WebUI panels all write global**: No UI to save config at instance level.

## Design

### Storage Layout

```
~/.ggcode/
  ggcode.yaml                     # global config
  instances/
    {sha256-of-abs-workspace-path}/
      ggcode.yaml                 # instance config (same schema as global)
      a2a.yaml                    # optional: separate A2A config
    ...
```

The sha256 hash is of the **absolute, cleaned workspace directory path**.

Example: workspace `/home/user/projects/myapp` → `sha256("/home/user/projects/myapp")[:16]` → `~/.ggcode/instances/a1b2c3d4e5f67890/ggcode.yaml`

We use the **first 16 hex chars** (8 bytes) for readability while avoiding collisions for typical workspace counts.

### Merge Rule

**"Global wins if non-zero"** — the opposite of typical overlay:

```
global.field set  →  use global value (instance config does NOT override)
global.field zero →  use instance value (instance fills gaps)
```

This means:
- If global has `model: gpt-4o`, instance CANNOT change it to `model: gpt-4o-mini`
- If global has no `model` (empty string), instance can set `model: gpt-4o-mini`
- If global has `im.enabled: true`, instance cannot disable it
- If global has `im.adapters` empty, instance can define adapters

This applies field-by-field for primitive types. For maps (vendors, im.adapters, mcp_servers), the merge is:
- Global keys are kept as-is
- Instance keys are added only if not present in global
- For matching keys, global values win

For slices (allowed_dirs, plugins, mcp_servers):
- Global list is kept as-is if non-empty
- Instance list is used only if global list is empty

### Config struct changes

```go
type Config struct {
    // ... existing fields ...
    FilePath     string `yaml:"-" json:"-"`
    FirstRun     bool   `yaml:"-" json:"-"`

    // Instance-level config support
    instanceDir  string `yaml:"-" json:"-"` // ~/.ggcode/instances/{sha256}/
    instancePath string `yaml:"-" json:"-"` // instanceDir + "/ggcode.yaml"
}
```

### New functions in `internal/config/instance.go`

```go
// InstanceDir returns the per-workspace instance config directory.
// Format: ~/.ggcode/instances/{sha256[:16]}
func InstanceDir(workspace string) string

// LoadInstanceConfig loads the instance-level config for a workspace.
// Returns nil if no instance config exists.
func LoadInstanceConfig(workspace string) *Config

// MergeInstance applies instance config on top of global config.
// Rule: global non-zero fields are never overwritten.
func MergeInstance(global, instance *Config)

// SaveInstance writes only the instance-level overrides to the instance config file.
// It writes only the fields that differ from the global config.
func (c *Config) SaveInstance() error

// HasInstanceConfig returns true if an instance config file exists for this workspace.
func HasInstanceConfig(workspace string) bool

// EffectiveConfigPath returns the file path where config changes should be saved.
// If scope is "instance", returns instance path. If "global", returns global path.
func (c *Config) EffectiveConfigPath(scope string) string
```

### Modified Save() logic

The existing `Save()` always writes to `c.FilePath` (global). We keep this behavior.

New `SaveInstance()` writes only to `c.instancePath`.

The key change: **Save() must NOT serialize fields that came from instance merge**. To do this:

1. Before merge, snapshot the global config's non-zero fields
2. `Save()` only writes fields that were present in the original global config file
3. Instance-only fields are never written to the global file

Implementation: read the original YAML file as raw map, apply only the changes made at runtime to the fields that were already present, write back.

Alternative (simpler): **Save() reads the raw global file, patches only the specific field being changed, writes back.** This is what `SaveLanguagePreference` already does. We extend this pattern to all saves.

### TUI panel changes

Each panel that saves config needs a scope selector:

1. **Model panel** (model_panel.go): save model/vendor/endpoint to instance or global
2. **MCP panel** (mcp_panel.go): save mcp_servers to instance or global  
3. **IM panels** (feishu/discord/dingtalk): save im config to instance or global
4. **WebUI config API**: add `scope` parameter to all PUT endpoints

UI pattern: When user presses Enter/Save in a panel, show a prompt:
```
Save to: [Global] [Instance (/path/to/workspace)]
```

### Migration from .ggcode/a2a.yaml

On first load with the new system, if `.ggcode/a2a.yaml` exists:
1. Load it as before
2. If instance config dir doesn't exist yet, create it
3. Move the A2A config into the instance `ggcode.yaml`
4. Keep `.ggcode/a2a.yaml` as a symlink or just leave it (both work)

Actually, simpler: keep `.ggcode/a2a.yaml` as a separate file loaded by the instance system. The instance directory can contain both `ggcode.yaml` and `a2a.yaml`.

### Phase Plan

**Phase 1: Core infrastructure** (this PR)
- `instance.go` — InstanceDir, LoadInstanceConfig, MergeInstance, SaveInstance
- Modify `Load()` flow to merge instance config
- Fix `Save()` to not leak merged fields

**Phase 2: TUI integration**
- Add scope selector to config panels
- Model panel, MCP panel, IM panels

**Phase 3: WebUI integration**
- Add `scope` to config API endpoints
- UI toggle for global vs instance

**Phase 4: Migration & polish**
- Migrate `.ggcode/a2a.yaml` to instance config
- Document instance config in ggcode.example.yaml
