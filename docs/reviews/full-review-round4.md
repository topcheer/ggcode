# ggcode 全面代码评审报告 (Round 4)

**评审日期**: 2025-05-12
**评审范围**: 全部 `internal/` + `cmd/` 目录 (~170k LOC)
**评审团队**: 5 位并行评审专家

---

## 汇总统计

| 评审维度 | 专家 | Critical | High | Medium | Low |
|---------|------|----------|------|--------|-----|
| Agent + Sub-agent + Swarm + Knight | agent-reviewer | 0 | 4 | 5 | — |
| Provider + Auth + API Key 安全 | provider-reviewer | 3 | 5 | 7 | 5 |
| TUI + WebUI + Daemon + IM | tui-reviewer | 2 | 5 | 7 | 6 |
| Harness + MCP + Plugin + Config | infra-reviewer | 4 | 6 | 8 | 7 |
| Resources + A2A + Tools + 边界 | resource-reviewer | 3 | 6 | 9 | 8 |
| **合计** | | **12** | **26** | **36** | **26** |

---

## 一、Agent 核心 + Sub-agent + Swarm 并发正确性 (agent-reviewer)

### High

| ID | 文件 | 问题 | 影响 | 建议 |
|----|------|------|------|------|
| H-1 | `internal/subagent/manager.go` | `Spawn()` TOCTOU — 解锁后重新读取共享状态，高并发下可能重复 spawn 或遗漏 | 并发安全 | 统一在锁内完成 spawn 决策 |
| H-3 | `internal/knight/` | `emitReportKeyed` 死锁风险 — 持有锁的同时调用外部接口 (IM adapter) | 潜在死锁 | 锁外调用 emitter，锁内仅设置标志 |
| H-4 | `internal/subagent/runner.go` | sub-agent panic 后不通知 `resultCh`，调用方可能永久阻塞 | goroutine 泄漏 | `safego.Recover` 后发送错误结果到 resultCh |
| H-5 | `internal/swarm/` | `teammateLoop` panic 后 teammate 变为 zombie（不工作也不释放） | 资源泄漏 | panic recovery 后重启或标记 teammate 为 failed |

### Medium

| ID | 文件 | 问题 | 影响 | 建议 |
|----|------|------|------|------|
| M-1 | `internal/agent/agent_tool.go` | tool_use 块中断后残留状态未清理 | 会话状态不一致 | context cancel 时清空 pending tool_use |
| M-2 | `internal/agent/agent_autopilot.go` | autopilot stall 返回 nil（静默失败） | 续跑可能静默停止 | 返回明确错误或日志 |
| M-3 | `internal/subagent/manager.go` | `syncToolWorkingDir` 使用 reflection 修改共享 tool 实例 | 数据竞争 | 为每个 sub-agent 创建 tool 实例副本 |
| M-4 | `internal/swarm/` | `CancelAll` 嵌套锁顺序需文档化 | 维护风险 | 添加注释说明锁获取顺序 |
| M-5 | `internal/agent/agent_compact.go` | compaction 期间的边界消息处理 | 压缩结果可能不完整 | 完善边界条件测试 |

### 跨模块观察
- 与 TUI 侧的 `safego.Recover` 使用模式一致，建议全局审查所有 recovery 点
- Knight IM emitter 调用链涉及 scheduler goroutine + IM runtime lock + WebSocket 广播，锁交互链较长
- 建议做一次全局锁获取顺序审计

---

## 二、Provider 层 + 安全/认证/API Key 处理 (provider-reviewer)

### Critical

| ID | 文件 | 问题 | 影响 | 建议 |
|----|------|------|------|------|
| C-1 | `internal/provider/anthropic.go` | Anthropic Chat 非流式请求无 retry 保护 | 瞬时故障直接失败 | 添加与 Stream 相同的 retry 逻辑 |
| C-2 | `internal/auth/jwt.go` | JWT issuer/audience 验证使用 fallback 默认值，可被构造的 token 绕过 | 认证绕过 | 当配置明确指定时不使用 fallback |
| C-3 | `internal/auth/jwt.go` | HMAC 签名使用 clientID 作为密钥（可预测） | 伪造 JWT | 使用独立的 signing secret |

