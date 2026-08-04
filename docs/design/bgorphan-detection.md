# Orphaned Background Command Detection

## Problem

AI coding agents frequently start background commands (dev servers, test watchers, file watchers, long-running builds) via `start_command` and then forget to check their output in subsequent iterations. This leads to:

- **Silent failures**: A dev server crashes or a build errors, but the agent never reads the output and continues making assumptions
- **Wasted iterations**: The agent works on code changes without verifying that the background process is healthy
- **Orphaned processes**: Background commands consume resources after the agent has moved on to other tasks
- **Missed signals**: Streaming output may contain warnings, deprecations, or errors critical to the task

No major competitor (Claude Code, Cursor, OpenHands, Devin, Aider) detects this pattern. They track process lifecycle but don't proactively nudge the agent to check forgotten commands.

## Solution

A deterministic, zero-LLM-cost agent loop health check that tracks `start_command` jobs and detects when their output hasn't been read for 2+ iterations.

### How it works

1. **Tracking**: When `start_command` executes, the job_id and metadata are registered in `bgOrphanState`
2. **Checking**: When `read_command_output`, `wait_command`, or `write_command_input` is called for a job_id, its "last checked" timestamp is updated
3. **Detection**: At each iteration start, any job whose output hasn't been checked for >= 2 iterations triggers a guidance injection
4. **Cleanup**: `stop_command` removes a job from tracking

### Thresholds

| Parameter | Value | Rationale |
|-----------|-------|-----------|
| `bgOrphanThreshold` | 2 iterations | Give the agent one natural iteration before intervening |
| `bgOrphanMaxInjections` | 3 per run | Prevent context flooding in autopilot with many bg commands |
| `bgOrphanMaxJobs` | 20 | Prevent unbounded memory growth |

### Interaction with existing systems

- `run_command`: Synchronous, completes immediately -- NOT tracked
- `start_command`: Asynchronous -- tracked as a background job
- `read_command_output`/`wait_command`/`write_command_input`: Clears the "unchecked" state
- `stop_command`: Removes job from tracking
- Convergence lock: Complementary -- convergence lock checks post-verification drift; bg orphan checks unchecked background work

## Files

- `internal/agent/bgorphan_detect.go` -- core detection logic
- `internal/agent/bgorphan_detect_test.go` -- 14 tests covering all paths
- `internal/agent/agent.go` -- wiring into the agent loop (struct field, init, reset, tool call recording, iteration check)
