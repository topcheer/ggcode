# config 域未合并分支栈合并顺序评估（r155）

日期：2026-09-27
基线：origin/main @ 29fad0ab0（本轮 fetch 复核：**main 自 r145 起未前进**，全部栈仍待合并）
性质：纯评估文档，无代码变更。

## 1. 背景与方法

r145–r154 十个 research 分支全部基于 29fad0ab0 排队等待合并，`config_access.go` 等热点文件被多个在途 worktree 占用，多副本风险随栈深递增。本轮依据在线研究确立的 long-horizon 实践（OpenAI《Run long horizon tasks with Codex》：以外部化的 documentation.md 作为"shared memory and audit log"，让长时程工作可脱离上下文继续），将分支栈状态外部化为本评估文档，作为合并期间的审计日志与决策依据。

数据采集方法（全部为本轮实测，非推断）：
- `git merge-base` 确认各分支基点；`git rev-list --count` 确认提交数与远端同步状态
- `git diff --name-only 29fad0ab0...<branch>` 生成逐分支文件集，`comm -12` 计算两两重叠
- 对重叠文件逐一取 `@@` hunk 行号，判定文本冲突 vs 区域无关

## 2. 分支清单（2026-09-27 实测）

| 分支 | 提交数 | 文件数 | config 域文件 | 远端状态 |
|---|---|---|---|---|
| r145-frontier | 1 | 12 | 3 | 已推送，同步 |
| r146-frontier | 1 | 10 | 0 | 已推送，同步 |
| r147-frontier | 1 | 6 | 3 | 已推送，同步 |
| r148-frontier | 1 | 1 | 0 | 已推送，同步 |
| r149-frontier | 1 | 3 | 0 | 已推送，同步 |
| r150-frontier | 1 | 3 | 0 | 已推送，同步 |
| r151-frontier | 1 | 1 | 0 | 已推送，同步 |
| r152-frontier | 1 | 2 | 1 | 已推送，同步 |
| r153-frontier | 2 | 6 | 2 | 已推送，同步 |
| r154-frontier | 1 | 1 | 1 | 已推送，同步 |

无本地领先远端的提交，无数据丢失风险。

## 3. 两两重叠矩阵（文件级）

| 分支对 | 重叠文件数 | 重叠文件 |
|---|---|---|
| **r145 × r146** | **6** | configuration.md、i18n_en.go、i18n_zh.go、model_messages.go、model_update_dispatch.go、update_misc.go |
| r146 × r147 | 2 | configuration.md、config_hotreload.go |
| r149 × r150 | 2 | desktop/wailskit/config.go、desktop.md |
| r145 × r147 | 1 | configuration.md |
| r145 × r153 | 1 | configuration.md |
| r146 × r153 | 1 | configuration.md |
| r147 × r153 | 1 | configuration.md |
| r152 × r153 | 1 | config_access.go |
| r153 × r154 | 1 | instance.go |
| 其余 36 对 | 0 | — |

`docs/guide/configuration.md` 被 **4 个分支**（r145/r146/r147/r153）触碰，是文档面冲突热点，但文档冲突均可机械合并（各分支各自追加章节）。

## 4. 重叠文件的 hunk 级判定

| 文件 | 分支 A hunk | 分支 B hunk | 判定 |
|---|---|---|---|
| config_access.go | r152：@@669、@@719、@@767（commitAPIKeyClear 及 setAPIKey*WithProbe） | r153：@@238（getRuntimeSectionKey、新增 getScopeOrigins） | **区域无关，可自动合并** |
| instance.go | r154：@@58（InstanceConfigPath docstring） | r153：@@677（InstanceFields） | **区域无关，可自动合并** |
| config_hotreload.go | r146：文件顶部（ConfigReloadEvent L79-84、SetReloadListener，新增函数） | r147：@@125（pollOnce 内新增 MatchesRecentSelfWrite 检查） | **区域无关，可自动合并** |
| wailskit/config.go | r149 与 r150 均改 SaveAPIKey 流程 | — | **同函数邻近编辑，需人工复核**（唯一真实代码冲突候选） |
| TUI 6 文件（r145×r146） | i18n 词条/消息/分发表均为追加式 | — | 冲突机械可解（取并集），但 6 文件 × 2 分支是最大 rebase 面 |

## 5. 语义耦合分析

