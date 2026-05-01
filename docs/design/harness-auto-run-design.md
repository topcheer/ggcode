# Harness Auto-Run Mode Design

## Status

In Progress (v1.1.73). All phases implemented, promote CTA uses single-task PromoteTask.

## Goal

Harness should become a first-class safe execution lane for engineering work. When a repository has opted into harness mode, normal coding requests should automatically enter a tracked harness run instead of letting the primary interactive agent edit the current working tree directly. The task result should remain isolated until it has passed configured verification and explicit review, and only then be promoted back into the current project.

In other words, the desired workflow is:

```text
user asks for engineering work
        |
        v
detect harness-enabled project and route to harness run
        |
        v
create tracked task + isolated workspace/worktree
        |
        v
worker agent implements and validates there
        |
        v
persist evidence: logs, changed files, checks, review state
        |
        v
human/owner approves
        |
        v
promote/merge into current project
```

The current implementation has many of the lower-level pieces, but it is still mostly an explicit control plane. It does not yet provide an automatic "all normal coding work goes through harness" operating mode.

## Current State

### What exists today

The existing harness package is already a real control plane:

- `internal/harness/project.go` discovers and initializes `.ggcode/harness.yaml`, `.ggcode/harness/`, root `AGENTS.md`, context-level `AGENTS.md`, and `docs/runbooks/harness.md`.
- `internal/harness/config.go` models project goal, checks, contexts, run options, execution mode, and worktree mode. Defaults already use `execution_mode: "subagent"` and `worktree_mode: "auto"`.
- `internal/harness/task.go` persists tracked tasks under `.ggcode/harness/tasks`.
- `internal/harness/run.go` queues/runs tasks, builds a harness-specific worker prompt, captures logs, records delivery evidence, and marks completed work as `review_pending`.
- `internal/harness/worktree.go` creates a git worktree per task when possible.
- `internal/harness/check.go` runs structural checks and configured validation commands.
- `internal/harness/delivery.go` captures changed files, diff stats, and check reports.
- `internal/harness/review.go` gates completion through approval/rejection.
- `internal/harness/promotion.go` commits dirty worktree changes and merges the promoted branch into the project root.
- `cmd/ggcode/harness_cmd.go` exposes explicit CLI commands such as `harness init`, `harness queue`, `harness run`, `harness review`, and `harness promote`.
- `internal/tui/commands_harness.go` exposes explicit TUI slash commands such as `/harness run`, `/harness run-queued`, `/harness review`, and `/harness promote`.
- `internal/tui/harness_panel.go` exposes a harness panel for status and manual operations.
- `cmd/e2e_test/harness_e2e_test.go` covers scaffold, queue, checks, monitor, and a gated real-LLM run.

### What is missing relative to the goal

1. **No LLM classifier (4th routing layer).** The 3-layer classifier is sufficient. An LLM-based classifier could improve accuracy for ambiguous prompts.
2. **Limited integration tests for review/promote CTA.** pendingHarnessReview/pendingHarnessPromote Enter/Esc handlers are unit-tested but lack e2e test with real harness flow.

## Distance to Target

Approximate readiness: **98%**.

All core components implemented and wired: config (P1), skills (P2), router (P3), RunService (P4), strict isolation (P5), review/promote CTA with one-key review and promote (P6), daemon auto-run (P4), IM/WebUI mirroring (P6). Remaining: LLM classifier (optional), integration test coverage for review/promote CTA.

## Proposed Architecture

### High-level design

Add a small orchestration layer above the existing agent submission paths:

```text
TUI / pipe / daemon / IM / WebUI input
        |
        v
HarnessRouter
        |
        +-- not enabled / not eligible / user opted out --> normal agent path
        |
        +-- enabled and eligible -----------------------> harness run path
                                                        |
                                                        v
                                  internal/harness.RunTaskWithOptions
                                                        |
                                                        v
                                      BinaryRunner / worker subagent
                                                        |
                                                        v
                                   review -> promotion -> release
```

The router should be deliberately small. It should not replace `internal/harness`; it should decide whether to run harness and then call the existing harness APIs.

### Components

#### 1. Harness project policy

Add a runtime policy object, for example `internal/harness/router.go`:

```go
type AutoRunMode string

const (
    AutoRunOff      AutoRunMode = "off"
    AutoRunSuggest  AutoRunMode = "suggest"
    AutoRunOn       AutoRunMode = "on"
    AutoRunStrict   AutoRunMode = "strict"
)

type RouterPolicy struct {
    Mode AutoRunMode
    RequireWorktree bool
    AutoInit bool
    AutoRunSlashBypass bool
}
```

