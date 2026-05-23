# Round 6 全量代码审查报告

**审查日期**: 2025-05-24  
**审查方式**: 6 个 sub-agent 并行审查  
**审查范围**: 全量代码库  

---

## 审查模块与统计

| 模块 | Agent | Critical | High | Medium | Low | 合计 |
|------|-------|----------|------|--------|-----|------|
| Go 后端 (`internal/`) | sa-1 | 3 | 7 | 8 | 6 | 24 |
| Flutter 移动端 (`mobile/flutter/`) | sa-2 | 3 | 5 | 7 | 7 | 22 |
| Relay 服务器 (`ggcode-relay/`) | sa-3 | 3 | 6 | 7 | 6 | 22 |
| Desktop 桌面端 (`desktop/`) | sa-4 | 3 | 5 | 8 | 7 | 23 |
| 安全审查 (全项目) | sa-5 | 2 | 6 | 8 | 4 | 20 |
| SRE/DevOps (全项目) | sa-6 | 3 | 5 | 7 | 6 | 21 |
| **合计** | | **17** | **34** | **45** | **36** | **132** |

> 注：跨模块审查（安全、SRE）与单模块审查可能存在重叠发现，去重后独立问题约 100+ 项。

---

## 一、Go 后端 (`internal/`) — 24 项

### Critical (3)

**C-1. Broker 回调字段无锁保护 — 数据竞争**
- 文件: `internal/tunnel/broker.go:29-32, 100-101`
- 问题: `onCommand` 和 `onConnect` 是裸函数指针，在 `senderLoop` goroutine 的 `sess.OnMessage` 回调中被读取，同时可能被外部 goroutine 设置。无同步机制。
- 修复: 将 `onCommand`/`onConnect` 纳入 `snapshotMu` 保护，或使用 `atomic.Value`。

**C-2. Config 结构体无并发保护 — 数据竞争风险**
- 文件: `internal/config/config.go:198`
- 问题: `Config` 是大型可变结构体，被多个 goroutine 共享读取，但自身无互斥保护。运行时修改（如 `/model` 切换模型）与并发读取存在竞争。
- 修复: 热路径字段使用 `atomic.Value` 或 copy-on-write 模式。

**C-3. session.JSONLStore.AppendTunnelEventToDisk 文件操作非原子**
- 文件: `internal/session/store.go:787`
- 问题: 使用 `os.OpenFile` + `enc.Encode` + `f.Close()`，无 `fsync`。进程崩溃时 tunnel event 可能丢失，导致移动端恢复不一致。
- 修复: 在 `f.Close()` 前添加 `f.Sync()`。

### High (7)

**H-1. Broker outbound 队列无界 — 内存增长风险**
- 文件: `internal/tunnel/broker.go:44, 141-142`
- 问题: `outbound` 是无界 slice，注释 `// unbounded queue: enqueue never blocks`。如果 `session.Send()` 长时间阻塞，内存无限增长。
- 修复: 添加上限（如 10000），超出丢弃最旧消息并记录警告。

**H-2. swarm.Manager.Teams() 返回内部 map 的浅拷贝**
- 文件: `internal/swarm/manager.go:560-573`
- 问题: 返回的 `*Team` 指针指向内部 map 原始对象，调用者可无锁访问。
- 修复: 文档说明线程安全约束，或返回深拷贝。

**H-3. subagent.Manager goroutine 泄漏风险**
- 文件: `internal/subagent/manager.go:652`
- 问题: `forEachAgent` 在 mutex 锁内遍历并调用回调，回调阻塞或重入会导致死锁。
- 修复: 锁内复制列表，锁外调用回调。

**H-4. harness 全局 taskSaveMu 粒度过粗**
- 文件: `internal/harness/task.go:19`
- 问题: 全局单锁保护所有 task 文件 I/O，高并发下成为瓶颈。
- 修复: 改为 per-task mutex。

**H-5. cron.Scheduler 递归锁风险**
- 文件: `internal/cron/scheduler.go:165`
- 问题: timer callback 在 `mu.Lock()` 内执行，如间接调用 `Create`/`Delete` 会死锁。
- 修复: callback 执行前释放锁，使用异步调度。