- **r147（自写防护）× r146（重载事件）**：r147 在 `pollOnce()` 中以 `MatchesRecentSelfWrite` 静默跳过自写触发的重载；r146 的 `ConfigReloadEvent{Applied, Err, Vendors, Fallback}` 无 self-write 原因字段。两者语义独立、可独立合并，但**合并后存在行为组合点**：自写跳过不产生任何事件，用户无感知。此为既有设计（r146 事件族已冻结，勿改），列入合并后回归验证清单，不立项新改动。
- **r149（endpoint scope 保持）× r150（key 轮换后重建 provider，519cf62b）**：同一 SaveAPIKey 流程的两段修复，r150 语义上依赖 r149 后的 scope 行为，**必须 r149 先并**。
- **r152（空 key 清除）× r153（scope.origins）**：hunk 区域无关，顺序不敏感，但连续合并可一次性回归 config_access 包。

## 6. 建议合并顺序

按"零冲突先行 → 解冻价值优先 → 冲突簇收尾"排序：

### Wave 0：零冲突，立即可并（任意顺序）
1. **r151**（webui dist 单文件，零重叠）
2. **r148**（config-hot-reload.md 单文档，零重叠）
3. **r154**（instance.go docstring 单 hunk；先于 r153 消除 instance.go 交叉）

### Wave 1：解冻价值最高的两组（解锁 r156 排队工作）
4. **r149** → 5. **r150**：desktop SaveAPIKey 修复对，r150（519cf62b）进 main 即解锁"Logout/SaveAPIKey/CompleteOAuth 对称测试族"中的 r150 侧冻结项
6. **r152** → 7. **r153**：config_access.go 区域无关对；r153 进 main 解锁钦定候选 2（对称测试族其余部分）

### Wave 2：config_hotreload 邻接对
8. **r147**（自写防护，hotreload 面 1 hunk + 3 个新文件）→ 9. **r146**（重载事件 + TUI 接线）

### Wave 3：最大重叠面收尾
10. **r145**（12 文件，与 r146 有 6 文件重叠）：**最后合并**，一次性 rebase 吸收 r146/r147/r153 带来的 configuration.md 与 TUI i18n 并集，避免后续分支反复 rebase。

> 排序核心依据：唯一真实代码冲突簇是 r145×r146 的 TUI 6 文件与 r149×r150 的 SaveAPIKey；其余重叠均已证明区域无关。把 r145 放最后使 rebase 面收敛为一次。

## 7. 合并后验证清单（每 Wave 执行）

```bash
go build -tags goolm ./...
go test -tags goolm -p 2 ./internal/config/... ./internal/agentruntime/...   # Wave 0-2 后
go test -tags goolm -p 2 ./internal/tui/...                                   # Wave 3（r145）后
```

- Wave 1 后：desktop SaveAPIKey 行为回归（scope 保持 + 轮换重建）
- Wave 2 后：hot-reload 组合回归（自写跳过 × 事件通知并存，确认无双重提示/无事件丢失告警误报）
- Wave 3 后：TUI i18n 键完整性（en/zh 词表并集无缺键）
- 全部合并后：删除 10 个本地+远端 frontier 分支，更新各轮记忆文件中"待合并"表述

## 8. 风险提示

1. **config_access.go 冻结建议**：r152/r153 落地前，禁止任何新 worktree 修改 config_access.go（当前已有 4 个在途 worktree 占用）；否则区域无关判定失效，r152/r153 需人工重放。
2. 主树 feat/opencode-oauth 长期偏离 origin/main（当前领先 137418ec3 等提交），分支栈合并期间**不要**将主树变更卷入 origin/main 的合并评审。
3. r153 含 2 个提交（唯一多提交分支），cherry-pick 时需整体采纳。

## 9. 在线依据

- OpenAI Developers, *Run long horizon tasks with Codex*（2026）：长时程工作依赖外部化状态（plan/implement/documentation.md 文件栈），"live status/audit log so the run stayed inspectable"；里程碑验证 + 修复后推进的循环。本评估文档即分支栈的 documentation.md。
- Bui, *Building AI Coding Agents for the Terminal*（arXiv:2603.05344）：scaffolding/harness 分离、"progressive degradation"与"transparency over magic"原则——分支栈治理是 harness 层可观测性的工程化延伸。
- 同轮证伪记录：OpenDev §2.2.5 per-workflow LLM binding（大/小模型分工）在 ggcode 无实施条件——`internal/config/vendor_defaults.go` 的 SmallModel 字段 38 个 vendor 全部为空串，且 `internal/` 无运行时消费者；数据通道已铺但无数据，构成可感知 gap 需新增配置面，与 config 域拥塞现状冲突，证伪为本轮可实施项（记入 r156 候选观察，不阻塞）。