### High

| ID | 文件 | 问题 | 影响 | 建议 |
|----|------|------|------|------|
| H-1 | `internal/provider/retry.go` | 默认重试次数 20 过高，加上 provider 层自带的 retry 可能达到 200+ 次实际请求 | API 费用爆炸 | 降低到 3-5 次，确保无嵌套重试 |
| H-2 | `internal/auth/` | Auth 模块使用 `http.DefaultClient` 无超时 | 请求可能永久阻塞 | 使用独立 client + 超时 |
| H-3 | `internal/provider/` | Token 错误消息可能泄漏完整 HTTP 响应体（含 API key） | 密钥泄漏 | 脱敏错误消息 |
| H-4 | `internal/auth/token_cache.go` | Token cache 过期逻辑有 off-by-one bug | 缓存行为异常 | 审查过期判断条件 |
| H-5 | `internal/provider/gemini.go` | `model_discovery` 不复用 HTTP Transport | 连接池浪费 | 共享 Transport 实例 |

### Medium

| ID | 问题 |
|----|------|
| M-1 | PKCE code_verifier 长度偏短（建议 >= 43 字符） |
| M-2 | 429 状态码的 backoff cap 不够（建议 >= 60s） |
| M-3 | Copilot provider body 全量读取无大小限制 |
| M-4 | `isRetryable` 默认返回 true（过于宽松） |
| M-5 | 重试状态码使用字符串匹配可能误匹配 |
| M-6 | Token cache 文件名截断到 12 字符可能导致冲突 |
| M-7 | `Retry-After-Ms` header 解析代码是死代码 |

### Low

| ID | 问题 |
|----|------|
| L-1 | `CountTokens` 不区分 tokenizer 差异 |
| L-2 | Transport 未设连接池参数 (MaxIdleConns 等) |
| L-3 | Anthropic bootstrap 产生临时明文 key |
| L-4 | Gemini `NewClient` 使用 `context.Background()` |
| L-5 | Windows 平台 atomic write 实现问题 |

---

## 三、TUI + WebUI + Daemon + IM 交互层 (tui-reviewer)

### Critical

| ID | 文件 | 问题 | 影响 | 建议 |
|----|------|------|------|------|
| C-1 | `internal/webui/` | WebSocket 无认证 — 任何本地进程可连接并注入消息 | 本地提权 | 添加 WebSocket 认证 (token/origin check) |
| C-2 | `internal/webui/` | `CheckOrigin` 全放行 — CSRF 攻击可让恶意网页向 WebUI 发送命令 | 远程命令执行 | 严格校验 Origin header |

### High

| ID | 文件 | 问题 | 影响 | 建议 |
|----|------|------|------|------|
| H-1 | `internal/im/` | IM runtime TOCTOU — 解锁后重新读取 adapter 状态 | 竞态条件 | 在锁内完成状态读取和操作 |
| H-2 | `internal/tui/` | `subAgentUpdateMsg` 的 grace tick timer 累积 — 每个 update 都创建新 timer | 内存泄漏 | 复用或取消前一个 timer |
| H-3 | `internal/daemon/` | 信号处理与 Bubble Tea 的交互 — fork 后资源清理不完整 | 文件描述符泄漏 | fork 前显式关闭所有连接 |
| H-4 | `internal/tui/` | `chatStartTool`/`chatFinishTool` suppress lists 未完美同步 | UI 状态不一致 | 统一管理 suppress state |
| H-5 | `internal/tui/` | `formatSubAgentDoneNotice` 未经过 i18n catalog | 国际化遗漏 | 使用 i18n.T() 包裹 |

### Medium

| ID | 问题 |
|----|------|
| M-1 | TUI 大列表渲染无虚拟化 — 上下文滚动可能在长对话中卡顿 |
| M-2 | IM adapter 断线重连逻辑不统一 — 各 adapter 自行实现 |
| M-3 | Daemon follow display 的 ANSI escape 在某些终端不兼容 |
| M-4 | WebSocket 写入 channel (256 buffer) 可能导致高负载下背压 |
| M-5 | TUI panel 切换时的短暂闪烁 — 无 double buffering |
| M-6 | WebUI REST API 缺少 CORS 配置 |
| M-7 | IM slash command `/restart` 无确认提示 |

