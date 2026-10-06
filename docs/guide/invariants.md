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

## Workflow Spec（有状态工作流规范）

不变式引擎是无状态的**单调用**规则；`workflow-spec.json` 补上**有状态的步骤层**（r26，Lean4Agent 启发）：声明步骤顺序（前置条件）、产物锚定（哪个文件证明步骤发生过）与收尾核对。

```json
{
  "steps": [
    {"id": "write-tests", "artifact_glob": "internal/**/*_test.go"},
    {"id": "run-tests", "requires": ["write-tests"], "mode": "warn", "on_commands": ["go test*"]},
    {"id": "release", "requires": ["run-tests"], "mode": "block", "on_commands": ["git push*", "git tag*"]}
  ]
}
```

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | string | 步骤标识（唯一） |
| `artifact_glob` | string | 产物路径 glob；本 run 内任一写类调用产出匹配路径即视为**完成** |
| `on_commands` | []string | 命令 glob（`git push*` 前缀 / `*test*` 包含 / 精确）；空 = 仅产物节点，不拦命令 |
| `requires` | []string | 前置步骤 id 列表，须全部完成才放行 `on_commands` 匹配的命令 |
| `mode` | string | `block`（默认，拒绝调用）或 `warn`（放行但追加反例警示） |
| `message` | string | 违规时附加说明 |

行为：

- **前置门**：执行匹配某步骤 `on_commands` 的 `run_command` 时检查 `requires`；未满足则 block/warn，消息含**反例步**（"release 需要 run-tests，但尚无产物匹配 ..."）
- **产物锚定完成**：写类调用成功后按 `artifact_glob` 自动登记完成——不信任口头"这步做完了"
- **收尾核对**：agent 声明任务完成前，未产生产物的声明步骤会被列出并要求核实（与 fulfillment gate 同点注入）
- 双作用域合并（`~/.ggcode/` + 项目 `.ggcode/`）、坏 JSON 降级惰性、无文件零行为变化——均与不变式一致
- block 级拒绝以 `workflow:<step_id>` 作为 `invariant_id` 入审计链

纯门控节点（无 `artifact_glob`，如上例 `run-tests`）永不单独"完成"——它只作为被依赖方时要求配 `artifact_glob`，或仅用于把警示挂在命令前。定理证明级形式验证（Lean/TLA+）不在本层范围。
