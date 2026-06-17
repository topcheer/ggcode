# Agent-to-Agent (A2A) Protocol

A2A lets ggcode instances communicate and delegate tasks across workspaces and projects.

## Overview

In a monorepo with multiple services, each project can run its own ggcode instance. A2A allows these instances to talk to each other — delegating work, requesting code reviews, or executing commands remotely.

## Starting an A2A Server

ggcode automatically registers as an A2A agent when **daemon mode** is active. No manual server startup is required.

## Discovering Agents

List all connected ggcode instances:

```
a2a_discover
```

## Sending Tasks

Delegate work to another ggcode instance by project name:

```
a2a_send_task --target order-service --skill full-task --message "Add pagination to the orders API"
```

## Remote Execution

Call a specific skill on a remote instance:

```
a2a_remote --target user-service --skill code-review --message "Review the latest commit"
```

Available skills: `code-edit`, `file-search`, `command-exec`, `git-ops`, `code-review`, `full-task`.

## Configuration

A2A auth is API-key based and configured in `~/.ggcode/ggcode.yaml`:

```yaml
a2a:
  host: 0.0.0.0:7878
  auth:
    api_key: your-secret-key
```

## Security

- All communication is authenticated via API keys.
- No unauthenticated access is permitted.
- API keys are stored securely in `keys.env`, never in plaintext config.
