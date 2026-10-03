# Declarative Behavior Invariants (.ggcode/invariants.json)

ggcode 支持在文件中声明**行为不变式**（behavior invariants），运行时在每次工具调用前做**确定性断言**。与提示级约束（概率性，可能被模型忽略）不同，不变式由代码强制执行，违规可直接阻断调用。

## 文件位置

| 作用域 | 路径 | 优先级 |
|--------|------|--------|
| 用户级 | `~/.ggcode/invariants.json` | 低 |
| 项目级 | `<项目根>/.ggcode/invariants.json` | 高（同 `id` 覆盖用户级） |

文件不存在时引擎完全惰性，行为零变化（默认状态）。

## 格式

```json
{
  "invariants": [
    {
      "id": "no-delete-env",
      "on_tools": ["file_ops"],
      "op": "delete",
      "path_glob": "**/.env*",
      "mode": "block",
      "message": "never delete env files"
    },
    {
      "id": "only-own-products",
      "on_tools": ["file_ops"],
      "op": "delete",
      "created_by_run": true,
      "mode": "block",
      "message": "only delete files this run created"
    }
  ]
}
```

### 字段

| 字段 | 类型 | 说明 |
|------|------|------|
| `id` | string | 唯一标识（必填）；出现在违规消息与审计条目中 |
| `on_tools` | []string | 工具名，支持首/尾 `*` 通配（`file_*`、`*_file`）；空 = 所有工具 |
| `op` | string | 操作类别谓词：`delete` / `write` / `mkdir` / `move` / `exec`；空 = 任意 |
| `path_glob` | string | 目标路径 glob，支持 `**` 跨目录与裸名（`.env` 匹配任意目录下的 `.env`）；空 = 任意目标 |
| `created_by_run` | bool | 要求谓词：`true` = 目标必须是本次运行产出（写入过）的文件；`false` = 必须不是 |
| `mode` | string | `block`（拒绝调用）或 `warn`（放行但追加警示）；未知值安全降级为 `warn` |
| `message` | string | 违规时展示给 agent 的说明 |

所有谓词**合取**（同时满足才算匹配）；`created_by_run` 是例外——它是要求谓词，不满足即违规。

### 匹配的工具与目标

- 目标路径提取：`write_file`/`edit_file`/`multi_edit_file` 的 `file_path`、`file_ops` 各操作的 `source`/`destination`、`batch_replace` 的 `files[0]`
- op 分类：文件写工具=`write`；`file_ops` 按操作类型=`delete`/`mkdir`/`move`；`run_command`/`start_command` 等=`exec`；只读工具不参与
- run 产出登记：每次成功的写类调用自动登记目标路径，供 `created_by_run` 比对

## 审计集成

开启 `GGCODE_AUDIT_LEDGER` 时，block 级拒绝以 `invalid` 状态入账，条目携带 `invariant_id` 字段，可通过审计链追溯是哪条不变式拒绝了哪个调用。

## 设计边界

- 引擎是**确定性护栏**，不替代权限系统（permission）或对话层约束提醒（constraint_amnesia）
- 坏 JSON 文件降级为该作用域无规则（另一作用域不受影响），不会阻断会话
- 与 AgentSpec（arXiv 2505.04447）的声明式 spec + 确定性运行时监视器思路对齐