**H-6. agent.Agent 中 contextManager 并发访问**
- 文件: `internal/agent/agent.go`, `internal/agent/agent_compact.go`
- 问题: compact 操作在独立 goroutine 中，与主 agent loop 可能竞争 `contextManager`。
- 修复: 确认 compact 是否只在单线程中执行，必要时添加互斥。

**H-7. provider retry.go 中的 response body 泄漏**
- 文件: `internal/provider/retry.go:110-130`
- 问题: 重试循环中 HTTP 响应体可能未正确关闭。
- 修复: 确保每个迭代中 `defer resp.Body.Close()` 或 break/continue 前显式关闭。

### Medium (8)

| ID | 文件 | 问题 |
|----|------|------|
| M-1 | `internal/daemon/background.go:34-37` | 使用 MD5 哈希，仅取 12 字符，碰撞概率在大量 workspace 下不可忽略 |
| M-2 | `internal/session/store.go:386` | `saveIndex` 非原子写入，进程崩溃时 index.json 可能损坏 |
| M-3 | `internal/swarm/idle_runner.go` | 无 context 取消传播，团队删除时需等待当前 agent turn 完成 |
| M-4 | `internal/tui/model.go:91` | pendingApproval Response channel 在 TUI 退出时可能泄漏 |
| M-5 | `internal/config/config.go` | 单文件 1305 行，圈复杂度高 |
| M-6 | `internal/mcp/client.go:597` | `ReadMessage` 每次创建新 bufio.Reader，缓冲区数据可能丢失 |
| M-7 | `internal/subagent/runner.go` | panic recovery 可能不覆盖 tool 执行内部 |
| M-8 | `internal/provider/retry.go` | exponential backoff 无 jitter，多实例同时重试（thundering herd） |

### Low (6)

| ID | 文件 | 问题 |
|----|------|------|
| L-1 | `internal/harness/release.go:737-743` | rand.Read 失败时回退到秒级时间戳 ID |
| L-2 | `internal/task/` | Manager 核心状态机测试不足 |
| L-3 | `internal/config/context_window.go` | 71KB / 781 行，大部分是静态映射表 |
| L-4 | `internal/chat/tools.go` | 29KB / 1067 行单文件 |
| L-5 | 多个文件（~25 处） | `_ = xxx.Close()` 吞掉关闭错误 |
| L-6 | `internal/daemon/follow.go` | 无 SIGWINCH 处理 |

---

## 二、Flutter 移动端 (`mobile/flutter/`) — 22 项

### Critical (3)

**C-1. StreamSubscription 泄漏 — 重复事件和内存增长**
- 文件: `lib/core/providers/session_provider.dart:167, 183, 190`
- 问题: `ConnectionNotifier.connect()` 中 `.listen()` 返回值未保存，重连/切换 workspace 时旧 subscription 永远不会 cancel。
- 修复: 将 subscription 存为实例字段，connect() 开头 cancel 旧的。

**C-2. 加密密钥来自 URL query parameter — 无 KDF 派生**
- 文件: `lib/core/providers/session_provider.dart:119-127`, `lib/core/crypto.dart:7-8`
- 问题: AES-GCM 密钥直接从 URL `?token=xxx` 提取使用，无 HKDF/SHA-256 派生。
- 修复: 对 token 进行 HKDF 派生后用作密钥。

**C-3. .env 文件包含 Apple App Store Connect API 凭证**
- 文件: `mobile/flutter/.env:4-6`
- 问题: 明文 Apple API Key ID 和 Issuer ID。`secrets/upload.jks` 签名密钥存在。
- 修复: 通过 CI 环境变量注入，添加 pre-commit hook 检查。

### High (5)

