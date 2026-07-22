# ggcode

> AI coding agent for the terminal. Go codebase with Bubble Tea TUI, multi-provider LLM support, MCP integration, IM adapters, and harness-engineering workflows.

## Quick Reference

| Item | Value |
|------|-------|
| Module | `github.com/topcheer/ggcode` |
| Go version | 1.26.2 (see `go.mod`) |
| Build tag | **`-tags goolm`** required for ALL `go build` / `go test` |
| Current release | v1.3.172 |
| Platform | Linux / macOS / Windows (amd64 + arm64) |

## Validation Commands

```bash
# CI-equivalent check (what pre-commit hook runs)
make verify-ci

# Quick build
go build -tags goolm -o /tmp/ggcode ./cmd/ggcode

# Run tests (use memory limits on shared/CI machines)
GOMEMLIMIT=2GiB GOGC=50 go test -tags goolm -p 1 -parallel 1 -timeout 600s ./...

# Lint
go vet -tags goolm ./...

# Cross-platform build check
CGO_ENABLED=0 go build -tags goolm ./...
```

**Key rule**: Always use `-tags goolm`. Without it, CGO-only packages (e.g. `go-olm`) will fail to compile.

## Major Directories

| Path | Purpose |
|------|---------|
| `cmd/ggcode/` | Main CLI entrypoint, root command, resume picker |
| `cmd/ggcode-installer/` | Go-based binary installer |
| `internal/agent/` | Agent loop, tool execution, autopilot strategist, compaction |
| `internal/tui/` | Bubble Tea terminal UI, panels, slash commands, i18n |
| `internal/provider/` | LLM provider adapters (OpenAI, Anthropic, Gemini), retry, error formatting |
| `internal/config/` | Config schema, vendor/endpoint resolution, built-in vendor defaults, i18n display names |
| `internal/session/` | JSONL session store, debounce, index, checkpoints |
| `internal/context/` | Context manager, token counting, compaction |
| `internal/tool/` | Built-in tools (file edit, search, run_command, browser, etc.) |
| `internal/im/` | IM adapters (QQ, Telegram, Discord, Slack, Feishu, WeChat, etc.) |
| `internal/permission/` | Permission modes, dangerous command detection |
| `internal/harness/` | Harness task engine, worktrees, review/promotion |
| `internal/a2a/` | Agent-to-agent protocol, mDNS discovery |
| `internal/mcp/` | MCP server/client integration |
| `internal/debug/` | Debug logging system (category-based ring buffer) |
| `internal/safego/` | Panic recovery for goroutines |
| `internal/util/` | Shell detection, path helpers, common utilities |
| `mobile/flutter/` | Flutter mobile app (iOS + Android) |
| `desktop/ggcode-desktop-wails/` | Desktop app (Wails: Go backend + web frontend) |
| `desktop/ggcode-desktop/` | Legacy desktop builds (no active go.mod) |
| `docs/` | Documentation, architecture notes, release process |

## Architecture

### Provider System
- Config uses `vendor` / `endpoint` / `model` schema (not old `provider/providers`)
- Built-in vendors: ZAI, Anthropic, OpenAI, Google, Kimi, Aliyun, Ark, MiniMax, MiMo, GitHub Copilot, etc.
- `ResolveEndpointSelection()` resolves active config to `ResolvedEndpoint` with display names
- Built-in vendor/endpoint display names are i18n-aware (`vendor_display_i18n.go`)
- Coding plan providers return 429 for both transient limits AND quota exhaustion — `isQuotaExhaustedError()` distinguishes them

### TUI Layout
- Panels (model, provider, inspector, harness, etc.) open in the main content area
- When any panel is open, conversation is hidden; panel fills full height
- `renderContextBox` forces full height; `renderContextBoxAuto` for compact elements (status bar)
- Composer/input position stays fixed regardless of panel content height

### Error Handling
- `FriendlyError()` — detailed error classification for retry decisions
- `UserFacingErrorLang()` — user-facing messages with i18n (zh-CN/en)
- Both detect quota exhaustion patterns from all coding plan providers
- Non-streaming `Chat()` paths in all 3 providers have `debug.Log` on errors

## Release Process

**Full playbook**: `docs/release-process.md`

Quick checklist:
1. Create `docs/releases/vX.Y.Z.md`
2. Run `cd mobile/flutter && bash scripts/version_sync.sh X.Y.Z` (bumps 4 files)
3. `make verify-ci`
4. `git commit -m "release: vX.Y.Z"` → push main
5. `git tag vX.Y.Z` → push tag
6. Monitor CI, Release, Mobile Release, CodeQL — all must pass

**Do NOT** push tag before mobile version sync. TestFlight/Google Play reject duplicate build numbers.

## Runtime Modes

| Mode | Behavior |
|------|----------|
| `supervised` | Default; asks confirmation for tool calls |
| `plan` | Read-only exploration only |
| `auto` | Safe operations auto-allowed |
| `bypass` | Almost everything allowed |
| `autopilot` | Bypass + autonomous goal-directed execution |

## Coding Conventions

- **Build tag**: All `go build`/`go test` must use `-tags goolm`
- **Debug logging**: Use `debug.Log(category, format, args...)` — never `log.Printf` for diagnostics
- **Goroutine safety**: Use `safego.Recover("name")` or `safego.Go("name", fn)` for all goroutines
- **Circular imports**: `debug` imports `util`; `util` cannot import `debug` — use injectable callback (`SetDebugLogFn`)
- **Panel rendering**: All full-screen panels use `renderContextBox`; compact elements use `renderContextBoxAuto`
- **Error handling**: Don't swallow errors silently — add `debug.Log` on error paths
- **i18n**: TUI uses `tr(lang, key)` system; register catalogs via `registerCatalog(en, zh)` in `init()`
- **Pre-commit hook**: Runs `gofmt`, `go vet`, `go build` on staged files

## Testing

```bash
# CI-safe (unit + Tier 1 integration)
go test -tags "goolm,integration" ./cmd/... ./internal/...

# Full suite (needs API key + external services)
go test -tags 'goolm,integration,integration_local,integration_service' ./...
```

Test memory limits: `GOMEMLIMIT=2GiB GOGC=50` on shared/CI machines.
Use `-p 1 -parallel 1` to avoid OOM on large packages.