### Low (6 项)
- TUI 状态栏信息过密
- IM Feishu adapter 时间格式硬编码
- Daemon 日志级别无法动态调整
- WebUI 前端未做 code splitting
- TUI 文件浏览器预览未限制文件大小
- Daemon 键盘快捷键文档不完整

### 架构亮点
1. ChatBridge 接口解耦设计优秀
2. i18n catalog 模式可维护
3. WebSocket per-connection write goroutine 避免了并发写问题
4. IM adapter 统一接口抽象良好
5. Daemon follow display 的 strip 模式设计实用
6. TUI panel 系统的组合模式灵活

---

## 四、Harness + MCP + Plugin + Config 基础设施 (infra-reviewer)

### Critical

| ID | 文件 | 问题 | 影响 |
|----|------|------|------|
| C-1 | `internal/mcp/` | MCP Client HTTP 无超时 — 连接挂起时 goroutine 泄漏 | 资源耗尽 |
| C-2 | `internal/mcp/` | MCP Client `io.ReadAll` 无大小限制 — 恶意 MCP server 可发送超大响应 | OOM |
| C-3 | `internal/harness/` | Harness JSON 文件写入无原子性 — 崩溃时数据损坏 | 数据丢失 |
| C-4 | `internal/config/` | Config 迁移逻辑可能覆盖用户自定义配置 | 配置丢失 |

### High

| ID | 文件 | 问题 |
|----|------|------|
| H-1 | `internal/mcp/` | MCP server 子进程 SIGTERM 后超时未 SIGKILL — 僵尸进程 |
| H-2 | `internal/harness/` | Worktree 清理不完整 — `.ggcode/worktrees/` 残留 |
| H-3 | `internal/config/` | YAML 合并逻辑对嵌套 map 处理不正确 |
| H-4 | `internal/permission/` | Plan → Auto 模式切换瞬间允许危险操作 |
| H-5 | `internal/session/` | JSONL session 写入非原子 — 崩溃时最后一行截断 |
| H-6 | `internal/plugin/` | Plugin 命令注入 — 未对 plugin 输出做大小限制 |

### Medium (8 项)
- Harness task 依赖解析不支持循环检测
- MCP JSON-RPC batch 请求未实现规范要求
- Config hot reload 有短暂不一致窗口
- Session checkpoint 与 compaction 的交互边界
- Permission `dangerous.go` 分类缺少部分工具
- Harness drift detection 对 merge commit 误报
- MCP OAuth 2.1 PKCE challenge 存储在内存（重启丢失）
- Plugin sandbox 对工作目录限制不严格

### Low (7 项)
- Harness monitor 轮询间隔硬编码
- MCP preset 安装无进度反馈
- Config 验证错误消息不够具体
- Session 文件名格式不一致
- Checkpoint 清理策略过于保守
- Permission mode cycle 方向未在文档说明
- Harness inbox 排序不支持自定义优先级

---

## 五、资源管理 + A2A + 工具 + 边界情况 (resource-reviewer)

### Critical

| ID | 文件 | 问题 | 影响 | 建议 |
|----|------|------|------|------|
| C-1 | `internal/a2a/server.go:756-794` | Push Notification SSRF — `firePushNotifications` 使用 `http.DefaultClient.Do(req)` POST 到用户注册的任意 URL，无 SSRF 防护 | 内网探测、AWS metadata 泄漏 | 对 push URL 做 `isPrivateHost` + `resolvePublicDialAddress` 检查，限制 schema 为 `https://` |
| C-2 | `internal/a2a/handler.go:678-679` | `SkillCommandExec` 无 agent 时直接将用户文本作为 `run_command` 执行，无过滤 | 远程任意命令执行 | A2A command-exec 应始终通过 agent 执行，或增加命令白名单 |
| C-3 | `cmd/ggcode/pipe.go:304` | `io.ReadAll(os.Stdin)` 无大小限制 — `cat /dev/urandom \| ggcode -p` 导致 OOM | 拒绝服务 | 使用 `io.LimitReader(os.Stdin, maxStdinSize)` |

