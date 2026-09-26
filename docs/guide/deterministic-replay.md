# Deterministic Replay（确定性重放）

对标前沿概念：*Deterministic Replay for AI Agent Systems*（arXiv:2607.16200，2026）与 AgentReplay 类工具的核心思想——把一次 agent 运行的工具调用记录成结构化 trace，随后**零 LLM 推理**地确定性重放并 diff 结果。

## 是什么

`ggcode replay <session-id>` 读取指定 session 的 JSONL 记录，提取其中全部 `tool_use`/`tool_result` 对，然后**只重新执行只读工具调用**（`read_file`、`multi_file_read`、`grep`、`glob`、`list_directory`、`search_files`、`code_search`、`code_health`），把当前 workspace 的实时结果与录制时的结果逐条对比。

- **零 token 成本**：全程不调用 LLM，trace 完全来自 session 文件。
- **确定性**：同一输入必得同一 verdict；模型被移出循环。
- **安全**：变更类工具（`run_command`、`edit_file` 等）永远不会被重放执行，只报告为 `SKIP`。

## 典型用途

1. **审计**："这次运行到底做了什么？"——有序、可检视的完整工具调用 trace。
2. **漂移检测**："它当时的观察现在还成立吗？"——例如部分修复之后，重放当时失败的检查，看哪些已通过、哪些仍在漂移。
3. **CI 复验**：退出码 1 表示检测到漂移，可直接接进流水线做"历史结论是否仍成立"的回归闸门。

## 输出格式

```
replay 20260716-180903-c595c09d57db900b: 692 steps -- 4 match, 15 drift, 673 skipped
[ 75] multi_file_read  DRIFT    description=读 chat.go 全部 7 处引用段  (+8/-8 lines, first diff: line 14)
[ 86] read_file        MATCH    description=读 zz_issue1181_test.go 构造模式 path=...
[  1] run_command      SKIP     command=... (mutating tool, not replayed)
```

状态含义：

| 状态 | 含义 |
|------|------|
| `MATCH` | 实时结果与录制一致（忽略行尾空白等噪声） |
| `DRIFT` | 内容不同（含 +N/-N 行统计与首个分歧行号）或 error 标志翻转 |
| `EXEC-ERR` | 重放执行本身失败 |
| `SKIP` | 变更类工具 / 未注册工具 / 无录制结果，不重放 |

## 参数

| 参数 | 说明 |
|------|------|
| `--workdir <dir>` | 重放工具调用的工目录（默认当前目录） |
| `--limit <n>` | 只重放前 N 步（0 = 全部） |
| `--tools a,b,c` | 覆盖默认只读白名单 |
| `--json` | 输出机器可读 JSON 报告 |

退出码：`0` 无漂移；`1` 检测到漂移或执行错误；`2` 参数/加载错误（cobra 默认）。

## 实现

核心逻辑在 `internal/replay`（trace 提取、重放器、噪声分层归一化、行多重集 diff），CLI 接线在 `cmd/ggcode/replay.go`。