Recommended semantics:

- `off`: current behavior.
- `suggest`: do not auto-run, but teach the model/TUI to recommend `/harness run`.
- `on`: auto-route eligible engineering prompts into harness when `.ggcode/harness.yaml` exists.
- `strict`: same as `on`, but disallow direct primary-agent mutation in the project root while harness is enabled.

Initial config can live in the existing app config rather than harness YAML:

```yaml
harness:
  auto_run: "on"
  auto_init: false
  require_worktree: true
  direct_write_guard: true
```

`harness.yaml` should remain project-control-plane config. The product-level ggcode config should decide whether the CLI uses harness automatically.

#### 2. Prompt classifier

Add a deterministic classifier first, not an LLM classifier:

- Route to harness when the input is a natural-language engineering task likely to modify code, tests, docs, build config, release files, or repo behavior.
- Do not route to harness for:
  - built-in slash commands,
  - pure chat/explanation questions,
  - model/provider/config/panel actions,
  - explicit shell commands,
  - direct user requests like "just answer, do not modify",
  - `/harness ...` itself.

Start conservative. False negatives are safer than false positives. The user can still type `/harness run`.

Suggested API:

```go
type RouteDecision struct {
    UseHarness bool
    Reason string
    Goal string
    ContextHint string
}

func DecideHarnessRoute(input string, project harness.Project, cfg *harness.Config, policy RouterPolicy) RouteDecision
```

#### 3. Shared harness execution bridge

TUI currently has `runTrackedHarnessGoal(...)`; CLI has `harness run`; pipe/daemon have their own agent setup. Extract a reusable service that can be called from all frontends:

```go
type RunService struct {
    Project harness.Project
    Config *harness.Config
    Runner harness.Runner
}

func (s *RunService) Run(ctx context.Context, goal string, opts RunOptions) (*harness.RunSummary, error)
```

TUI can keep its progress UI, but the actual execution decision should be common. Pipe/daemon/IM/WebUI should not each reinvent harness routing.

#### 4. Strict mutation guard

When `auto_run: strict` or `direct_write_guard: true` is active, the primary agent should not be able to modify the project root directly for eligible engineering work.

Implement this in one of two ways:

1. **Preferred:** route before the normal agent starts, so the primary agent never receives mutating work.
2. **Defense in depth:** add a policy wrapper around mutating tools for the primary agent:
   - `write_file`
   - `edit_file`
   - `multi_edit_file`
   - `notebook_edit`
   - mutating `run_command`
   - `git_add`
   - `git_commit`
   - `git_stash`

The guard should return a clear error:

```text
This project is in harness auto-run mode. Start a harness task instead of modifying the project root directly.
```

Do not apply this guard inside harness worker processes running in task worktrees.

#### 5. Built-in harness skills

Add bundled skills in `internal/commands/bundled.go`. These should be model-visible, non-user-invocable, and prioritized as bundled skills.

Recommended skills:

1. `harness-run`
   - Use when the user asks for code changes, tests, refactors, docs updates, bug fixes, feature work, build changes, release prep, or any engineering task in a harness-enabled project.
   - Instructs the model to invoke the harness run path or tell the user why harness cannot run.
2. `harness-review`
   - Use when a harness task is completed or the user asks whether work can be merged.
   - Instructs the model to inspect delivery report, changed files, logs, and configured checks before approving/rejecting.
3. `harness-promote`
   - Use after review approval when the user asks to merge/apply/combine the harness result.
   - Instructs the model to promote only approved tasks and report conflicts clearly.
4. `harness-diagnose`
   - Use when harness queue/run/worktree/review/promotion fails.
   - Guides the model through monitor, task JSON, logs, delivery reports, and git worktree state.

These skills should complement, not replace, deterministic routing. Skills help the model pick the right workflow; routing enforces the product behavior.

#### 6. Auto-run user experience

For TUI:

- On eligible prompt, show the user's message as usual.
- Immediately add a system/status line:

```text
Harness auto-run: starting isolated task for "<goal>"
```

- Reuse current harness progress rendering.
- On completion, show:
  - task ID,
  - status,
  - workspace/branch,
  - changed files,
  - validation result,
  - delivery report path,
  - next action: review or rerun.

For pipe:

- If harness auto-run is enabled and the prompt is eligible, pipe mode should print a harness run summary and return non-zero when the harness task fails or verification fails.
- Add an explicit bypass flag if needed, for example `--no-harness` or `--harness=off`.

For daemon/IM/WebUI:

- Use the shared router.
- Send concise lifecycle notifications:
  - queued,
  - running,
  - completed,
  - failed,
  - review pending,
  - promotion ready.

