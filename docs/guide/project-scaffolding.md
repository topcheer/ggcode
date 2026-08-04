# Project Scaffolding

The `scaffold_project` tool generates a complete multi-file project skeleton (Go, TypeScript/Node, Python, or Rust) in a single tool call, eliminating the need for 8-15 sequential `write_file` calls.

## Why

When starting a new project or module, the agent would previously create files one at a time -- each call consuming a full LLM round-trip. This tool collapses that into one deterministic operation with best-practice defaults.

## Supported Languages

| Language | Files Generated |
|----------|----------------|
| Go | `go.mod`, `cmd/<name>/main.go`, test file, `Makefile`, `.gitignore`, `README.md`, CI workflow, optional `Dockerfile` |
| TypeScript | `package.json`, `tsconfig.json`, `src/index.ts`, test file, `.gitignore`, `README.md`, CI workflow, optional `Dockerfile` |
| Python | `pyproject.toml`, `<pkg>/__init__.py`, `<pkg>/main.py`, test file, `.gitignore`, `README.md`, CI workflow, optional `Dockerfile` |
| Rust | `Cargo.toml`, `src/main.rs`, `src/lib.rs`, integration test, `.gitignore`, `README.md`, CI workflow, optional `Dockerfile` |

## Usage

```
scaffold_project(
  language: "go",
  project_name: "my-service",
  output_dir: "/path/to/project",
  options: {
    module_path: "github.com/user/my-service",  // Go only
    ci: true,          // default: true
    docker: false      // default: false
  }
)
```

## Safety

- **Never overwrites** existing files -- pre-existing files are skipped with a "skipped" status.
- **Sandbox enforced** -- all paths are validated against the sandbox policy.
- **Plan mode** -- the tool is blocked in plan mode (it writes files).

## Output

Returns a JSON manifest with per-file results:

```json
{
  "summary": "Scaffolded go project \"my-service\": 8 files created",
  "language": "go",
  "project_name": "my-service",
  "output_dir": "/path/to/project",
  "total": 8,
  "created": 8,
  "skipped": 0,
  "files": [
    {"path": "go.mod", "status": "created"},
    {"path": "cmd/my-service/main.go", "status": "created"},
    ...
  ]
}
```
