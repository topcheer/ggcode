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
或代码内调用 `GuidanceLedgerReport()`。

## 范围说明

只计量**预算内** guidance。绕过预算的一次性通知（计划建议、loop 恢复
nudge、会话超时告警）自带硬上限，不属于校准问题。
