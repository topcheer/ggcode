# Command Cost Hints

## Why

arXiv 2607.27250 ("Do Context Files Help Coding Agents? A Two-Agent Ablation Study on Real Repositories", Khatri 2026; 288 evaluated runs across Claude Code and Codex) ablated context-injection strategies (`none` / `always_on` / `selective`) on real merged PRs with gold-test evaluation. Two findings matter for command execution:

1. Coding conventions and architecture prose in context files had **no detectable correctness effect** on either agent.
2. The **one component with a measurable behavior effect** was an *operational cost warning* — an AGENTS.md note saying "the full test suite takes >20 minutes". Given it, agents cut blind full-suite invocations monotonically (3.67 → 2.44 → 1.67 per task) and wall-clock dropped ~24%. Stripped of the warning, the agent repeatedly ran the slow full suite.

The lesson generalizes beyond context files: **empirical cost feedback changes command selection**. An agent that just watched `make verify-ci` burn 20 minutes should reach for a package-filtered `go test ./internal/agent/ -run ...` on the next iteration — but only if the cost was visible where it plans.

## What

`internal/tool/cmd_cost.go` + wiring in `run_command.go`:

- **Record**: every synchronously completed `run_command` files its wall-clock duration into an in-process ring (last 16 runs per normalized command; timing starts *after* the permission gate so approval waits never inflate it).
- **Surface**: each result ends with a `[cost] took ...` line, plus — once a command averages ≥2 minutes over ≥2 recent runs — an advisory `[cost hint] ... consider a narrower scope (package filter / -run pattern) for faster iteration`.

### Design notes

- **Advisory only.** The command always executes; a hint that blocked would turn a warning into a regression. The paper's effect came from advice, not enforcement.
- **Backgrounded/GUI commands are exempt**: they return before completion, so there is no meaningful duration to record.
- **Normalized keys**: whitespace variants of the same command share one history bucket; distinct commands never inherit each other's cost.
- **In-process, not persisted**: cost data is session-scoped. Machines differ; yesterday's CI hiccup should not advise today.

## Example

```
> go test ./... -tags goolm
... (12 minutes later) ...
[cost] took 12m3s
[cost hint] this command averaged ~11m50s over 3 recent runs - consider a
narrower scope (package filter / -run pattern) for faster iteration
```

The next planning turn sees the hint in-context and scopes down — the exact dose-dependent behavior the ablation measured.
