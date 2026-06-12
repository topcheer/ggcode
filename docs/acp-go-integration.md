# ggcode-acp-go integration

ggcode now consumes the standalone ACP client/runtime library from:

- `github.com/topcheer/ggcode-acp-go`

This document explains what moved, what stayed local, and how ggcode wires the library into its
existing delegate/tool system.

## Why this split exists

The old `internal/acp` package mixed together two different responsibilities:

1. **reusable ACP client/runtime logic** for talking to external ACP CLIs
2. **ggcode-specific ACP host/server logic** for running `ggcode acp`

`ggcode-acp-go` extracts the first part into a standalone module while leaving the second part
inside ggcode.

## Current boundary

```text
github.com/topcheer/ggcode-acp-go
  ├─ discovery
  ├─ transport
  ├─ protocol types
  ├─ client runtime
  └─ standalone permission/logger hooks

ggcode/internal/acpclient
  └─ adapter layer into ggcode's tool + permission interfaces

ggcode/internal/acp
  ├─ ACP server command path
  ├─ protocol handler
  ├─ auth
  ├─ session host/state
  └─ MCP bridge / ask_user / host-side behavior
```

## Runtime flow inside ggcode

When ggcode wants to delegate to an external ACP agent:

1. `cmd/ggcode/root.go`, `pipe.go`, or `daemon.go` creates `internal/acpclient.ClientManager`
2. that adapter constructs `ggcode-acp-go.ClientManager`
3. the adapter maps ggcode permission decisions into the standalone library's decision model
4. delegate calls from `internal/tool` use the adapter's `Get(...)`
5. prompt events/results are converted back into `internal/tool.ACPPromptEvent` and `ACPPromptResult`

That means the delegate tool does not need to know anything about the standalone library directly.

## Protocols and interfaces in play

There are two boundaries here, and they are intentionally different:

| Boundary | Protocol / interface | Owner |
| --- | --- | --- |
| external agent transport | ACP over JSON-RPC 2.0 on stdio | `ggcode-acp-go` |
| ggcode delegate integration | `internal/tool.ACPAgentRegistry` / `ACPAgentClient` | ggcode adapter |

So ggcode does **not** expose `ggcode-acp-go` directly to the rest of the app. It translates it into
the interface shape the existing delegate tool already understands.

## Key adapter responsibilities

`internal/acpclient/manager.go` is intentionally small. It owns:

- debug logger bridging into `internal/debug`
- permission policy adaptation from `internal/permission.PermissionPolicy`
- approval callback adaptation
- prompt event/result conversion into `internal/tool` types

It does **not** reimplement process lifecycle, transport, or discovery. Those now live in
`ggcode-acp-go`.

## Why `internal/acp` still exists

`internal/acp` is still the source of truth for server-side ACP behavior used by `ggcode acp`.

That code remains local because it is coupled to:

- ggcode sessions and projection state
- tool execution and permission UX
- ask_user routing
- auth and host-side protocol decisions
- session persistence rules

Trying to extract those pieces together with the client runtime would have created a much less
reusable library.

## Local development wiring

Right now the root `go.mod` uses a local replace while the two repositories evolve together:

```go
replace github.com/topcheer/ggcode-acp-go => ../topcheer-ggcode-acp-go
```

That keeps local development fast while the library API is still settling.

## Publishing / release flow

Once the library API is stable:

1. tag a release in `topcheer/ggcode-acp-go`
2. remove the local `replace`
3. update ggcode's `require github.com/topcheer/ggcode-acp-go ...` to the tagged version
4. run `make verify-ci`

## Supported discovered ACP targets

The standalone library preserves ggcode's current discovery model and includes:

- `copilot`
- `droid`
- `opencode`
- `ggcode`

`ggcode` is exposed as a normal discovered ACP target via `ggcode acp`.

## Supported CLI command forms

| Agent name | Executed command |
| --- | --- |
| `copilot` | `copilot agent` |
| `droid` | `droid acp` |
| `opencode` | `opencode acp` |
| `ggcode` | `ggcode acp` |
