# 案卷 v2：常驻自主 agent loop 的 provider/model 切换语义（2026-11-19 调查）

状态：**立案未派修**——待 persistent-teams PR 合并后处理（同触 internal/agent|agentruntime 有冲突风险）。

## 主表：真常驻/自主 LLM loop 清单（证据行号 @8d2cc3d0d）

| loop | 形态 | provider 来源 | 主切 provider/model 后 |
|---|---|---|---|
| **teammate idle loop** | 常驻（board/inbox 驱动） | Agent 构造时 factory getter **取一次**（subsystems.go:148）；SetProvider 只喂主 agent（model_switch.go:93） | ❌ **钉死 spawn 时模型**。分时切 flash 对团队席不生效 |
| **IM daemon bridge** | 常驻（消息驱动，daemon 模式） | 持 `agent *agent.Agent`（daemon_bridge.go:48）+runAgentStream 自主 loop（:819）；**有 onProviderSwitch 回调**（:70/:1203-1221） | ⚠️ **IM 内切换即时生效**；TUI/主侧切换是否通知 daemon 实例待 E2E 定（若独立实例则钉死） |
| memory sweep curator（#5512） | **周 cron/手动**，非常驻（config 默认 off） | 注入 chat；注释明示 "main model, not flash"（memory_sweep.go:21）——**有意设计** | 触发时现取→跟主切 |
| cron scheduler | 常驻调度器，LLM 经主循环 | 主 agent | ✅ 跟 |
| compact（上下文压缩） | 主 loop 边界同步调用，非常驻 | a.provider 活字段 | ✅ 跟（含风险：分时 flash 期压缩长上下文=摘要降级） |
| subagent | 一次性跑完即焚 | spawn 快照 | n/a |

## 关键发现

1. **teammate 钉死**是分时策略的实际漏洞面：mono-ops 团队 5 席在 14:00 切 flash 后仍跑主力模型（省钱不生效）；反向——18:00 切回后，若团队在 flash 期 spawn 的新席会钉在 flash。
2. **IM bridge 的 onProviderSwitch**是正确模式（切换感知回调）——teammate 缺的就是这个。
3. compact 跟随主模型 + aux_model 死配置（v1 发现，保留）：`config.go:302` 定义+`model_routing.go:11` SetAuxModel 实现完整+**零生产调用**——配了不生效。

## 修法三件（对 v1 修正）

1. **teammate/subagent getter 动态化**：idle loop 每任务前重取 provider（分时生效）；或最小改——Manager 持 providerGetter，在 executeTask 前刷新 Agent.SetProvider。IM bridge 模式（onProviderSwitch 回调）可复用为主切换广播机制。
2. **aux_model 接线**（保留 v1）：启动+ApplyProviderToAgent 时 SetAuxModel；同步加 auxProvider 缓存失效（model_routing.go:28）。
3. **分时文档**：docs/guide 注明各 loop 跟随/钉死覆盖面。

## restore-verify 交叉检查（E2E 步骤 4.5）

team_restore 重建 teammate 的 provider：预期=持久化团队结构、**活解析模型**（factory getter 现取）。实测回填：
> [E2E 待填：____]

## 追记（E2E 实测发现·#5830 follow-up P1）

**exec 重启死锁**：restart 走 exec（PID 不变）后 bootID 更新，owner lease 判定序 owner_lease.go:207-210 同 host PID signal-0 权威优先——旧 lease 持有者=自己 PID→判 aliveForeign→RestoreTeam 永拒（心跳 5min 兜底被 PID 探针短路）。修法：alive() 中 `l.PID == os.Getpid()` 时视为 dead（自己的 exec 后代），或 lease 增进程启动时间戳防复用。E2E 绕行=清 owner.json 走 legacy 死继。

## 4.5 provider 语义回填（E2E 实证 2026-11-20 01:10）

teammate 恢复（RestoreTeam）= **fresh agent + factory getter 现取**：恢复时经 providerGetter 构造新 Agent——即 teammate 在"恢复瞬间"取当前主模型，恢复后钉死该值（与 spawn 同语义）。含义：分时切换对已恢复席同样不生效（钉死面与 spawn 一致）；但重启+恢复=天然换模窗口（恢复时现取）。修 teammate 钉死（getter 动态化）时恢复路径同修。

## E2E 终版结论（四轮实证）

- 跨重启持久化全链闭环 ✓：五席恢复（ID 钉死）/板 11 任务回/manifest sessionFile 回写/journal 同文件续写（21→29 行=回放挂载）
- 途中修两缺陷已 commit：①restore 旧 mf 覆盖回写擦 sessionFile（52769ecf）②upsert 漏 lastResultSummary
- 已立案未修：exec 重启死锁（owner_lease.go:207 PID 探针权威优先+bootID 变=活拒；修法=PID==os.Getpid() 视死或 lease 加启动时间戳）

## 收口挂账清理（01:26）
- [silent-error 闭环] 早前 "lanchat no receipt records (DapperShark f52b0b13)" 为记录淘汰所致误报：DapperShark 后续 LAN 明示"DM 为正式通道已知悉、重组完成"，送达与消费由对方实证闭环，非未处理错误。
- [ SilentShark cron-1 删裁 ] 裁决 DM delivered @01:18，执行回报在途（该席同步盯桌面 dmg 换池 run）。
