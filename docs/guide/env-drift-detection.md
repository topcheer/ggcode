# Environment Variable Drift Detection

ggcode automatically detects when environment variables documented in your project's `.env.example` (or `.env.template`) file are missing from the local environment, preventing cryptic build/test failures.

## How It Works

When an agent run starts, ggcode:

1. Looks for `.env.example`, `.env.template`, or `.env.sample` in the project root
2. Parses the file to extract expected environment variable names
3. Checks whether those variables are set in `.env`, `.env.local`, or the shell environment (`os.Environ`)
4. If critical variables are missing, injects a concise advisory so the agent knows commands may fail

This is a **zero-LLM-cost** check (deterministic file parsing, no model involvement). It fires at most once per run and is non-blocking.

## Example

Given `.env.example`:
```
DATABASE_URL=postgresql://localhost:5432/mydb
API_KEY=your-api-key-here
REDIS_URL=redis://localhost:6379
```

If you only have `DATABASE_URL` set in your `.env`, the agent receives:

```
[env-drift] Warning: API_KEY, REDIS_URL are defined in .env.example but not set
in the local environment or .env file. Commands depending on these vars may fail.
Set them in .env or inform the user.
```

The agent can then proactively inform the user or set placeholder values before running commands that would fail.

## Supported File Types

| Role | Files (priority order) |
|------|----------------------|
| Template (expected vars) | `.env.example`, `.env.template`, `.env.sample` |
| Actual (runtime vars) | `.env`, `.env.local`, shell environment |

## Features

- **Comment handling**: Lines starting with `#` are skipped
- **Export syntax**: Supports both `VAR=value` and `export VAR=value` syntax
- **Inline comments**: `VAR=value # comment` is parsed correctly
- **Empty values**: Variables with empty values in `.env` are treated as unset
- **Cross-run caching**: Results are cached for 5 minutes to avoid redundant file reads
- **Large var counts**: When more than 10 vars are missing, the list is truncated with a summary count

## Competitor Comparison

| Product | Env Drift Detection |
|---------|-------------------|
| Claude Code | No (manual hooks only) |
| Cursor | No |
| Devin | No |
| OpenHands/Cline | No |
| Aider | No |
| **ggcode** | **Automatic, zero-config** |

Missing-env is one of the most common causes of silent build/test failures in AI coding agents. ggcode is the first to detect this proactively.
