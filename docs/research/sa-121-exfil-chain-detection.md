# sa-121: Agent 数据外泄链检测（Log-to-Leak 对标）

日期: 2026-10-05 · 判定: PARTIAL · P1 实施派发 sa-122

## 来源

- Log-To-Leak 原始论文 (openreview.net/forum?id=UVgbFuXPaO): 注入式外泄 prompt 四组件 — **Trigger**(触发条件,潜伏多轮)、**Tool Binding**(指定外发通道 logging/analytics/http)、**Justification**(编造正当理由 "for quality monitoring")、**Pressure**(紧迫感话术)。攻击者可 log 用户-agent 全部交互。
- Microsoft 2026-06-30 披露 (via aviatrix/CSA 研究笔记): 自然语言工具描述被静默修改后 agent 收集并外泄敏感数据，**每个单独动作看起来都合法** — 单点内容检测系统性失效，需链级序列检测。

## 判定: 四组件对照

| # | 组件 | 判定 | 证据 |
|---|------|------|------|
| 0 | 前置: 外发内容 secret 屏蔽 | EXISTS | secret_redact.go:37-74 (输出→LLM mask), :107 防 REDACTED 写盘 — 内容侧 |
| 1 | 外泄链模式检测 | **GAP** | taint_influence_check.go:59-81 sink 全为写/执行类, 无 web_fetch/im/browser |
| 2 | Justification 话术识别 | **GAP** | logging_intel_check.go:272,331 是代码内容侧, 非 agent 外调参数 |
| 3 | 敏感源→外发汇数据流配对 | PARTIAL | taint 传播架构可复用(:34-49 window=6步/5min)但源=注入内容(:131-138)、汇=写/执行 — 方向错配 |
| 4 | 外发意图配对 | **GAP** | irrev_gate.go:97 有 irreversible 门槛; agent_tool.go:86 approval memory 已承认 exfiltration 语义, 无意图→目标配对 |

r101 "outbound-secret-guard" 边界澄清: 该命名在 internal/agent 实现文件零命中; 实际存在的是 secret_redact (输出 mask) + hardcoded_secret_check (写盘) + v1.3.189 release note 的 egress 条目 (permission 层 curl/wget 单命令告警) — 全部单点内容侧, 非链式。

## Backlog

| 项 | 优先级 | 理由 |
|----|--------|------|
| P1 exfil_chain_check.go | 高 | Tier-1 参数 verbatim 匹配误报≈0; 复用检测器族模式 ~220 行 (sa-122 实施中) |
| P2 出站参数 secret 扫描 | 高 | web_fetch/im send 参数侧复用 secretPatterns, 独立可交付 |
| P3 Justification 话术共现 | 中 | 只作 P1 置信度加权, 勿独立触发 (误报爆炸) |
| P4 用户意图→出站配对 | 低 | 需意图表示, P1+审批已覆盖大部分 |

## P1 方案要点 (sa-122 任务书)

- 源记录: read_file/grep/run_command 结果命中 secretPatterns 或路径 ~/.ssh|*.pem|.env|*credentials*; fingerprint 5min 过期/上限 6
- 汇: exfilSinkTools + run_command 含 curl|wget|nc|ssh
- Tier-1 (warn): 出站参数 verbatim 含敏感内容; Tier-2 (低置信): 6 步窗内出站, 限 2/run
- 误报控制: 豁免表照搬 hardcoded_secret_check.go:44-59; localhost/内网 IP 豁免
- 挂载: agent.go post-tool 管线 (taint 接线同款)

与 sa-119 (anti-rug-pull, 定义侧, 已合 8ed168f7b) 互补: sa-119 防定义偷换, 本项防运行时外泄链。

GitHub issue 立案因 integration 权限 403 未成, 以本文档为准。
