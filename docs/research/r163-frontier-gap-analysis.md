# r163 前沿概念 Gap 分析报告（零 gap 结论）

- 轮次：r163（研究轮）
- 基线：origin/main = `29fad0ab0`
- 分支：`r163-frontier-gap-report`（本文档为唯一交付物）
- 结论：**本轮所有候选方向经三重查（禁忌清单 + 已实施项 + 代码行号实证）后全部证伪，未发现用户可感知的真实 gap。零 gap 结论合法（dispatch 规则），本报告沉淀证伪证据供 owner 决策。**

## 1. 在线趋势研究依据（2025-2026）

| 主题 | 来源 |
|------|------|
| HITL 设计模式（confidence-threshold routing、显式检查点） | https://ortemtech.com/blog/human-in-the-loop-ai-agent-design-2026/ |
| Hooks = agent 确定性控制面（PreToolUse/PostToolUse 生命周期） | https://code.claude.com/docs/en/hooks 、https://agentsroom.dev/claude-code-hooks |
| 2026 安全模型对比：permission modes + granular rules + OS 级沙箱三层架构 | https://www.developersdigest.tech/blog/ai-coding-agent-security-models-compared-2026 |
| 沙箱阶梯与凭证保护（sandbox.credentials、内核强制边界） | https://bartlomiejkrupa.dev/articles/claude-code-security-sandboxing-2026 |
| 沙箱平台原生化（Seatbelt/bubblewrap 进入各大 Agent SDK） | https://www.dreaming.press/posts/agent-code-sandbox-platform-native-2026.html |
| 从业者共识：orchestration / grounding / permissions / evaluation / security 五轴 | https://kingy.ai/news/the-state-of-ai-agents-in-2026-a-practitioners-guide/ |
| 多代理黑板共享工作内容（承 r162） | arXiv:2510.01285 |

## 2. 逐轴证伪证据

### 轴 A：swarm direct-delivery（assignee inbox）依赖注入一致性（候选 3a）→ 非 gap，封案

r162 预埋的疑点：直投路径是否缺失 r162 的 depFindingsPrompt 依赖发现注入。证伪：

1. **公开工具面无法携带依赖**：`internal/tool/swarm_task_tools.go:26-53` —— `swarm_task_create` schema 仅暴露 `team_id / subject / description / assignee`，无 `blocked_by` 参数。直投任务在创建时不可能声明 BlockedBy，"直投任务带依赖却收不到 findings"的场景在工具面上不可达。
2. **发送方完全可控 prompt**（禁忌清单的证伪判据）：`internal/tool/swarm_task_tools.go:113` 直投走 `formatTaskPrompt(created)`；`:138-150` 中 `Description` 逐字透传给 teammate。leader（发送方）持有全部上下文，可在 description 中自行写入依赖发现——与 board claim 路径不同（claim 者无法接触创建者意图，故 r162 才需注入）。
3. **若任务后经 board 领取，仍走注入路径**：`internal/swarm/idle_runner.go:234` poller 跳过他人认领任务；assignee 本人的任务经 `tryClaimPendingTask`（`:240` `allBlockersComplete` 门控）领取，r162 的 depFindingsPrompt 在此生效。

结论：符合 r163 禁忌"发送方可控 prompt 即非 gap"。直投绕过 board 门控为既有设计行为，非缺陷。

### 轴 B：result metadata 在板面/list 的展示面（候选 3b）→ 非 gap，封案

1. `swarm_task_list` 渲染文本摘要 `- ID [status] subject → owner`（`internal/tool/swarm_task_tools.go:220`），确实不含 `metadata["result"]`。
2. 但 result 的两条读取路径已全量覆盖：
   - leader 侧：`teammate_results` 输出 teammate lastResult 全文（`internal/tool/team_tools.go:359-364` 单个 / `:368+` 全队一次调用）；
   - teammate 侧：r162 `depFindingsPrompt` claim 时注入已完成依赖的 result。
3. r162 明确设计"leader 上下文零增长"，板面列表追加 2000-rune 级 result 会破坏该意图且收益边际（一次 `teammate_results` 即得全部）。

结论：数据面无缺口，UX 面改动违背既有设计意图。封案。

### 轴 C：HITL 交互优化（hooks）→ 已覆盖

- `internal/hooks` 包完整实现：`HookConfig`（`internal/config/config.go:365`）、`ValidateHooks`（`config.go:1697`）、实例级合并 `mergeHookConfig`（`internal/config/instance.go:418`）。
- 文档在位：`docs/guide/hooks.md`（5 事件、command/http 双类型、match patterns、payload schema）、`docs/guide/configuration.md:194-218`。
- 权限模式（supervised/plan/auto/bypass/autopilot）+ approval memory（`internal/permission/approval_memory.go`）覆盖人工审批面。

### 轴 D：OS 级命令沙箱（2026 安全主轴）→ 已覆盖（含后台对称性）

- origin/main 已含完整实现：`0f01847d5` feat(tool): OS-level kernel sandbox for shell commands (sandbox.enabled)、`546b18a15` companion-test coverage。
- 接入面：`internal/tool/shell_sandbox.go:58` `wrapShellCommandOS`；前台 `internal/tool/run_command.go:308-321`（fail-closed，GUI 豁免 #568/#1245）；**后台命令对称接入** `internal/tool/command_jobs.go:328`。
- 配置：`SandboxConfig`（`internal/config/config.go:611-620`，AllowNetwork 开关）；与 `internal/permission/sandbox.go` PathSandbox（进程内路径层）分层互补，与 2026 三层架构对标。
- 本轮专项核查的后台绕过疑点：后台路径已同套包装，覆盖对称性成立。

### 轴 E：凭证保护 / prompt injection 防护 / grounding → 已覆盖

- 外泄策略与测试：`internal/permission/config_policy.go`（9 处 redact/secret 相关）+ `config_policy_exfiltrate_test.go`、`network.go` 网络策略。
- injection 意识：`internal/agent/agent.go`（23 处）、`internal/a2a/remote_tool.go`（远端工具面）。
- grounding：`internal/agent/file_freshness.go` File Freshness Sentinel——跨迭代外部变更主动检测，自带研究引用（Lost in the Middle, Liu et al. 2023）。

## 3. 保留给 owner 的开放方向（非本轮 gap，均为既有授权流程）

1. 合并评估执行（Wave 0-3）——owner 授权 + main 前进后（持续候选）。
2. 519cf62b OAuth 进 main 后的对称测试族解锁（冻结域，勿提前动）。
3. `internal/swarm/idle_runner.go` 存量重构（`tryClaimPendingTask` 复杂度 22/179 行）——须单独立项 + 全量 swarm pin 测试，勿搭车。

## 4. r164 禁忌清单 append

- 本轮全部证伪结论勿重立项：3a 直投依赖注入（发送方可控 prompt）、3b 板面 result 展示（读取面已覆盖 + 零增长设计意图）、hooks（已有）、OS 沙箱（main 已有且后台对称）、credential/injection/grounding（已有）。
- r105-r163 全部累计禁项继续有效（见 r162 记忆尾部清单全文）。