| ID | 文件 | 问题 |
|----|------|------|
| H-1 | `session_provider.dart` (2574 行) | God File: 单文件承载 18+ 个 Provider 和多个数据模型类 |
| H-2 | `lib/core/theme/app_theme.dart:171, 209` | 静态可变全局 `_current` 绕过 Riverpod 响应式，主题切换不触发 rebuild |
| H-3 | `session_provider.dart:1053-1243` | ChatNotifier 使用 O(n) 列表推导处理高频流式消息 |
| H-4 | `session_provider.dart:1428-1566` | `_ValueNotifier<T>` 通过闭包捕获 initialValue，Provider 重建风险 |
| H-5 | `session_provider.dart:53` | ConnectionNotifier 未注册 `ref.onDispose`，WebSocket 连接泄漏 |

### Medium (7)

| ID | 文件 | 问题 |
|----|------|------|
| M-1 | `chat_screen.dart` (1169 行) | 接近 God Widget 阈值 |
| M-2 | 多处 | 硬编码颜色值（0xFF1E1E2E 等）绕过主题系统 |
| M-3 | `lib/core/l10n/app_localizations.dart:31` | `_translations` 全局可变 Map 竞态条件 |
| M-4 | `connect_screen.dart:11-37` | 局部泛型 Provider `_SimpleNotifier<T>` 与全局 `_ValueNotifier<T>` 重复 |
| M-5 | `session_provider.dart:2436-2552` | `displayed*` Provider 与 Notifier 内部实现紧耦合 |
| M-6 | `main.dart:157-221` | build 方法中 5 个 ref.listen，频繁持久化写入 |
| M-7 | `lib/core/connection_service.dart:38, 75` | `_queue` Future 链无错误隔离，异常导致后续消息不处理 |

### Low (7)

| ID | 文件 | 问题 |
|----|------|------|
| L-1 | `session_provider.dart:36-50` | `TunnelConnectionState` 缺少 `==`/`hashCode` 重写 |
| L-2 | `main.dart:47` | `_connSub` 声明但从未使用 |
| L-3 | `pubspec.yaml:7` | SDK 约束过宽 `>=3.0.0`，实际使用 3.3+ 特性 |
| L-4 | `session_provider.dart:2364-2371` | `_flushDirtySnapshots` 在 Timer callback 中执行 async IO |
| L-5 | `approval_sheet.dart:42, 57` | `Colors.white` 硬编码 |
| L-6 | `session_provider.dart` (ChatMessage) | 缺少 `equatable`，不必要的 rebuild |
| L-7 | 无 `test/` 目录 | 零测试覆盖 |

---

## 三、Relay 服务器 (`ggcode-relay/`) — 22 项

### Critical (3)

**C-1. CheckOrigin 无条件允许所有来源**
- 文件: `main.go:590`
- 问题: `CheckOrigin: func(r *http.Request) bool { return true }`，任何网页可通过 JS 连接。
- 修复: 校验 Origin 在允许列表中。

**C-2. 零认证机制**
- 文件: `main.go:592-654`
- 问题: 仅靠 URL query 参数中的 token（最小 16 字符）做"认证"，无签名验证。
- 修复: 添加 HMAC 签名或 Bearer token 认证。

**C-3. 明文 HTTP 传输**
- 文件: `main.go:689`
- 问题: `http.ListenAndServe` 无 TLS，自部署场景 token 和消息明文传输。
- 修复: 添加 TLS 或强制要求反向代理做 TLS 终端。

### High (6)

| ID | 文件 | 问题 |
|----|------|------|
| H-1 | `main.go:53, 58-69` | `room.history` 无上限增长，OOM 风险 |
| H-2 | `main.go:635` | `sendCh` 缓冲区 10000 条，单连接可占 ~10GB |
| H-3 | `main.go:378-412` | `handleResume` 持有 room.mu 时调用 sendJSON/sendRaw，潜在死锁 |
| H-4 | `main.go:546-586, 608-614` | `destroyRoom` 与 `handleWS` 竞态，客户端可能连到 zombie room |
| H-5 | `main.go:131-177, 179-320` | 无 panic recovery，goroutine panic 导致进程崩溃 |
| H-6 | `main.go:658-692` | 无 graceful shutdown，连接粗暴断开 |

### Medium (7)

