# Tool Macros（工具宏）

> sa-148 · Tool Composition · 对标 ACE（arXiv:2510.04618）学习闭环的"可调用资产"缺口

## 问题

重复型多步只读操作（如发版前检查：`git_status` + `git_log` + 版本文件 `grep`）每次都要 LLM 重新展开逐个调用，或者用户手写 skill markdown。策略 playbook 学到了成功序列却只注入提示词，无法物化为可重放资产——**学与用之间断链**。

## 方案

`macro` builtin：把一段只读工具调用序列**录制一次**，之后**一行调用重放**。

### 动作

| action | 参数 | 说明 |
|---|---|---|
| `define` | `name`, `description`, `steps:[{tool, args}]` | 持久化宏（同名覆盖）。steps 1–8 步；args 支持 `{{1}}`..`{{9}}` 位置占位符 |
| `run` | `name`, `args:[...]` | 替换占位符后按序执行，遇错中止 |
| `list` / `get` / `delete` | `name` | 管理已存宏 |

存储：`.ggcode/macros.json`（工作区共享，cmd_snippet 同款惯例；损坏自动隔离 fail-open）。

### 示例

```json
{"action":"define","name":"release-preflight","description":"发版前检查","steps":[
  {"tool":"git_status"},
  {"tool":"git_log","args":{"count":3}},
  {"tool":"grep","args":{"pattern":"versionName","glob":"*.gradle"}}
]}
```

```json
{"action":"run","name":"release-preflight"}
```

## 安全模型

- 宏只能组合**只读工具**（与 `code_execution` 同一白名单）。`edit_file`/`run_command` 等写操作在 define 时即被拒绝——写操作永远走正常逐调用审批（含 diff 预览），宏无法旁路权限
- 宏不能调用宏（`macro` 不在只读集），递归结构上不可能
- 占位符替换做 JSON 转义，注入值无法逃出字符串字面量
- 输出预算：单步 2KB / 总量 16KB 截断

## 与 playbook 联动

策略 playbook 中某模式命中 ≥3 次后，系统提示会提示模型"该序列可用 macro 固化"——把 ACE 式学习闭环到可执行资产。

## 相关

- [command-snippets.md](command-snippets.md) — shell 命令复用（宏组合的是**工具调用**，snippet 复用的是**shell 命令**）
- [code_execution](../design/) — 即时 JS 沙箱批量调用（每次写代码；宏是命名持久资产）
