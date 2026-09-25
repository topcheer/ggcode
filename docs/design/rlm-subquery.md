# RLM Sub-Queries: Recursive LLM Calls Inside code_execution

- **Round**: r86
- **Concept source**: Zhang & Khattab, *Recursive Language Models (RLMs)*, arXiv:2512.24601 (Dec 2025) — https://arxiv.org/abs/2512.24601 ; author's write-up: https://alexzhang13.github.io/blog/2025/rlm/
- **Related prior art in-tree**: sa-19 MCP code-execution bridge ("Code execution with MCP", Anthropic), tool output offloading (spill files)

## Gap

RLM treats a long prompt as part of an *external environment* that the model can
programmatically examine, decompose, and **recursively call itself over snippets
of**. ggcode already had the first half ("context as environment"): the
code_execution sandbox keeps large tool results as JS variables so only
`console.log` summaries reach the window. The recursion half was missing: when a
task genuinely needs model-grade comprehension over an oversized artifact (a
200KB build log, a 5MB JSON dump), the only options were (a) pulling slices
into the main context window chunk by chunk, or (b) crude JS heuristics. Both
flood the context or lose semantics.

## Implementation

`tools.subquery(prompt, context) -> answer` inside the code_execution sandbox:

| File | Change |
|------|--------|
| `internal/tool/subquery.go` | `SubQueryFn` type, prompt composition (isolation framing + `<context>` block), size caps, `NewProviderSubQueryFn` provider adapter |
| `internal/tool/code_execution.go` | `SubQueryFn` field + late-bound `SetSubQueryFn`; sandbox injection of `tools.subquery` with budgets; tool description |
| `internal/tool/builtin.go` | Register `*CodeExecution` (pointer) so runtimes can late-bind |
| `internal/agentruntime/interactive_core.go` | Late-bind to the interactive runtime's provider (`#1592-B` closure pattern) |
| `cmd/ggcode/acp.go` | Late-bind to the ACP session provider |
| `internal/tool/subquery_test.go` | 7 tests: binding, prompt composition, budget cap, truncation marker, error propagation, arg validation, nil-set no-op |

## Budgets (recursion guard)

A runaway JS loop must not become an unbounded LLM-spend loop:

- **At most 8 sub-LLM calls per `code_execution` run** (`maxSubQueriesPerRun`)
- Context snippet capped at **64KB** (`maxSubQueryContextBytes`), rune-safe with an explicit `[context truncated: ...]` marker so the sub-LLM never treats a partial snippet as complete evidence
- Task prompt capped at **8KB** (`maxSubQueryPromptBytes`)
- Per-call deadline is the sandbox's own exec context (`codeExecTimeout`, 30s) — a hanging provider chat cannot outlive the tool call that requested it
- Not bound (`SubQueryFn == nil`) → `tools.subquery` is simply absent; models get a clean `undefined` signal instead of a runtime failure

## Protocol compatibility

The sub-query is a plain non-streaming `provider.Chat` call with a single user
message. It introduces no new protocol shapes between `tool_calls` and
`tool_results`; the sandbox result resolves as an ordinary string, identical to
other sandbox tool calls.

## Usage sketch

```js
const r = await tools.read_file({path: "/tmp/build.log"});   // 200KB, stays in JS
const lines = r.split("\n");
const mid = lines.slice(4000, 8000).join("\n");              // pick a window
console.log(await tools.subquery("List test failures with reasons", mid));
```
