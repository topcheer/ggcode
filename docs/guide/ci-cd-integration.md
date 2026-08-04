# CI/CD Pipeline Status Integration

ggcode includes a built-in `ci_status` tool that lets the agent check CI/CD pipeline results directly, creating a complete push -> verify -> fix loop without manual user intervention.

## Overview

When an agent pushes code, it needs to know whether CI passed or failed. Without this capability, the user must manually check CI results and relay them back to the agent. The `ci_status` tool closes this gap by leveraging the GitHub CLI (`gh`) to query workflow run status, list recent runs, and retrieve failure logs.

## Prerequisites

- **GitHub CLI (`gh`)** must be installed and authenticated
  - Install: https://cli.github.com/
  - Authenticate: `gh auth login`
- The repository must be hosted on GitHub

## Actions

### `status` (default)
Check the latest workflow run for the current branch.

```
ci_status action=status
```

Output includes:
- Pass/fail/in-progress status
- Workflow name, commit SHA, event type
- Run ID for follow-up queries

### `list`
Show the 10 most recent workflow runs across all branches.

```
ci_status action=list
```

### `logs`
Retrieve failure details from the most recent failed run on the current branch. Shows which jobs and steps failed, plus the last 100 lines of failure logs.

```
ci_status action=logs
```

## Agent Workflow

The system prompt instructs the agent to:

1. Push code (via `git_commit` + `git push`)
2. Wait briefly for CI to start
3. Check status: `ci_status action=status`
4. If CI is in progress, wait and re-check
5. If CI failed, read logs: `ci_status action=logs`
6. Diagnose and fix the failure
7. Push the fix and re-verify

## Limitations

- Currently supports GitHub Actions only (via `gh` CLI)
- Read-only: the agent cannot trigger or cancel workflow runs
- Requires `gh` to be authenticated with sufficient repo permissions
