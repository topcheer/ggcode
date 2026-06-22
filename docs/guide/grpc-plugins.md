# gRPC Plugin Development Guide

gRPC plugins allow you to extend ggcode with custom tools written in **any language**. Unlike Go `.so` plugins, gRPC plugins run as independent processes and have **zero version coupling** with the host.

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

## Configuration

Add a `type: grpc` entry to your `ggcode.yaml`:

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

### 1. Create a new Go module

```bash
mkdir my-plugin && cd my-plugin
go mod init my-plugin
```

### 2. Add the SDK dependency

```bash
go get github.com/topcheer/ggcode/sdk/plugin
```

In your `go.mod`, add a replace directive pointing to your local ggcode checkout (or remove it once published):

```
replace github.com/topcheer/ggcode => /path/to/ggcode
```

### 3. Implement the plugin

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
        Categories: []string{"tickets"},
    }}
}

func (p *myPlugin) Execute(toolName string, input json.RawMessage, ctx plugin.Context) (*plugin.Result, error) {
    var args struct {
        Title    string `json:"title"`
        Priority string `json:"priority"`
    }
    _ = json.Unmarshal(input, &args)

    // Your business logic here
    ticketID := "TKT-001"

    return &plugin.Result{
        Content: fmt.Sprintf("Created ticket %s: %s (priority: %s)", ticketID, args.Title, args.Priority),
    }, nil
}

func (p *myPlugin) Shutdown() {
    // Cleanup resources (close DB connections, etc.)
}

func main() {
    plugin.Serve(&myPlugin{})
}
```

### 4. Build and configure

```bash
go build -o my-plugin .
```

In `ggcode.yaml`:

```yaml
plugins:
  - name: my-tools
    type: grpc
    command: ["/path/to/my-plugin"]
```

## SDK Reference

### ToolSpec

| Field | Type | Description |
|-------|------|-------------|
| `Name` | `string` | Unique tool identifier (e.g., `"create_ticket"`) |
| `Description` | `string` | Human-readable description shown to the LLM |
| `Parameters` | `json.RawMessage` | JSON Schema defining the tool's parameters |
| `Categories` | `[]string` | Optional grouping tags |

### Context

| Field | Type | Description |
|-------|------|-------------|
| `WorkingDir` | `string` | The agent's current working directory |
| `SessionID` | `string` | Current session identifier |
| `Extra` | `map[string]string` | Additional context key-values |

### Result

| Field | Type | Description |
|-------|------|-------------|
| `Content` | `string` | Text result returned to the LLM |
| `IsError` | `bool` | If true, the result is treated as an error |
| `Images` | `[]ResultImage` | Images to include in the result |
| `SuggestedWorkingDir` | `string` | Optional hint to change the agent's working directory |

### ResultImage

| Field | Type | Description |
|-------|------|-------------|
| `Mime` | `string` | MIME type (e.g., `"image/png"`) |
| `Base64` | `string` | Base64-encoded image data |
| `Width` | `int` | Image width in pixels |
| `Height` | `int` | Image height in pixels |

## Protocol Details

- **Transport**: gRPC over Unix domain socket
- **Handshake**: Magic cookie `GGCODE_PLUGIN=ggcode-grpc-plugin-v1`, protocol version 1
- **Framework**: [HashiCorp go-plugin](https://github.com/hashicorp/go-plugin)

## Non-Go Plugins

Any language with gRPC support can implement a plugin. You need to:

1. Generate gRPC stubs from `proto/ggcode_plugin.proto`
2. Implement the `ToolService` gRPC server
3. Use go-plugin's handshake protocol (magic cookie + stdout handshake)

For Python, you can use `grpcio` and implement the handshake manually. A Python SDK is planned.

## Security

- Plugins run with the **same user permissions** as ggcode
- The host's permission system (`tool_permissions`) applies to plugin tools
- Plugins do **not** have access to the host's API keys or config
- Only `working_dir` and `session_id` are passed in the execution context

## Limitations

- One plugin process per config entry (use multiple tools within one plugin for efficiency)
- Plugin startup adds to ggcode launch time (~200ms per plugin)
- No hot-reload yet (restart ggcode to pick up plugin changes)
