# gRPC Plugin Development Guide

gRPC plugins allow you to extend ggcode with custom tools written in **any language**. Unlike Go `.so` plugins, gRPC plugins run as independent processes and have **zero version coupling** with the host.

## Reference Implementations

Ready-to-use demo plugins in three languages:

| Language | Repo | Tools |
|----------|------|-------|
| **Go** | [ggcode-plugin-demo-go](https://github.com/topcheer/ggcode-plugin-demo-go) | `timestamp`, `uuid` |
| **Python** | [ggcode-plugin-demo-python](https://github.com/topcheer/ggcode-plugin-demo-python) | `weather`, `calc` |
| **Node.js** | [ggcode-plugin-demo-node](https://github.com/topcheer/ggcode-plugin-demo-node) | `base64_encode`, `base64_decode`, `hash` |

## Quick Start

### 1. Clone a demo

```bash
# Go
git clone https://github.com/topcheer/ggcode-plugin-demo-go.git
cd ggcode-plugin-demo-go
go build -o ggcode-plugin-demo .

# Python
git clone https://github.com/topcheer/ggcode-plugin-demo-python.git
cd ggcode-plugin-demo-python
pip install -r requirements.txt
./generate_proto.sh

# Node.js
git clone https://github.com/topcheer/ggcode-plugin-demo-node.git
cd ggcode-plugin-demo-node
npm install && npm run build
```

### 2. Install

```bash
# Go
ggcode plugin install time-uuid-tools $(pwd)/ggcode-plugin-demo

# Python
ggcode plugin install weather-calc python $(pwd)/plugin.py

# Node.js
ggcode plugin install crypto-tools node $(pwd)/dist/plugin.js
```

### 3. Verify

```bash
ggcode plugin list
```

Restart ggcode — the agent can now use the plugin's tools.

## CLI Commands

### Install

```bash
ggcode plugin install <name> <command...> [--env KEY=VALUE ...] [--type grpc|command]
```

Examples:

```bash
# Install a Go binary
ggcode plugin install my-tools /path/to/binary

# Install a Python plugin
ggcode plugin install jira-tools python -m my_jira_plugin --env JIRA_TOKEN=xxx

# Install with multiple env vars
ggcode plugin install api-tools ./bin/api-tool --env API_KEY=secret --env DEBUG=true
```

### List

```bash
ggcode plugin list
```

### Uninstall

```bash
ggcode plugin uninstall <name>
```

### Test (verify plugin can start)

```bash
ggcode plugin test <name>
```

## How It Works

```
ggcode host process
  │
  ├── starts plugin subprocess (your binary)
  ├── connects via gRPC (Unix domain socket)
  ├── calls ListTools() → registers tools in the agent's tool registry
  ├── LLM calls tool → host calls Execute() via gRPC → returns result
  └── on shutdown: calls Shutdown() → SIGTERM → grace period → SIGKILL
```

## Manual Configuration

You can also edit `ggcode.yaml` directly:

```yaml
plugins:
  - name: my-plugin
    type: grpc
    command: ["./bin/my-plugin"]       # path to your plugin binary
    env:                                # optional environment variables
      API_KEY: "${MY_API_KEY}"
      DEBUG: "true"
```

## Go SDK Quick Start

### 1. Create a module

```bash
mkdir my-plugin && cd my-plugin
go mod init my-plugin
```

### 2. Add SDK dependency

```bash
go get github.com/topcheer/ggcode/sdk/plugin
```

### 3. Implement the plugin

{% raw %}
```go
package main

import (
    "encoding/json"
    "fmt"
    "github.com/topcheer/ggcode/sdk/plugin"
)

type myPlugin struct{}

func (p *myPlugin) ListTools() []plugin.ToolSpec {
    return []plugin.ToolSpec{{
        Name:        "create_ticket",
        Description: "Create a support ticket",
        Parameters: json.RawMessage(`{
            "type": "object",
            "properties": {
                "title": {"type": "string", "description": "Ticket title"},
                "priority": {"type": "string", "enum": ["low", "medium", "high"]}
            },
            "required": ["title"]
        }`),
    }}
}

func (p *myPlugin) Execute(toolName string, input json.RawMessage, ctx plugin.Context) (*plugin.Result, error) {
    var args struct {
        Title    string `json:"title"`
        Priority string `json:"priority"`
    }
    _ = json.Unmarshal(input, &args)
    return &plugin.Result{
        Content: fmt.Sprintf("Created ticket: %s (priority: %s)", args.Title, args.Priority),
    }, nil
}

func (p *myPlugin) Shutdown() {}

func main() {
    plugin.Serve(&myPlugin{})
}
```
{% endraw %}

### 4. Build and install

```bash
go build -o my-plugin .
ggcode plugin install ticket-tools $(pwd)/my-plugin
```

## SDK Reference

### ToolSpec

| Field | Type | Description |
|-------|------|-------------|
| `Name` | `string` | Unique tool identifier |
| `Description` | `string` | Description shown to the LLM |
| `Parameters` | `json.RawMessage` | JSON Schema for tool parameters |
| `Categories` | `[]string` | Optional grouping tags |

### Context

| Field | Type | Description |
|-------|------|-------------|
| `WorkingDir` | `string` | Agent's current working directory |
| `SessionID` | `string` | Current session identifier |
| `Extra` | `map[string]string` | Additional context values |

### Result

| Field | Type | Description |
|-------|------|-------------|
| `Content` | `string` | Text result returned to the LLM |
| `IsError` | `bool` | If true, treated as an error |
| `Images` | `[]ResultImage` | Images in the result |
| `SuggestedWorkingDir` | `string` | Optional hint to change working directory |

## Non-Go Plugins

Any language with gRPC support works. The plugin must:

1. Check the environment variable `GGCODE_PLUGIN` equals `ggcode-grpc-plugin-v1`
2. Create a Unix domain socket
3. Print the go-plugin handshake line to stdout:
   ```
   1|1|unix|/tmp/plugin-XXXXX|grpc|
   ```
   Format: `core_version|app_version|network|socket_path|protocol|cert`
4. Start a gRPC server on that socket
5. Register a health check service (go-plugin requires it)
6. Implement `ToolService` with `ListTools`, `Execute`, `Shutdown` methods

See the [Python](https://github.com/topcheer/ggcode-plugin-demo-python) and [Node.js](https://github.com/topcheer/ggcode-plugin-demo-node) demos for complete implementations.

## Protocol Details

- **Transport**: gRPC over Unix domain socket
- **Handshake**: `GGCODE_PLUGIN` magic cookie, protocol version 1
- **Framework**: [HashiCorp go-plugin](https://github.com/hashicorp/go-plugin)
- **Proto**: [`proto/ggcode_plugin.proto`](https://github.com/topcheer/ggcode/blob/main/proto/ggcode_plugin.proto)

## Security

- Plugins run with the **same user permissions** as ggcode
- The host's permission system (`tool_permissions`) applies to plugin tools
- Plugins do **not** have access to the host's API keys or config
- Only `working_dir` and `session_id` are passed in the execution context

## Limitations

- One plugin process per config entry
- Plugin startup adds to ggcode launch time (~200ms per plugin)
- No hot-reload (restart ggcode to pick up changes)
