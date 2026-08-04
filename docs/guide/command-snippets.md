# Command Snippets

## What Command Snippets Are

Command snippets are a persistent, project-scoped library of reusable shell commands. The agent saves commands it discovers during a session (build, test, deploy, debug) and retrieves them in future sessions -- avoiding the need to re-discover project-specific commands from scratch.

## Storage

Snippets are stored in `<project>/.ggcode/cmd-snippets.json`. This file is git-ignored and local to each developer's workspace.

## Using the `cmd_snippet` Tool

The `cmd_snippet` tool supports five actions:

### Save a Command

Store a new command or update an existing one:

```
cmd_snippet(action="save", name="build-go", command="go build -tags goolm ./...", description_field="Build the Go project with goolm tag", tags=["build", "go"])
```

### List All Snippets

```
cmd_snippet(action="list")
```

### Get a Specific Command

Retrieve a command by name. This increments the usage counter for relevance tracking:

```
cmd_snippet(action="get", name="build-go")
```

### Search for Commands

Search across name, command, description, and tags with relevance scoring:

```
cmd_snippet(action="search", query="build")
```

### Delete a Command

```
cmd_snippet(action="delete", name="old-deploy-cmd")
```

## How It Differs from Skills

| Feature | Skills | Command Snippets |
|---------|--------|-----------------|
| Content | Markdown workflow instructions | Shell commands |
| Scope | Global or project | Project only |
| Purpose | Guide agent through task patterns | Store executable commands |
| Invocation | `skill` tool | `cmd_snippet` tool |

## Competitor Comparison

- **Claude Code**: `.claude/commands/` directory for custom slash commands
- **Cursor**: Rules and custom command snippets
- **Aider**: `aider.conf.yml` command aliases
- **ggcode**: Project-scoped JSON store with tags, search, and usage tracking

## Limits

- Maximum 200 snippets per project (oldest evicted)
- Maximum 120 characters for command names
- Maximum 2000 characters for commands
- Maximum 8 tags per snippet
