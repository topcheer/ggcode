# Release Review: v1.3.176 → v1.3.186 — 资源消耗与副作用审计

**Review scope**: 279 commits（v1.3.176..v1.3.186，10 个 release: 177-186 全覆盖）, 1943 files, ~322K insertions (大部分为 skills/文档) — 统计自 `git log --oneline v1.3.176..v1.3.186 | wc -l` 与 `git diff --stat v1.3.176 v1.3.186`
**边界核查**: `git diff --name-only v1.3.176 v1.3.177 | grep -E '^(internal|cmd)/'` 显示 v1.3.177 自身含核心变更：`cmd/ggcode/daemon.go`、`internal/hooks/runner.go`、`internal/lsp/{client,session}.go`、`internal/metrics/digest.go`——均为 daemon/LSP/metrics 常规演进，与报告内 M4（daemon 拆分）、LSP 相关发现同源，无独立于既有发现的资源/副作用风险
**Review date**: 2026-07-31
**Focus**: 计算资源占用 + 明显副作用改动

---

## 高风险 (3)

### R1: 同步构建验证循环 syncVerifyAndGate — 最大资源消耗点
- **提交**: `d41f70fc` (fix-on-fail), 后续 `9235e444` 等
- **位置**: `internal/agent/verify.go:412` (`syncVerifyAndGate`), 调用点 `agent.go:1421`; 辅助函数 `llmDecideVerifyCommand` `verify.go:194`, `executeVerifyCommand` `verify.go:289`
- **行为**: 每次 agent run 结束前，如果检测到代码变更 (`codeChangedInRun`)，**同步执行** build/test 命令（如 `go test ./...`），失败后注入错误并继续 agent 循环，最多 **3 次自动修复重试**
- **资源消耗**:
  - 每次 verify 最多 120s 超时 (verifyExecuteTimeout)，4 次尝试 = 最多 **480s 阻塞**
  - 每次失败会调用 `generalizeErrorsWithRetry` → **LLM 调用** (`ProcessErrorsWithLLM`) 泛化错误规则，最多重试 2 次 + 指数退避
  - 若无法确定性检测构建系统，会调用 LLM 决定验证命令 (`llmDecideVerifyCommand`，30s 超时)
  - 单次失败最多产生 **3 次 LLM 调用**（决策 + 泛化 + 重试）
- **副作用**: 每次代码编辑 run 都会触发完整构建/测试，大型 monorepo 中 `go test ./...` 可能数分钟；agent 自动修复循环可能掩盖用户意图（agent 认为修好了但语义偏离）
- **缓解**: 有 `maxSyncVerifyRetries=3` 边界、plan mode 跳过、ctx 取消检查；验证通过后跳过冗余 async verify
- **建议**: 考虑将验证命令结果缓存（`cdb09f02` 已做命令缓存，需确认 verify 路径是否复用）；`generalizeErrorsWithRetry` 使用 `context.Background()` 不可取消

### R2: 每次工具结果磁盘读 + 正则编译 — 热路径 I/O
- **提交**: `d41f70fc` (verify.go 的 injectRulesIntoResult)
- **位置**: `internal/agent/verify.go:366` (`injectRulesIntoResult` 定义), 调用点 `agent.go:1644` — 每个工具结果都调用
- **行为**: 对**每个** tool result:
  1. `NewRuleStore(workingDir)` 创建新实例（`verify.go:371`，无缓存复用）
  2. `load()` → `os.ReadFile(".ggcode/agent-rules.json")`（`ratchet.go:72`）**每次调用都从磁盘读**（因新实例 `loaded=false`）
  3. `MatchingRulesForTool`（`ratchet.go:382`）对每条规则 `regexp.Compile`（`ratchet.go:401`）每次调用都重新编译
  4. `MatchingRulesForResult`（`ratchet_reactive.go:23`）**有快速退出**：先检查结果是否含 error/fail/panic 等标记（`ratchet_reactive.go:28-43`），仅在有错误标记时才编译正则（`ratchet_reactive.go:50`）——非错误输出不触发正则扫描，但 preventive 路径 (MatchingRulesForTool) 无此快速退出
- **资源消耗**: 每次工具调用 = 1 次磁盘 I/O + N 次正则编译（N=规则数）。长时间 run（50+ 工具调用）累计明显
- **副作用**: 低 — 仅注入规则提示文本
- **建议**: 在 Agent 结构中缓存 RuleStore 实例（或按 workingDir 缓存）；规则模式在加载时预编译