### High

| ID | 文件 | 问题 | 影响 | 建议 |
|----|------|------|------|------|
| H-1 | `internal/a2a/client.go:110` | `Discover` 解码 Agent Card 无大小限制 — 恶意服务端可返回超大 JSON | OOM | 使用 `util.ReadAll` + 大小限制 |
| H-2 | `internal/a2a/server.go:229-272` | 多认证逻辑短路 — API Key 不匹配时直接 return false，不尝试 Bearer/mTLS | 认证绕过 | 所有认证方式都失败时才返回 false |
| H-3 | `internal/a2a/server.go:603-616` | Push Notification 无数量限制和 URL 验证 | 资源滥用 | 限制每客户端最多 N 个回调，验证 URL |
| H-4 | `internal/tool/web_fetch.go:232-244` | `stripHTML` 正则 ReDoS — `.*?` 惰性匹配对恶意 HTML 回溯爆炸 | 拒绝服务 | 使用 `golang.org/x/net/html` 解析 |
| H-5 | `internal/tool/web_fetch.go:151-165` | `isPrivateHost` DNS rebinding TOCTOU | SSRF 绕过 | 仅在 Dial 层检查，URL 层不做 DNS |
| H-6 | `internal/tool/todo_write.go:140` | `os.WriteFile` 非原子写入 | 崩溃时数据损坏 | 改用 `util.AtomicWriteFile` |

### Medium (9 项)

| ID | 文件 | 问题 |
|----|------|------|
| M-1 | `internal/tool/read_file.go:83` | `os.ReadFile` 无大小限制 |
| M-2 | `internal/tool/edit_file.go:71` / `multi_edit.go:86` | 同上 |
| M-3 | `internal/tool/notebook_edit.go:95` | 同上 |
| M-4 | `internal/tool/search_files.go:82` | Scanner 单行无长度限制 |
| M-5 | `internal/a2a/handler.go:723-725` | `generateID` 使用时间戳+自增，可预测 |
| M-6 | `internal/context/tokenizer.go:6-18` | Token 估算对混合 Unicode 不准确 |
| M-7 | `internal/memory/init.go` | 多处 `os.ReadFile` 无大小限制 |
| M-8 | `internal/cost/manager.go` | 非原子写入 |
| M-9 | `internal/image/clipboard_windows.go:35` | PowerShell 路径注入风险 |

### Low (8 项)

| ID | 文件 | 问题 |
|----|------|------|
| L-1 | `internal/diff/diff.go` | 无输入大小限制 |
| L-2 | `internal/image/image.go:106` | 图片读取无大小限制 |
| L-3 | `internal/a2a/client.go:78-79` | 缺少 TLS 握手/连接超时 |
| L-4 | `internal/tool/run_command.go` | 后台命令输出缓冲无大小限制 |
| L-5 | `internal/a2a/registry.go:158` | PID 存活性检查 TOCTOU |
| L-6 | `internal/a2a/client.go:142-145` | 无安全要求时静默跳过认证 |
| L-7 | `internal/context/manager.go` | `contentFingerprint` XOR 碰撞率高 |
| L-8 | `internal/tool/web_search.go:81` | 查询无长度限制 |

---

## 跨模块联合发现

### 1. 全局 `io.ReadAll` / `os.ReadFile` 无限制问题 (影响 20+ 文件)
**问题**: 整个代码库中大量使用 `io.ReadAll` 和 `os.ReadFile` 读取不受信任的数据，没有大小限制。
**涉及模块**: A2A、MCP、Provider、Tool、Memory、Image、Config、Pipe
**建议**: 建立统一的 `util.SafeReadAll(r io.Reader, maxSize int64)` 工具函数，全局替换。

