# sa-19: MCP Code-Execution Bridge (沙箱内 MCP 工具桥接)

## 研究背景 (Frontier Research)

**Anthropic Engineering, "Code execution with MCP: Bringing more agentic efficiency to MCP servers" (2025-12)**

前沿共识:将 MCP 工具暴露为代码执行沙箱内的 JS 函数是 MCP 规模化的关键方向:

1. **上下文经济**: 20 个工具 × 5K tokens 的 schema ≈ 100K tokens(占 200K 窗口的一半)。
   沙箱内代理调用让中间结果留在代码环境里,只有模型写的 summary 进入上下文
   (官方示例:单次 sandbox 运行内完成 grep → 摘要,对比逐调用路径节省 ~98% tokens)。
2. **渐进披露 (Progressive Disclosure)**: schema 按需获取(`mcpDescribe(name)`)而非
   全量预载 —— 与 sa-96/sa-99 的 schema 级披露互补:那些方案优化"请求里带哪些定义",
   本方案把"工具怎么用、结果怎么算"都移到沙箱里。
3. **多步工作流单轮完成**: for/map/filter 链式调用 N 个工具只需 1 次 tool_use 往返,
   且调用预算可控。

社区佐证: brightbean.io "MCP Simplified"(代理签名提及), particula.tech MCP 模式分析,
Cursor/Cline 类产品的 code-mode 讨论 (2025 Q4)。

## Gap 分析

- 现有 `code_execution` 沙箱仅桥接 10 个静态只读内置工具 (`readOnlyToolNames`),
  MCP 工具完全不可从沙箱访问 —— 每次 MCP 调用都是独立的 tool_use 往返,
  大结果直接进上下文,与本仓已有的 50KB MCP 结果上限 (#365 fill-aware cap) 叠加浪费。
- sa-96 (Tool-Search Tool) / sa-99 (structured_tool_defs) 解决的是 **schema 侧**披露;
  沙箱侧为空白。
- 安全边界已存在: `MCPServerConfig.ReadOnly` → `NewReadOnlyAdapter` 在注册期按
  write-shape 名单拦截写工具。这个服务级闸门正好可以复用为沙箱暴露条件。

## 实施

| 文件 | 变更 |
| --- | --- |
| `internal/tool/tool.go` | 新增可选接口 `tool.SandboxSafe` (非侵入,不实现即不暴露) |
| `internal/mcp/adapter.go` | `mcpTool.SandboxSafe() bool` 返回服务级 `readOnly` 标志 |
| `internal/tool/code_execution.go` | ① 注入循环重构为 `injectSandboxTool` 工厂 (行为不变);② `mcp__` 前缀 + `SandboxSafe()==true` 的 MCP 工具注入沙箱;③ `tools.mcpList()` 枚举 + `tools.mcpDescribe(name)` 按需 schema;④ 每 run 30 次 MCP 调用预算;⑤ Description() 文档 |

## 安全设计

- **默认关闭**: 仅 `read_only: true` 的 MCP server 的工具进入沙箱;写能力 server
  的工具必须继续走直接 tool_call 路径(权限流/UI diff/approval 完整保留)。
- 沙箱内对未暴露工具的调用表现为 JS TypeError,`mcpList()` 会标注
  `sandbox:false` 引导模型改走直接调用。
- **预算**: 每 run 最多 30 次 MCP 调用,防止失控循环在 30s 窗口内打满 MCP server。
- **审计**: 所有沙箱内 MCP 调用仍记入现有 `toolCalls` 轨迹 (含 budget 拒绝记录)。
- `AvailabilityChecker` 为 false 的工具不注入;`mcpDescribe` 仅返回 schema 文本
  (纯只读信息),对任何 `mcp__` 工具可用,支持渐进披露。

## 协议兼容性

纯工具内部实现:不改变 tool_calls/tool_results 消息结构、不插入 user 消息、
不触碰 Claude Code prompt 协议。Provider 侧唯一可见变化是 `Description()` 文本,
用于教模型新能力,与工具定义 JSON 无冲突。

## 测试

`internal/tool/code_execution_mcp_test.go` 5 项:
mcpList 标志/按需 schema、非 MCP 名拒绝、安全工具沙箱调用+结果留在沙箱、
写工具不可调用且零执行、30 次预算精确拒绝。`internal/mcp` 全量套件通过。

## 参考

- https://www.anthropic.com/engineering/code-execution-with-mcp
- 本仓 #365 (MCP 结果上限/fill-aware), #1594-A (adapter 所有权), sa-96/sa-99
