# Copilot instructions for `ggcode`

## Build, test, and lint commands

- Build the CLI binary: `make build` or `go build -o bin/ggcode ./cmd/ggcode`
- CI-style build smoke test: `go build -o /tmp/ggcode ./cmd/ggcode`
- Full test suite from the Makefile: `make test`
- CI-style test run: `go test -tags=!integration ./...`
- Run a single test: `go test ./cmd/ggcode -run TestRootHelpUsesCompactLayout`
- Run tests in one package: `go test ./internal/tool`
- Lint from the Makefile: `make lint` (`go vet ./...`)
- CI formatting check: `test -z "$(gofmt -l .)"`

Integration tests live in `internal/provider/integration_test.go` and depend on `ZAI_API_KEY` (or `GGCODE_ZAI_API_KEY`). CI skips them via `-tags=!integration`.

## High-level architecture

- `cmd/ggcode/main.go`, `cmd/ggcode/root.go`, and `cmd/ggcode/pipe.go` are the main entry points. `root.go` assembles the interactive TUI path, while `pipe.go` assembles non-interactive execution. Both paths intentionally follow the same sequence: load config, resolve the active endpoint, build a permission policy, register built-in tools, attach MCP/plugin tooling, load project and auto memory, register skills, then construct `agent.NewAgent`.
- `internal/config` owns the vendor/endpoint model. The runtime resolves `vendor` + `endpoint` into a `ResolvedEndpoint`, and `internal/provider` turns that into a protocol adapter (`openai`, `anthropic`, or `gemini`). Most model/vendor work should happen in config resolution, not scattered through callers.
- `internal/tui` owns Bubble Tea UI state, approvals, slash-command UX, provider/MCP panels, and session resume/save. `internal/agent` and `internal/context` own the tool loop, streaming, and context compaction.
- Built-in tools are registered centrally in `internal/tool/builtin.go`. MCP servers are merged and connected through `internal/mcp` and `internal/plugin`. Markdown-defined skills and custom slash commands are loaded through `internal/commands`.
- The Go CLI is the source of truth. `cmd/ggcode-installer`, `npm/`, and `python/` are release-backed installers/wrappers that download or launch the built binary rather than reimplementing core behavior.

## Key conventions

- Keep `run` in `cmd/ggcode/root.go` and `RunPipe` in `cmd/ggcode/pipe.go` behaviorally aligned. Changes to tool registration, prompt assembly, memory loading, MCP/plugin wiring, or provider setup usually need to be applied in both places.
- Project prompt memory is file-driven, not hard-coded. `GGCODE.md`, `AGENTS.md`, `CLAUDE.md`, and `COPILOT.md` are loaded from `~/.ggcode`, walked parent directories, and nested subdirectories via `internal/memory/project.go`.
- Skills and legacy custom slash commands are markdown-backed. Loader precedence matters: `~/.agents/skills` -> `~/.ggcode/skills` / `~/.ggcode/commands` -> project `.ggcode/skills` / `.ggcode/commands`, with later entries overriding earlier ones.
- The `internal/context` package documents a required import alias: consuming packages should import it as `ctxpkg` to avoid colliding with the standard library `context` package.
- Do not treat `make lint` as the whole lint contract. The repository’s CI also enforces `gofmt` cleanliness with `test -z "$(gofmt -l .)"`.