### 2. 非原子文件写入 (影响 8+ 文件)
**问题**: 多处使用 `os.WriteFile` 而非 `util.AtomicWriteFile`，崩溃时数据损坏。
**涉及模块**: Session、Harness、Todo、Cost、Config
**建议**: 全局搜索 `os.WriteFile` 并逐个评估是否需要原子写入。

### 3. TOCTOU 竞态模式 (影响 5+ 文件)
**问题**: 多处 "解锁后重新读取共享状态" 的 TOCTOU 模式。
**涉及模块**: Sub-agent Spawn、IM runtime、A2A authenticate、DNS rebinding
**建议**: 统一修复模式 — 在锁内完成决策和操作。

### 4. HTTP Client 超时缺失
**问题**: 多处使用 `http.DefaultClient` 或未配置超时的 client。
**涉及模块**: Provider、Auth、MCP、A2A、WebUI
**建议**: 建立统一的 `util.NewSafeHTTPClient()` 工厂函数。

### 5. Panic Recovery 后未通知等待方
**问题**: `safego.Recover` 后未通知 channel 等待方，导致调用方永久阻塞。
**涉及模块**: Sub-agent runner、Swarm teammate loop
**建议**: 全局审查所有 `safego.Recover` 调用点，确保 recovery 后通知等待方。

---

## 修复优先级建议

### P0 — 立即修复 (Critical, 安全相关)

| # | 问题 | 风险 |
|---|------|------|
| 1 | A2A Push Notification SSRF (C-res-1) | 内网攻击跳板 |
| 2 | A2A 远程命令执行 (C-res-2) | 远程 RCE |
| 3 | JWT issuer/audience fallback 绕过 (C-prov-2) | 认证绕过 |
| 4 | HMAC 用 clientID 做密钥 (C-prov-3) | 伪造 JWT |
| 5 | WebSocket CheckOrigin 全放行 (C-tui-2) | CSRF/RCE |
| 6 | MCP Client HTTP 无超时 (C-infra-1) | DoS |
| 7 | Harness JSON 非原子写入 (C-infra-3) | 数据损坏 |

### P1 — 尽快修复 (High)

| # | 问题 | 风险 |
|---|------|------|
| 1 | 多认证逻辑短路 (H-res-2) | 认证绕过 |
| 2 | Sub-agent Spawn TOCTOU (H-agent-1) | 竞态 |
| 3 | Knight emitReportKeyed 死锁 (H-agent-3) | 死锁 |
| 4 | Auth 模块无超时 (H-prov-2) | 挂起 |
| 5 | 重试嵌套 200+ 次 (H-prov-1) | 费用爆炸 |
| 6 | stripHTML ReDoS (H-res-4) | DoS |
| 7 | DNS rebinding TOCTOU (H-res-5) | SSRF |
| 8 | MCP 僵尸进程 (H-infra-1) | 资源泄漏 |
| 9 | Config 迁移覆盖用户配置 (C-infra-4) | 数据丢失 |

### P2 — 计划修复 (Medium)

- 全局 `io.ReadAll` / `os.ReadFile` 大小限制
- 全局 `os.WriteFile` → `util.AtomicWriteFile` 替换
- Token 估算精度优化
- A2A task ID 改用 UUID
- Permission 模式切换原子性

### P3 — 持续改进 (Low)

- 连接池参数优化
- Windows 平台兼容性
- 文档和注释补充
- 日志级别动态调整

---

## 建议的系统性改进

1. **建立安全编码规范**: 针对本文发现的 5 大类共性问题（无限制读取、非原子写入、TOCTOU、HTTP 超时、panic recovery），编写团队级安全编码 checklist。

2. **引入静态分析**: 配置 `golangci-lint` 的 `gosec` 规则，自动检测 `io.ReadAll` 无限制、`os.WriteFile` 非原子等模式。

3. **统一工具函数**: 建立 `util.SafeReadAll`、`util.NewSafeHTTPClient`、`util.AtomicWriteFile` 等标准工具，新代码强制使用。

4. **锁获取顺序文档**: 编写全局锁获取顺序图，避免跨模块死锁。

5. **Fuzz 测试**: 对 A2A 协议、MCP JSON-RPC、Provider 响应解析等外部输入路径增加 fuzz 测试。