| ID | 文件 | 问题 |
|----|------|------|
| M-1 | `main.go:593-594` | Token 在 URL query 参数中，日志泄露风险 |
| M-2 | `main.go:211-319` | 无消息限流，可被 DDoS |
| M-3 | `main.go:132, 183` | readPump 和 writePump 双重 conn.Close() |
| M-4 | `main.go:243-277` | 控制消息忽略客户端 ready 状态 |
| M-5 | `store.go:44` | SQLite SetMaxOpenConns(1) 高并发瓶颈 |
| M-6 | `store.go:326-329` | Token 哈希无 salt |
| M-7 | `main.go` 多处 | 日志打印 token 前 8 字符 |

### Low (6)

| ID | 文件 | 问题 |
|----|------|------|
| L-1 | `Dockerfile:8` | Alpine 3.19 非最新 LTS |
| L-2 | `Dockerfile` | 缺 HEALTHCHECK 指令 |
| L-3 | `deploy.sh:26-27` | 部署前不运行测试 |
| L-4 | `main.go:684-687` | health 端点不检查数据库连接 |
| L-5 | `go.mod`, `Dockerfile` | Go 版本锁定问题 |
| L-6 | `main_test.go`, `store_test.go` | 测试覆盖不足（缺并发、端到端、背压测试） |

---

## 四、Desktop 桌面端 (`desktop/`) — 23 项

### Critical (3)

**C-1. HTML 文件预览启动本地 HTTP 服务器暴露整个工作目录**
- 文件: `desktop/ggcode-desktop/file_preview.go:159-219`
- 问题: `http.FileServer(http.Dir(dir))` 提供整个工作目录，包含 `.ggcode/ggcode.yaml`（含 API key）。
- 修复: 仅提供目标文件，添加白名单。

**C-2. Tunnel Broker 命令无来源验证**
- 文件: `desktop/ggcode-desktop/app.go:256-304`
- 问题: `CmdApprovalResponse` 直接调用 `handleMobileApprovalResponse`，无签名验证。relay 被攻破时攻击者可伪造批准响应。
- 修复: 添加命令签名/认证机制。

**C-3. 临时图标文件以 0644 权限写入固定路径**
- 文件: `desktop/ggcode-desktop/main.go:34-36`
- 问题: 写入 `/tmp/ggcode-icon.png`，固定路径冲突，0644 权限任何用户可读写。
- 修复: 使用 `os.CreateTemp()` 和 0600 权限。

### High (5)

| ID | 文件 | 问题 |
|----|------|------|
| H-1 | `file_preview.go:557-563` | `pakoEncode()` 忽略 deflate 错误和写入错误 |
| H-2 | `agent_bridge.go` (2554 行) | 单文件承载过多职责 |
| H-3 | `file_preview.go:641-648` | `findFreePort()` TOCTOU 竞态 |
| H-4 | `file_preview.go:471-472, 482` | Mermaid 缓存文件以 0644 写入 |
| H-5 | `safe_ui.go` | UIState 多锁获取顺序不一致可能死锁 |

### Medium (8)

| ID | 文件 | 问题 |
|----|------|------|
| M-1 | `file_tree.go:106-124` | `dirContainsMatch()` 无限递归风险，每次按键触发 |
| M-2 | `sidebar.go:280-338` | `loadSessions()` 非 UI goroutine 调用时数据竞争 |
| M-3 | `agent_bridge.go:2006-2007` | `saveSession()` 忽略磁盘写入错误 |
| M-4 | `config.go` | 多处 `_ = a.cfg.Save()` 忽略保存错误 |
| M-5 | `i18n.go:84` | `fmt.Sprintf` 格式/参数不匹配时 panic |
| M-6 | `chat_view.go` | 重建所有消息 widget，长对话卡顿 |
| M-7 | `file_preview.go:586-601` | Chroma 语法高亮基础设施存在但未工作 |
| M-8 | `desktop/markdownx/render.go:65-80` | `fetchImage()` 无超时，永久阻塞 |

### Low (7)

