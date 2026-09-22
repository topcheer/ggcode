# Watch Mode

`ggcode watch` turns any editor into a trigger surface for the agent. While
watch mode is running it polls your project for files and picks up `@ggcode`
annotations you leave in code comments — no plugin, IDE extension, or chat
message required.

## Usage

```bash
ggcode watch                 # watch the current directory
ggcode watch --interval 1s   # faster polling
ggcode watch --bypass        # auto-approve tools for act-mode runs
```

## Annotation syntax

Add a line containing the `@ggcode` token anywhere in any project file:

| Annotation | Effect |
|------------|--------|
| `// @ggcode! fix the nil check below and add a test` | **act** — the agent performs the task and may edit files / run commands |
| `// @ggcode? why is this function O(n^2)` | **ask** — the agent explains only; no file modifications |
| `# @ggcode refactor this loop` (bare token) | treated as **act** |

The token works in any language's comments (`//`, `#`, `--`, `/* */`, ...).

## Semantics

- Each new annotation is dispatched as a non-interactive pipe run
  (`ggcode -p "<prompt>"`) in the watched directory, so it uses your normal
  config, provider, tools, and project memory. Output is streamed back into
  the watch terminal.
- **Baseline rule**: annotations that already exist when watch mode starts are
  ignored. Only annotations you add *while* watch is running fire.
- **Fire-once**: a given instruction in a file fires once even if unrelated
  edits above it shift the line number. Editing the instruction text creates a
  new request and fires again.
- Files are scanned via `git ls-files` (respects `.gitignore`); binary files,
  files over 1 MiB, and dot-directories (`.git`, `.ggcode`, ...) are skipped.
- When several annotations land while a run is in flight they queue and
  execute one at a time (queue cap 16, oldest dropped when full).
- Ctrl+C stops watch mode and cancels the in-flight run.

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--interval` | `2s` | file poll interval (minimum 250ms) |
| `--timeout` | `10m` | per-run timeout |
| `--bypass` | off | forward `--bypass` to spawned act-mode runs so tools run without permission prompts |

## Tips

- Use `@ggcode?` for quick "explain this" questions while reading code, and
  `@ggcode!` for drive-by fixes without leaving your editor.
- Combine with `--bypass` only in workspaces you trust: bypass skips
  permission prompts for the spawned runs.
- Watch mode pairs well with CI-style review flows: annotate a file, let the
  agent work, then inspect the resulting diff in your editor.
