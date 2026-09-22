# Detector Effectiveness Ledger（检测器实证账本）

> sa-36 研究轮产出。对标概念：detector calibration / empirical evaluation loop
> （TRAIL benchmark, arXiv:2505.08638；Pisama 校准检测器实践 2026；arXiv:2602.15391 自适应检测）。

## 问题

ggcode 的约 40 个行为检测器（edit-oscillation、spec-gaming、error-rush 等）
产生的 guidance 全部经过统一的 per-turn 预算闸门（`guidanceBudget`）：
数量上限（2/turn）、字节上限（2048B/turn）、关键提示独立池（1024B/turn）、
同 tag 去重。但预算只保留一个 per-turn 的 `suppressed` 计数——运行结束后
无法回答校准问题：

- 哪些检测器本 run 真正投递了 guidance？投了几次、占多少上下文字节？
- 哪些检测器被字节上限/数量上限/去重/关键池**静默饿死**（仍然付出
  tier 采样成本，但对模型上下文零贡献）？
- 检测器的边际价值是否为零（应调阈值或下线）？

文献结论：启发式检测器的精度不是假设出来的，是**测出来的**——静态阈值
误报率 ~25%，经实测反馈校准后可降至 ~3%（arXiv:2602.15391）。

## 实现

`internal/agent/detector_ledger.go`：

- `detectorLedger`：run 作用域账本，按 hint 头部 tag
  （`[edit-oscillation]`、`[spec-gaming]`、无 tag 归入 `(untagged)`）记录
  `delivered / suppressed{bytes,count,dedup,crit} / bytes / 首末迭代号`。
- 记录点在预算闸门内部（`allowTagged` / `allowDeduped`），因此三条投递
  路径（`injectGuidance`、`appendGuidance`、coalesced tool-result hints）
  全部在唯一咽喉点被计量，调用方零改动。
- run 开始清零；迭代循环头部 `setTurn(i+1)` 盖时间戳；run 结束
  （含取消/错误路径）`debug.Log("detector-ledger", ...)` 输出聚合。
- 对外 API：`GuidanceLedgerSnapshot()` / `GuidanceLedgerReport()`。

## 输出示例

```
guidance ledger: 6 tag(s), 12 delivered (2.3KB), 7 suppressed
  [edit-oscillation] delivered=4 (1.38KB) suppressed{bytes=1 count=0 dedup=2 crit=0} turns 5-31
  [spec-gaming] delivered=2 (612B) suppressed{bytes=0 count=1 dedup=0 crit=0} turn 12
```

查看方式：`GGCODE_DEBUG=1` 运行后经 debug log（类别 `detector-ledger`），
或 `/runreport`（scorecard 后自动追加账本与饥饿报告）。

## 消费闭环（sa-37）

> 对标概念：silent failure / entropy principle（arXiv:2606.08162，反馈校正层）
> 与 AGrail（ACL 2025，inference-time 自适应 guardrail）。测量不消费 = 仍是熵。

sa-36 账本只测量、不消费——`GuidanceLedgerReport()` 唯一去处是 debug.Log。
sa-37 把测量结果接回系统，形成两条闭环：

**1. 弹性字节池（预算侧自适应校准，arXiv:2602.15391 路线）**

`guidanceBudget` 的两个字节池（advisory 2048B / critical 1024B）不再是
固定常量，而是**弹性上限**：每个 turn 边界 `reset()` 里的 `adaptCaps()`
读取上一 turn 实测的字节拒绝计数：

- 有饥饿 → 池 +25%（上限 2× 基准，#1197 防洪保证仍有硬顶）
- 无饥饿 → 池 -20% 回落至基准

滞回设计（只在实测饥饿时增长、只在干净 turn 回落）使池稳定围绕本 run
真实需求波动而非振荡。**数量上限（2/turn）刻意保持固定**：它保护的是
注意力带宽（ACE context-collision），自适应注意力上限会重新引入 alert
fatigue。调整事件进 `guidance-budget` debug 类别。

**2. 静默饥饿呈现（观测侧闭环）**

账本新增饥饿分析：`starvedRows`（delivered==0 且有拒绝的 tag，即
"fired but never delivered"）与 `starvationReportLocked()` 单行摘要：

```
silent starvation: 2 tag(s) fired but never delivered: [late-detector]x7 [rare-tag]x2
```

消费端三处：

- run 结束 `logRunSummary()` 在账本汇总后单独输出饥饿行（debug 类别
  `detector-ledger`）
- 对外 API `GuidanceStarvationReport()`
- `/runreport`：scorecard 后自动追加账本报告与饥饿报告——算子第一次
  无需开 debug 就能看到"哪些检测器在白付采样成本"

## 边界

- 弹性池只调**容量**上限；计数帽与去重语义不变，单 turn 内的防洪界
  （#1197）不变（增长只发生在 turn 边界）
- 单元测试：`internal/agent/ledger_consume_test.go`（增长/封顶/回落/
  critical 池/饥饿报告/nil 安全）

## 范围说明

只计量**预算内** guidance。绕过预算的一次性通知（计划建议、loop 恢复
nudge、会话超时告警）自带硬上限，不属于校准问题。