| ID | 文件 | 问题 |
|----|------|------|
| L-1 | `file_tree.go:53` | `.go.mod` 扩展名匹配永远不生效 |
| L-2 | `log.go:13-16` | `logPanic()` 与 `safeRecover()` 功能重复 |
| L-3 | `app.go:1262-1267` | 自定义 `max()` 遮蔽 Go 1.21+ 内置 `max` |
| L-4 | `file_preview.go:651-653` | 不必要的 `urlParse()` 包装 |
| L-5 | `types.go:48-51` | `Truncate()` 参数命名风格不一致 |
| L-6 | `version.go` | Version 变量无默认值 |
| L-7 | `markdownx/widget.go:140-165` | streaming rebuild 高频时性能问题 |

---

## 五、安全审查 (全项目) — 20 项

### Critical (2)

**C-01. WebUI 认证 Token 时序攻击**
- 文件: `internal/webui/auth.go`
- 问题: 使用 `==` 比较 Token，存在时序攻击风险。
- 修复: 改用 `subtle.ConstantTimeCompare`。

**C-02. DingTalk accessToken 明文日志泄露**
- 文件: `internal/im/dingtalk_adapter.go`
- 问题: debug 日志中输出 accessToken 和 appKey。
- 修复: 移除或遮蔽敏感字段。

### High (6)

| ID | 文件 | 问题 |
|----|------|------|
| H-01 | `ggcode-relay/main.go` | WebSocket 无 Origin 校验（与 Relay C-1 重叠） |
| H-02 | `ggcode-relay/main.go` | Token 在 URL 参数中（与 Relay M-1 重叠） |
| H-03 | `ggcode-relay/main.go` | 日志泄露 Token 前缀（与 Relay M-7 重叠） |
| H-04 | `internal/webui/server.go` | 无 CORS 配置 |
| H-05 | `internal/a2a/server.go` | 推送通知使用 `http.DefaultClient` 请求用户提供的 URL，SSRF 风险 |
| H-06 | 配置系统 | API Key 明文存储在 `keys.env` |

### Medium (8)

| ID | 文件 | 问题 |
|----|------|------|
| M-01 | 插件系统 | 参数分割不安全 |
| M-02 | Tunnel 加密 | KDF 使用静态盐 |
| M-03 | 沙箱系统 | 可被符号链接绕过 |
| M-04 | `.gitignore` | `keys.env` 未加入忽略 |
| M-05 | WebUI | 无认证速率限制 |
| M-06 | Relay | 无 TLS（与 Relay C-3 重叠） |
| M-07 | IM 适配器 | Webhook 签名验证不完整 |
| M-08 | Tunnel | 加密模式选择不当 |

### Low (4)

| ID | 文件 | 问题 |
|----|------|------|
| L-01 | WebSocket | Token 仅 localhost 传输 |
| L-02 | Shell 执行 | 已有缓解措施 |
| L-03 | 文档 | 占位符密钥示例 |
| L-04 | 正面发现 | A2A 使用 ConstantTimeCompare、Command Gate、权限模式、沙箱路径检查等安全设计值得肯定 |

### 正面安全设计

- A2A 使用 `subtle.ConstantTimeCompare` 验证 API Key
- 完善的 Command Gate 阻止破坏性命令
- 权限模式提供纵深防御
- 沙箱路径检查限制文件操作
- AES-GCM 用于隧道加密
- Relay SQLite 中 Token 哈希存储
- Config API 自动遮蔽敏感字段

---

## 六、SRE/DevOps 审查 (全项目) — 21 项

### Critical (3)

**C-1. Makefile install targets 缺少 `-tags goolm`**
- 文件: `Makefile:27-31`
- 问题: `go install $(PKG)` 和 `go install $(INSTALLER_PKG)` 不含 `-tags "$(TAGS)"`。
- 修复: 添加 `-tags "$(TAGS)"`。

**C-2. CI 集成测试标签不一致**
- 文件: `.github/workflows/ci.yml:25`
- 问题: CI 用 `-tags "goolm,integration"` 但 `release.yml` 用 `-tags goolm`。
- 修复: 统一标签策略。

**C-3. Relay 无 graceful shutdown（与 Relay H-6 重叠）**

### High (5)