### R3: 写后完整性检查链 — 每次写操作的解析开销
- **提交**: `549a1250`, `c39e760f`, `70a4ab90`, `27dbc78c`, `ac528f25`, `c010cd5c`, `2d5010af`
- **位置**: `internal/agent/write_integrity.go` (`checkWriteIntegrity`), `artifact_guard.go`, `import_lint.go`, `delimiter_check.go`
- **行为**: 每次 write/edit 后对**全部新内容**执行:
  - Go 文件: AST 解析 1 次 (已从 3x 优化到 1x, `9235e444`)
  - 非 Go 文件: 分隔符平衡扫描 (O(n) 逐字符)
  - 内容增长比检查、占位符检测、调试语句检测、合并冲突标记检测、delimiter balance
- **资源消耗**: 对大型文件（数百 KB 的生成文件）每次写都全量扫描；delimiter balance 对每个字符做状态机
- **副作用**: 低 — 仅注入警告。但 `checkContentGrowth` 的 5x 阈值对合法大幅重构可能误报
- **缓解**: Go AST 已去重；但 delimiter 检查无大小上限 — 已核实 `scanDelimiters`（`delimiter_check.go:139`）从 `i:=0` 遍历至 `n:=len(content)`（`delimiter_check.go:144-147`），函数体内无大小/长度上限检查；调用点 `write_integrity.go:131` 也无前置长度门控
- **建议**: 为 delimiter balance 增加大小上限（如 >1MB 跳过）

---

## 中风险 (4)

### M1: BM25 后台索引 — 已充分防护但仍是后台消耗
- **提交**: `d8492322`, `37094e8a`, `522e257f`, `1ff9cdc7`
- **防护已到位**: 惰性构建、10min 空闲内存释放、5min dirty-check 间隔、50K 文件上限、256KB/文件上限、5min 构建超时、100MB 磁盘缓存上限、跨进程 flock
- **剩余消耗**: 每次文件编辑调用 `MarkDirty()` 触发周期性重建，对 1M 行仓库首次构建 30-60s CPU
- **评估**: 可接受

### M2: 多语言 Test Impact Analysis (TIA) — 大文件新代码
- **提交**: `67e7a4ba`, `test_impact_lang.go` (934 行)
- **行为**: 解析 JS/TS/Python/Rust 等语言 AST 计算变更影响范围
- **位置**: 仅在 verify 流程中调用，非每轮循环
- **评估**: 低频调用，可接受；但 934 行新增需关注正则/AST 解析效率

### M3: 全量错误重试循环 (swarm/agent)
- **提交**: `deb00fb0` (swarm 永久错误重试修复), `c33ec924` (agent 层 LLM 重试), `77afb3c5` (429 自适应退避)
- **行为**: agent 层对瞬时 LLM 错误重试（rate limit 429 + 服务器过载自适应退避）
- **副作用**: 重试可能延长响应时间；`9bbddece` 已排除配额耗尽的 429（避免无效重试循环）
- **评估**: 修复方向正确，边界已控制

### M4: 守护进程拆分 + P2P 基础设施
- **提交**: `23b832f2` (daemon.go 2268→1586 行拆分), P2P webrtc 相关 (~10 commits)
- **资源**: 引入 pion/webrtc 依赖 + 信令 goroutine；已有 goroutine leak 修复 (64cd9c57, 839386ba, 9d479ed9)
- **评估**: 默认 feature flag 关闭 (p2p.enabled=false)，风险受控

---

## 低风险/已缓解 (若干)

| 项目 | 提交 | 评估 |
|------|------|------|
| Secret 红action | `17efc3f9`, `568eb426` | 256KB 截断 + 仅外部内容工具；15 个正则模式但限长，OK |
| 重复行压缩 | `befa34f9` | 5000 行上限，OK |
| 自适应 reasoning effort | `9986239b` | 纯启发式无 LLM 调用，反而降成本 |
| Scope drift 检测 | `e8545ead` | 确定性文件计数，每 run 一次，OK |
| 内容增长检测 | `ac528f25` | 5x 阈值 + 10 行最小值，误报低 |
| 令牌预算执行 | `e708a6a9` | 渐进式警告，无阻塞 |
| code_health 工具 | `92786c31` | 按需调用（LLM 主动），非自动 |
| 命令结果缓存 | `cdb09f02` | 确定性命令缓存，降低重复执行 |
| BM25 集成 | `8215e4dc` | 有权限/memoization 集成 |
| 计划器 | `755a7a4d` | 仅 seed 时调用 |