## Key Decisions

### ADR-001: Harness auto-run should be a router, not a second agent architecture

**Decision:** Add a router/service layer that calls existing harness APIs instead of duplicating the agent loop.

**Rationale:** `internal/harness` already owns task persistence, worktrees, checks, review, promotion, and monitoring. The missing behavior is deciding when to use it.

**Trade-off:** The router will need careful integration with TUI/pipe/daemon entry points, but this avoids forking provider/tool/session logic again.

### ADR-002: Strict mode should require worktree isolation

**Decision:** For the target behavior, strict harness auto-run should fail if it cannot create an isolated workspace.

**Rationale:** The user's goal is to avoid direct modification of current project logic until validation and merge. Falling back to root undermines that guarantee.

**Trade-off:** Some repositories without valid git/worktree support will need setup before strict mode works. `suggest` or `on` can remain less strict for compatibility.

### ADR-003: Built-in harness skills are guidance, not enforcement

**Decision:** Add bundled harness skills, but do not rely on them as the only mechanism.

**Rationale:** Skills improve model behavior but can be missed, disabled, or truncated from prompt context. A deterministic router is required for product-level safety.

**Trade-off:** There will be two mechanisms to keep aligned: skills explain the workflow; router enforces it.

## Implementation Plan

### Phase 1: Document and expose configuration

Files likely involved:

- `internal/config/config.go`
- `docs/ARCHITECTURE.md`
- `docs/design/harness-auto-run-design.md`
- `cmd/ggcode/root.go`
- `cmd/ggcode/pipe.go`
- `cmd/ggcode/daemon.go`

Tasks:

1. Add a product-level harness config section.
2. Define defaults as safe but non-breaking:
   - `auto_run: "off"` initially.
   - Later consider making new harness-initialized projects default to `suggest` or `on`.
3. Add config validation for allowed enum values.
4. Surface current harness auto-run mode in `/status` or `/harness doctor`.

Acceptance:

- Existing configs load unchanged.
- Invalid harness mode reports a clear config error.
- Users can see whether auto-run is active.

### Phase 2: Add bundled harness skills

Files likely involved:

- `internal/commands/bundled.go`
- `internal/commands/bundled_test.go`
- `cmd/ggcode/root_test.go`

Tasks:

1. Add `harness-run`, `harness-review`, `harness-promote`, and `harness-diagnose`.
2. Mark them bundled, enabled, non-user-invocable.
3. Include precise `WhenToUse` text.
4. Ensure `buildSkillsSystemPrompt` includes them early due to bundled priority.

Acceptance:

- `/skills` shows bundled harness skills.
- System prompt includes harness skills when model invocation is enabled.
- Tests confirm the skills are bundled and cannot be disabled as normal user/project skills.

### Phase 3: Add deterministic harness router

Files likely involved:

- `internal/harness/router.go`
- `internal/harness/router_test.go`
- `internal/tui/commands.go`
- `cmd/ggcode/pipe.go`
- `cmd/ggcode/daemon.go`

Tasks:

1. Implement `RouterPolicy` and `DecideHarnessRoute`.
2. Discover harness project from current working directory.
3. Route only when:
   - harness config exists,
   - auto-run mode is `on` or `strict`,
   - prompt is eligible,
   - no current agent/harness run is active.
4. Return clear reasons for non-routing to aid debugging.
5. Add explicit bypass:
   - slash command: `/harness off for this request` is too stateful; prefer prompt-level command or config.
   - CLI flag: `--no-harness` for pipe mode.
   - TUI command: `/mode` or `/harness auto off|suggest|on|strict`.

Acceptance:

- Unit tests cover coding prompt, pure question, slash command, shell command, explicit no-modification prompt, and uninitialized repo.
- Existing non-harness behavior remains unchanged when auto-run is off.

### Phase 4: Extract shared run service

Files likely involved:

- `internal/harness/run_service.go`
- `internal/tui/commands_harness.go`
- `cmd/ggcode/harness_cmd.go`
- `cmd/ggcode/pipe.go`
- `cmd/ggcode/daemon.go`
- IM/WebUI bridge files if they submit normal prompts.

Tasks:

1. Extract a service wrapper around `RunTaskWithOptions`, `RunQueuedTasks`, and progress hooks.
2. Keep TUI-specific rendering in TUI, but move frontend-neutral execution setup into the service.
3. Ensure pipe and daemon use the same service rather than shelling through CLI commands.
4. Preserve current task persistence and delivery report behavior.

Acceptance:

- `/harness run` and `ggcode harness run` still behave the same.
- Auto-routed TUI prompt creates the same task structure as explicit `/harness run`.
- Pipe auto-run creates a task and prints `Harness run <id>: <status>`.