| ID | 文件 | 问题 |
|----|------|------|
| H-1 | `ggcode-relay/Dockerfile` | Go 版本不匹配 + 缺 `-tags goolm` |
| H-2 | `ggcode-relay/main.go` | 无结构化 JSON 日志 |
| H-3 | `ggcode-relay/deploy.sh` | 无回滚机制 |
| H-4 | 全项目 | 无 metrics 导出（Prometheus/OpenMetrics） |
| H-5 | 测试文件 | HOME 目录隔离不一致 |

### Medium (7)

| ID | 文件 | 问题 |
|----|------|------|
| M-1 | `internal/webui/server.go` | 无健康检查端点 |
| M-2 | `internal/tunnel/relay_client.go` | 无自动重连 |
| M-3 | Relay 认证（与安全审查重叠） | WebSocket `/ws` 无认证 |
| M-4 | `ggcode-relay/Dockerfile` | 未定义非 root 用户 |
| M-5 | Desktop | 无 E2E 测试覆盖 |
| M-6 | `.github/workflows/ci.yml` | actions/checkout@v6 可能不存在 |
| M-7 | `.goreleaser.yaml` | 未包含 Relay 二进制或 Docker 镜像 |

### Low (6)

| ID | 文件 | 问题 |
|----|------|------|
| L-1 | `internal/debug/debug.go` | 运行时无法切换 debug 级别 |
| L-2 | `internal/config/config.go` | 启动时不验证必要字段 |
| L-3 | `docs/` | 无架构图/ADR 索引 |
| L-4 | `internal/update/update.go` | 自动更新不验证二进制校验和 |
| L-5 | `ggcode-relay/store.go` | SQLite 无备份机制 |
| L-6 | 项目根 | 无 CONTRIBUTING.md 或运维手册 |

---

## 跨模块 Top 10 紧急修复项

按影响范围和严重程度排序：

| 优先级 | 问题 | 影响模块 | 类别 |
|--------|------|----------|------|
| **1** | Relay CheckOrigin 全放行 + 零认证 | Relay, 安全 | 安全 |
| **2** | Broker onCommand/onConnect 数据竞争 | Go 后端 | 并发 |
| **3** | Flutter StreamSubscription 泄漏 | Flutter | 资源泄漏 |
| **4** | WebUI Token 时序攻击 | 安全 | 安全 |
| **5** | Desktop HTML 预览暴露工作目录 | Desktop | 安全 |
| **6** | DingTalk accessToken 日志泄露 | 安全 | 信息泄露 |
| **7** | Relay room.history 无上限 | Relay | 稳定性 |
| **8** | Relay 无 graceful shutdown | Relay, SRE | 稳定性 |
| **9** | Broker outbound 队列无界 | Go 后端 | 内存 |
| **10** | Makefile install 缺 goolm tag | SRE | 构建 |

---

## 与 Round 5 对比

| 方面 | Round 5 | Round 6 |
|------|---------|---------|
| Critical | 5 | 17（含跨模块重叠） |
| High | 28 | 34 |
| Medium | 75 | 45 |
| Low | 56 | 36 |
| 审查方式 | 6 sub-agent | 6 sub-agent |
| 新增关注 | - | Desktop 安全面、SRE 构建一致性、Flutter 生命周期 |

Round 5 中的数据竞争（C1/C2）已在 commit `8fb669c5` 中修复。本轮新发现的数据竞争（Broker callbacks、Config）是不同的位置。

---

## 建议的修复优先级

### P0 — 立即修复（安全/稳定性阻塞）
1. Relay 添加 Origin 校验和认证
2. Broker callback 添加锁保护
3. WebUI Token 比较改用 ConstantTimeCompare
4. Desktop file_preview 限制文件服务范围

### P1 — 本周修复
5. Flutter StreamSubscription 泄漏
6. DingTalk 日志脱敏
7. Relay graceful shutdown + history 上限
8. Makefile install 添加 goolm tag

### P2 — 下个迭代
9. Go 后端无界队列添加上限
10. Flutter god file 拆分
11. Desktop agent_bridge.go 拆分
12. 添加结构化日志和 metrics
