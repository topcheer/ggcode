# Decision Audit（决策审计 ledger）

每条工具调用事件已持久化进 hash 链审计 ledger（append-only JSONL），支持事后重放与防篡改校验。

## Ledger 面（既有）

- 位置：`internal/audit/`（`Open/Append/Verify/Close`），事件含 Tool/Status/InputHash/DurationMS/Session/InvariantID/Peer/TaskID
- 防篡改：每条 Entry 携带 `prev_hash`+`hash`（SHA-256 链式，`hashEntry` tagged-optional 字段），`audit.Verify(path)` 全链校验
- 挂点：`internal/agent/audit_ledger.go` `auditToolResult`——含 StatusPolicyDenied/UserDenied/UserApproved/AskTimeout/Cancelled 等审批与执行状态

## Rationale 字段（#decision-audit 新增）

**问题**：模型对重参数写侧工具附带的 `arg_rationale`（"为什么这么做"）在执行边界被 `stripArgRationale` 剥除，从未持久化——事后无法回答"这个改动凭什么这么做"。

**修复**：
- `agent_tool.go`：strip 前用 `extractArgRationale` 抽取（500 rune 截断）暂存至 `Agent.pendingAuditRationale`
- `audit_ledger.go`：下次 `auditToolResult` 将其写入 `Event.Rationale` 并消费清空
- `audit.Event/Entry` 新增 `Rationale` 字段（`rationale,omitempty`）

**后向兼容**：rationale 走 hashEntry **独立 tag-4 条件块**（空值时零扰动）——既有 ledger 全链 Verify 依旧通过。

## 查询

```sh
# 事后审计：哪些事件带 rationale
jq 'select(.rationale) ' ~/.ggcode/audit/*.jsonl   # 按 ledger 实际路径调整

# 全链完整性校验（TUI/CLI 面待 Step 2 扩展）
ggcode audit verify   # 若已接入；否则调 internal/audit.Verify
```

设计参照：tianpan《Decision Provenance in Agentic Systems》2026/04（logs ≠ accountability；decision event 六要素）、HiveMind《Structured audit trails》2026/06（Evidence 引用+hash-linked）。

## 反事实修复腿（Counterfactual Repair）

失败归档此前只回答**哪一步致败**（suspect 工具/文件/CRS）；CausalFlow（arXiv:2605.25338）的修复腿补上**下一步该怎么不同**：

- `PlaybookFailureEntry.Repair`：按工具类确定性生成修复指令（编辑类→"先重读再改+最窄验证"；命令类→"先取全量错误输出再重试、一次只改一个变量"；测试类→"先 -run 单测"等）
- 记录与更新两路均写入；`FailureHintsForPrompt` 注入时带 `→ fix:` 前缀（hint 预算 400→600 补偿）
- 确定性分类器，无 LLM 依赖；见 `internal/agent/counterfactual_repair.go`