### Phase 5: Enforce strict isolation

Files likely involved:

- `internal/harness/worktree.go`
- `internal/harness/config.go`
- `internal/permission`
- `internal/tool/builtin.go`
- `internal/tool/*`
- `cmd/ggcode/root.go`
- `cmd/ggcode/pipe.go`

Tasks:

1. Add `RequireWorktree` to run options or derive it from policy.
2. In strict mode, set effective worktree mode to required for harness auto-runs.
3. Prevent root fallback in strict auto-run.
4. Add a mutation guard for primary-agent tools when strict mode is active.
5. Make guard exemptions explicit for harness worker processes.

Acceptance:

- If worktree creation fails in strict mode, the harness task fails before root mutation.
- Direct primary-agent write tools return a harness-mode guard error.
- Harness worker write tools still work inside the task workspace.

### Phase 6: Review and promotion UX

Files likely involved:

- `internal/tui/harness_panel.go`
- `internal/tui/commands_harness.go`
- `internal/im/*`
- `internal/webui/*`
- `internal/harness/review.go`
- `internal/harness/promotion.go`

Tasks:

1. Add post-run CTA:
   - failed -> rerun or inspect log,
   - completed + verification passed -> review,
   - review approved -> promote.
2. Add a concise review summary:
   - task goal,
   - changed files,
   - diff stat,
   - check command results,
   - log path,
   - delivery report path.
3. Mirror review/promote-ready events to IM/WebUI if they are attached.
4. Add commands/skills that make "approve and promote" intentional, not automatic.

Acceptance:

- User can complete the loop from one TUI session: prompt -> auto-run -> review -> promote.
- Promotion never happens before review approval.
- Failed validation cannot be approved without explicit override; if override is desired later, design it separately.

### Phase 7: Tests and release hardening

Files likely involved:

- `internal/harness/router_test.go`
- `internal/commands/bundled_test.go`
- `internal/tui/*harness*_test.go`
- `cmd/e2e_test/harness_e2e_test.go`
- `cmd/e2e_test/*worktree*_test.go`

Test coverage:

1. Router unit tests.
2. Built-in skill registration tests.
3. TUI auto-route test: plain coding prompt starts harness run instead of normal agent.
4. Pipe auto-route test with fake runner or isolated provider.
5. Strict worktree failure test.
6. Direct write guard test.
7. End-to-end no-LLM lifecycle tests for init/queue/review/promote where possible.
8. Real-LLM e2e remains opt-in with environment key.

Acceptance:

- `go test -tags=!integration ./...` passes.
- `go vet ./...` passes.
- `gofmt` is clean.
- Existing explicit harness commands remain backward compatible.

## Open Questions

1. Should new `ggcode harness init` projects write app-level config to enable `harness.auto_run`, or should auto-run remain a user/global preference?
2. Should `auto_run: on` allow root fallback while `strict` forbids it, or should all auto-run modes require isolation?
3. Should review approval require human action only, or can a model-assisted review propose approval while still requiring user confirmation?
4. Should auto-promote ever exist? The recommended answer is no for now; keep review/promote explicit.

## Recommended Rollout

1. Ship built-in harness skills first. This is low risk and immediately improves model behavior.
2. Add router in `suggest` mode and expose diagnostics.
3. Enable `on` mode behind explicit config.
4. Add strict mutation guard only after router behavior is stable.
5. Consider making `harness init` offer to enable auto-run once strict mode is proven.

## Success Criteria

The target is reached when all of these are true:

- A harness-enabled project can opt into auto-run mode.
- A normal coding request in TUI enters a harness task without the user typing `/harness run`.
- The current project root is not modified by the primary agent in strict mode.
- The task runs in an isolated worktree or fails safely.
- The task records logs, changed files, verification result, and delivery report.
- Successful tasks become review-pending, not automatically merged.
- Only approved tasks can be promoted into the current project.
- The model sees built-in harness skills and consistently explains/uses the harness workflow.
- Pipe/daemon/IM/WebUI either share the same router or explicitly document why they are excluded.

## Summary

Harness is already more than halfway to the desired model. The existing implementation has the task engine, worktree isolation, verification evidence, review, promotion, and UI control plane. The missing pieces are the product-level defaults and enforcement layer: automatic prompt routing, built-in harness skills, strict mutation guards, and a shared execution bridge across entry points.

The recommended implementation path is incremental: first add bundled skills, then add a deterministic router, then enforce strict isolation, and finally improve review/promote UX. This keeps the current harness engine intact while turning it into the default safe lane for engineering changes.
