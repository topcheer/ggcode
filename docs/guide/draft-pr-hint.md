# Draft-PR Hint Gate（完成时未推送分支提醒）

## 概念来源（2025-2026 前沿）

主流 AI 编码 agent 的版本控制闭环对比（`docs/research/rotation-competitor-analysis-2026-07-10.md`）：

- Aider / Claude Code：任务结束自动 commit（ggcode 已有 commit hint gate 覆盖）
- 前沿模式更进一步："auto … draft PR creation on completion"——落在 feature 分支上的工作应以可评审 PR 形式呈现，而不是留在贡献者磁盘上无人知晓

## Gap（sa-223 研究结论）

ggcode 的完成门只走到 commit：feature 分支上已完成提交的工作在 run 结束时静默收尾，用户数天后才发现分支。全库无内置 draft PR 产物（`create_pull_request` 仅作为外部 MCP 工具用例出现在去重测试中）。

## 实施

`internal/agent/draftpr_hint_gate.go`：挂在 commit hint gate 之后（同为 advisory 非阻塞，每 run 一次）。全部条件满足才提示：

1. 本 run 调用过 `git_commit`（agent 已完成版本控制动作）
2. HEAD 在非默认分支（main/master 上绝不建议 PR）
3. 分支有未推送提交（无 upstream 时统计领先默认分支的提交数；`git status -sb` ahead N）

提示内容：push 分支 + `gh pr create --draft` + 变更摘要，并明确"不要自行 merge"。提示只注入消息不执行任何命令——supervised 模式下走正常工具审批。

## 验证

- `go test -tags goolm -run DraftPR ./internal/agent/`（4 探针：未推送 feature 分支触发+单次性 / 无 commit 静默 / main 分支静默 / 已推送静默，真实 git fixture）
- `go build -tags goolm ./...`

纯 Go 逻辑 + git 子进程调用，无 syscall/文件锁/平台分派面，无需交叉编译验证。

## Backlog（同研究，未实施）

- crash 后任务级自动续跑（中高，~120 行）：`run_journal.go:205` 已有检测，缺确认式续跑闭环，挂载 `internal/tui/commands_slash.go:137`
- run report 结构化文档沉淀（低，建议 skill 化）