---

## 结论与建议优先级

1. **【高】R1 同步验证** — 确认 `verify` 路径复用 `cdb09f02` 的命令缓存；`generalizeErrorsWithRetry` 的 `context.Background()` 改为可取消；考虑对超大仓库禁用 sync verify 或降级为 async
2. **【高】R2 RuleStore 热路径** — Agent 结构内缓存 RuleStore + 预编译正则，消除每次工具调用的磁盘读 + 正则编译
3. **【中】R3 delimiter 扫描** — 增加 1MB 大小上限
4. 其余项防护已到位，无需立即处理

**总结**: 10 个 release 中新增了大量"agent 自我监督"类功能（写后校验、验证循环、规则学习），整体方向是**每次操作更多计算**换取质量提升。R1（同步构建验证）是最大的计算资源消耗点，R2 是最高频的隐藏 I/O 开销。两者均有明确的优化空间且不影响功能正确性。

---

## 副作用扫描补充（v1.3.176..v1.3.186 边界内，经 `git diff --name-status` 验证）

### S1: cmd/ggreport 整目录删除（4 个 Go 文件）
- **提交**: `bea3c3fb`（refactor: remove cmd/ggreport, use 'ggcode report' subcommand instead）
- **文件**: `cmd/ggreport/{html,main,report,scanner}.go` 全部 D
- **影响**: CLI 入口变更——`ggreport` 命令被移除，替代为 `ggcode report` 子命令。**用户可见的破坏性变更**，需在 release notes 中声明
- **风险**: 中（命令替换有 `7a483ceb` 等修复跟进，但依赖旧命令的脚本会失效）

### S2: config.go 主配置结构调整（94 行变更）
- **位置**: `internal/config/config.go`（+94/-34），`internal/config/external_files.go`（+454 新增）
- **内容**: 字段级调整 + 系统提示安全文本注入（tool-output UNTRUSTED DATA 指令，见 config.go diff）+ `external_files.go` 将 vendors/im/mcp_servers 拆分为独立配置文件（`4c5839d8` 在范围前引入，本范围延续）
- **风险**: 低-中。`omitempty` 保持向后兼容；但 Save 路径已重写（`361eb676` 修复首写数据泄漏），多进程并发写存在竞态窗口

### S3: 网络基础设施新增（relay/tunnel/lanchat）
- **文件**: `ggcode-relay/relay.go`（+437 行 `internal/tunnel/upgrade.go`、`cmd/ggcode/daemon_tunnel.go` +445、`internal/lanchat/{health,udp_transport}.go` 新增）
- **内容**: P2P WebRTC 升级路径 + relay 服务 + lanchat UDP 传输
- **风险**: 中。pion/webrtc 依赖 + 常驻 goroutine；已有 leak 修复（64cd9c57/839386ba/9d479ed9）；P2P 默认 feature flag 关闭，relay/lanchat 常开

---

## 后续建议（范围外观察）

### O1: Autopilot 策略师无界重试循环（v1.3.144-169 引入，早于审查范围）
- **引入**: `eb8f9f81`(v1.3.144) → `8fe980a2`(v1.3.145) 增加 call cap → `f21233df` 修复 → `a7ad2b19`(v1.3.169) 调整计数
- **问题**: 主 agent 以纯文本声明"任务完成"（无 build/test tool result 证据）时，策略师按 Anti-premature-convergence 规则（`autopilot_strategist.go:55-67` "When in doubt, continue — not stop" + 要求看到测试通过证据）拒绝宣布完成，持续注入"运行验证"指导；主 agent 认为已完成后不再调工具 → 死循环直到 100 次预算耗尽
- **证据**: `agent.go:1336`（仅按"无工具调用"触发，不识别主 agent 完成声明）、`autopilot_strategist.go:110-113`（Complete 仅前缀匹配 "GOAL_ACHIEVED"，markdown/措辞漂移即失败）、无连续无进展计数（对比 todo 检查有 `todoCheckCount<2` 上限）
- **修复建议**: P0 触发前检测主 agent 完成声明标记直接终止；P0 放宽 Complete 判定为包含匹配；P1 策略师提示加"主 agent 声明完成且近 3 轮无新工具调用→宣布完成"；P1 加连续无进展强制终止计数
