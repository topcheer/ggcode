# Round 7 全量代码审查报告

**审查日期**: 2025-07-14  
**审查方式**: 7 个 sub-agent 并行审查  
**审查范围**: 全量代码库（基于 commit `2095536e`）

---

## 审查模块与统计

| 模块 | Agent | Critical | High | Medium | Low | 合计 |
|------|-------|----------|------|--------|-----|------|
| Go 后端 (`internal/`) | sa-1 | 3 | 5 | 8 | 8 | 24 |
| Flutter 移动端 (`mobile/flutter/`) | sa-2 | 3 | 6 | 8 | 5 | 22 |
| Relay 服务器 (`ggcode-relay/`) | sa-3 | 4 | 5 | 8 | 7 | 24 |
| Desktop 桌面端 (`desktop/`) | sa-4 | 3 | 5 | 9 | 6 | 23 |
| 安全审查 (全项目) | sa-5 | 2 | 7 | 7 | 5 | 21 |
| SRE/DevOps (全项目) | sa-6 | 2 | 6 | 10 | 8 | 26 |
| 测试与质量 (全项目) | sa-7 | 6 | 15 | 13 | 8 | 42 |
| **合计（去重前）** | | **23** | **49** | **63** | **47** | **182** |

> 注：跨模块审查（安全、SRE、测试质量）与单模块审查存在重叠发现，去重后独立问题约 120+ 项。

---

## 跨模块 Top 15 紧急修复项

| # | 问题 | 模块 | 类别 | 首次发现 |
|---|------|------|------|----------|
| **1** | WebUI Token `==` 比较 → 时序攻击 | 安全 | CWE-208 | sa-5 C-01 |
| **2** | A2A `CancelTask` 不关 `done` channel → goroutine 泄漏 | Go后端 | 并发 | sa-1 C-1 |
| **3** | Flutter StreamSubscription 泄漏 → 重复事件 | Flutter | 资源泄漏 | sa-2 C-1 |
| **4** | Python 安装器默认禁用 TLS 校验 | SRE | 供应链安全 | sa-6 C-1 |
| **5** | Relay CheckOrigin 全放行 + 零认证 + 无 TLS | Relay | 安全 | sa-3 C-1/C-3/C-4 |
| **6** | Desktop `file_preview` HTTP 暴露整个工作目录 | Desktop | 安全 | sa-4 C-1 |
| **7** | Desktop `agent_bridge.go` 多处数据竞争 | Desktop | 并发 | sa-4 C-2/C-3 |
| **8** | WebUI Server 字段无锁并发读写 | Go后端 | 并发 | sa-1 C-2 |
| **9** | `safego.PanicHook` 全局变量无 atomic 保护 | Go后端 | 并发 | sa-1 C-3 |
| **10** | Flutter Token 明文存储 SharedPreferences | Flutter | 安全 | sa-2 C-2 |
| **11** | A2A Push Notification SSRF | 安全 | CWE-918 | sa-5 C-02 |
| **12** | `session_provider.dart` 2609 行 God File | Flutter | 代码质量 | sa-2 C-3 |
| **13** | `make test` 和 CI 缺 `-race` flag | 测试 | 并发安全 | sa-7 Q-C03 |
| **14** | `internal/daemon/` 1050 行零测试覆盖 | 测试 | 覆盖缺口 | sa-7 Q-C02 |
| **15** | DingTalk accessToken 日志泄露 | 安全 | CWE-522 | sa-5 H-04 |

---

## 一、Go 后端 (`internal/`) — 24 项

### Critical (3)

**C-1. A2A handler `CancelTask` 不关闭 `done` channel → goroutine 泄漏**
- 文件: `internal/a2a/handler.go:522-545`
- 问题: `CancelTask()` 设状态为 `TaskStateCanceled` 但不关闭 `t.done`。`handleMessageSend()` 在 `select` 等待 `<-done`，导致 HTTP handler goroutine 泄漏直到 5 分钟超时。
- 修复: `CancelTask()` 中状态更新后关闭 `t.done`。

**C-2. WebUI Server 字段无锁并发读写**
- 文件: `internal/webui/server.go:137-159` + `server_handlers.go:66`
- 问题: `saveScope`、`chatBridge`、`agent` 等字段在 `SetXxx()` 和 HTTP handler goroutine 之间无锁共享。
- 修复: 统一用 `mu` 保护，或限制 setter 仅在 `Start()` 前调用。

**C-3. `safego.PanicHook` 全局变量无 atomic 保护**
- 文件: `internal/safego/safego.go:32`
- 问题: `PanicHook` 和 `logFn` 在 `Recover()` 中读取（任意 goroutine），在初始化时写入，存在 data race。
- 修复: 使用 `sync.Once` 或 `atomic.Value` 保护。

### High (5)

**H-1.** A2A `RequestInput` 不关闭 `done` channel — `internal/a2a/handler.go:549-571`
**H-2.** IM feishu adapter 无界 `seenEvents`/`seenNonces` map — `internal/im/feishu_adapter.go:132-133`
**H-3.** Tunnel broker WebSocket 写无背压保护（部分路径持锁 I/O）— `internal/tunnel/broker.go`
**H-4.** `readJSON` 提前关闭 `r.Body` — `internal/webui/server.go:328-331`
**H-5.** Harness worker `done` channel 可能阻塞 — `internal/harness/worker.go:90-123`

### Medium (8)

| ID | 文件 | 问题 |
|----|------|------|
| M-1 | `internal/im/feishu_adapter.go:118-120` | `_ = err` 吞掉端口解析错误 |
| M-2 | `internal/harness/auto_init.go:106` | `_ = err` 吞掉初始化错误 |
| M-3 | `internal/a2a/server.go:480` | `handleTaskList` 忽略 JSON 反序列化错误 |
| M-4 | `internal/webui/server.go:319-326` | `writeJSON`/`writeError` 忽略编码错误 |
| M-5 | `internal/context/manager.go` | `CompactSnapshot` 浅拷贝 messages slice |
| M-6 | `internal/subagent/manager.go` | `ForEachRunning` 锁内调用用户回调，可能死锁 |
| M-7 | `internal/session/store.go:787` | JSONL 写入非 atomic，崩溃可丢数据 |
| M-8 | `internal/swarm/team.go` | teammate inbox channel 无界 |

### Low (8)

| ID | 文件 | 问题 |
|----|------|------|
| L-1 | `internal/provider/retry.go` | 重试退避无 jitter（thundering herd） |
| L-2 | `internal/a2a/handler.go:723-725` | `generateID` 使用 timestamp + counter（可预测） |
| L-3 | `internal/webui/server.go:337-358` | `sanitizeConfigForAPI` 忽略 marshal 错误 |
| L-4 | `internal/tunnel/relay_client.go` | `Close` 不等待 goroutine 退出 |
| L-5 | `internal/tool/tool.go:148-161` | `Clone` 持读锁时间较长 |
| L-6 | `internal/agent/agent_tool.go:363-366` | 使用 reflect 设置 WorkingDir |
| L-7 | `internal/knight/budget.go` | 预算操作非 atomic（当前串行执行，未来风险） |
| L-8 | `internal/mcp/oauth.go` | 多处 HTTP response body 正确处理（正面） |

---

## 二、Flutter 移动端 (`mobile/flutter/`) — 22 项

### Critical (3)

**C-1. StreamSubscription 泄漏 — 重复事件和内存增长**
- 文件: `lib/core/providers/session_provider.dart:167-192`
- 问题: `ConnectionNotifier.connect()` 中三个 `.listen()` 返回值未保存，重连时旧 subscription 永远不会 cancel。
- 修复: 将 subscription 存为实例字段，`connect()` 开头 cancel 旧的，`ref.onDispose` 中全部 cancel。

**C-2. Token 明文存储于 SharedPreferences**
- 文件: `lib/core/providers/session_provider.dart:700-708`
- 问题: WebSocket URL（含 `?token=`）完整保存到 SharedPreferences（Android 明文 XML），WorkspaceRecord cache 同样含 token。
- 修复: 持久化前 strip token，改用 `flutter_secure_storage` 存储敏感部分。

**C-3. God File — session_provider.dart 2609 行 15+ 职责**
- 文件: `lib/core/providers/session_provider.dart`
- 问题: 单文件包含 ConnectionNotifier、ChatNotifier、SubagentInfo、ApprovalInfo、AskUserInfo、WorkspaceCacheNotifier、7+ derived provider 等。
- 修复: 拆分为 connection_notifier、chat_notifier、subagent_provider、approval_provider、workspace_cache 等独立文件。

### High (6)

**H-1.** O(n) list recreation on every streaming text chunk — `session_provider.dart:1122-1143`
**H-2.** handleSubagentText/handleToolResult/finalizeStreaming 同为 O(n) — `session_provider.dart:1146-1452`
**H-3.** 无消息分页 — 整个聊天历史常驻内存 — `session_provider.dart:1053-1058`
**H-4.** `Future.delayed` 捕获 `ref` — use-after-dispose — `session_provider.dart:580-590`
**H-5.** WorkspaceRecord.toJson() 序列化含 token 的 URL — `session_provider.dart:1722-1728`
**H-6.** `_workspaceKeyForUrl` 用 base64(URL 含 token) 作 key，可逆 — `session_provider.dart:2567-2568`

### Medium (8)

| ID | 文件 | 问题 |
|----|------|------|
| M-1 | session_provider + connect_screen | 重复的 generic Notifier 模式（_ValueNotifier、_SimpleNotifier） |
| M-2 | chat_screen, approval_sheet 等 | 硬编码颜色值绕过主题系统（6 处+） |
| M-3 | protocol.dart, session_provider 等 | `#4CAF50` magic string 重复 7+ 次 |
| M-4 | features/ 多处 | 缺少 `const` widget constructor |
| M-5 | session_provider + connect_screen | `_saveUrl` 和 `_saveToHistory` 重复管理历史 |
| M-6 | session_provider.dart:939-945 | `_persistResumeState` fire-and-forget 无错误处理 |
| M-7 | connect_screen.dart:234 | 硬编码英文 "or" 未走 i18n |
| M-8 | session_provider.dart:1294-1396 | 业务逻辑嵌入 StateNotifier（tool result 格式化） |

### Low (5)

| ID | 文件 | 问题 |
|----|------|------|
| L-1 | pubspec.yaml:7 | SDK 约束 `>=3.0.0` 过宽 |
| L-2 | test/ | 仅 5 个测试文件，关键路径覆盖不足 |
| L-3 | session_provider.dart:2 | import 审计困难（2600 行文件） |
| L-4 | pubspec.yaml | flutter_lints 版本验证 |
| L-5 | connect_screen.dart:11-35 | `_SimpleNotifier` 构造后 mutation 模式脆弱 |

---

## 三、Relay 服务器 (`ggcode-relay/`) — 24 项

### Critical (4)

**C-1. CheckOrigin 无条件允许所有来源**
- 文件: `main.go:590`
- 修复: 校验 Origin 在允许列表中。

**C-2. Token 在 URL Query String 中传输**
- 文件: `main.go:594`
- 修复: 改用 WebSocket subprotocol 或首条消息传递 token。

**C-3. 零认证机制**
- 文件: `main.go:592-654`
- 修复: 实现 HMAC 签名或 challenge-response 认证。

**C-4. 明文 HTTP（无 TLS）**
- 文件: `main.go:689`
- 修复: 添加 TLS 支持，或强制文档说明需要反向代理 TLS。

### High (5)

**H-1.** `room.history` 无上限增长 — OOM 风险 — `main.go:53,58-69`
**H-2.** `sendCh` 缓冲区 10000 条，per-peer 可占 ~1GB — `main.go:635`
**H-3.** 无 graceful shutdown — `main.go:658-691`
**H-4.** SQLite `SetMaxOpenConns(1)` 读写瓶颈 — `store.go:44`
**H-5.** Token hash 无 salt（SHA-256 明文哈希）— `store.go:326-329`

### Medium (8)

| ID | 文件 | 问题 |
|----|------|------|
| M-1 | main.go:58-69 | `upsertHistoryEvent` O(n) 线性扫描去重 |
| M-2 | main.go:131-320 | readPump/writePump 无 panic recovery |
| M-3 | main.go:378-412 | `handleResume` 持锁调用阻塞 send |
| M-4 | main.go:576-585 | `destroyRoom` 直接 Close conn 与 writePump 竞争 |
| M-5 | main.go:467-497 | `getOrLoadClientRoom` TOCTOU |
| M-6 | main.go:243-277 | `language_change`/`theme_change` 重复代码 |
| M-7 | Dockerfile:5 | `COPY . .` 包含测试文件和构建产物 |
| M-8 | deploy.sh:26-28 | 部署前不运行测试 |

### Low (7)

| ID | 文件 | 问题 |
|----|------|------|
| L-1 | main.go:326-329 | `mustJSON` 静默丢弃 marshal 错误 |
| L-2 | main.go 全文 | 无结构化日志 |
| L-3 | main.go | 无 Prometheus/OTEL metrics |
| L-4 | main.go:635,205,580 | 硬编码 magic numbers |
| L-5 | go.sum | 无 `go mod verify` 审计 |
| L-6 | main.go:684-687 | 健康检查不验证 SQLite |
| L-7 | store.go:316-324 | `relayDBPath` 默认相对路径 |

---

## 四、Desktop 桌面端 (`desktop/`) — 23 项

### Critical (3)

**C-1. HTTP 文件服务器暴露整个工作目录**
- 文件: `file_preview.go:160-166`
- 问题: `http.FileServer(http.Dir(dir))` 提供目录下全部文件，含 `.ggcode/ggcode.yaml`（API key）。
- 修复: 限制只提供目标文件，添加白名单。

**C-2. `SendContent` goroutine 无锁访问 `b.resolved`**
- 文件: `agent_bridge.go:574-584`
- 修复: Unlock 前复制 `b.resolved` 和 `b.cfg.Language` 到局部变量。

**C-3. `SwitchModel` 无锁读写 `b.agent`**
- 文件: `agent_bridge.go:2247-2255`
- 修复: 添加 `b.mu.Lock()/Unlock()` 保护。

### High (5)

**H-1.** `agent_bridge.go` God File（2554 行，~65 个函数）— 需拆分
**H-2.** `setupAgent()` 锁内执行 MCP 启动/插件加载等重 I/O — `agent_bridge.go:208-551`
**H-3.** 13 处 `Save()` 错误静默忽略（`_ = cfg.Save()`）— 多文件
**H-4.** `saveSession`/`RecordTunnelEvent` 中 `b.currentSes` 锁外读写 — `agent_bridge.go:1502-1543`
**H-5.** Mermaid 缓存文件 0644 权限 — `file_preview.go:471-482`

### Medium (9)

| ID | 文件 | 问题 |
|----|------|------|
| M-1 | file_preview.go:641-648 | `findFreePort` TOCTOU 竞争 |
| M-2 | agent_bridge/chat_view/im_window | `strings.Title` deprecated（Go 1.18+） |
| M-3 | chat_view.go:570-585 | Thinking 动画 goroutine 无退出机制 |
| M-4 | file_preview.go:558-563 | `pakoEncode` 忽略 deflate 错误 |
| M-5 | chat_view.go:508-537 | 长对话无虚拟化，widget 持续累积 |
| M-6 | file_preview.go:586-601 | Chroma lexer 结果被丢弃 |
| M-7 | im_bridge.go:162-221 | wechat bot_token 可能通过响应泄露 |
| M-8 | app.go:1262-1267 | 自定义 `max()` 遮蔽 Go 1.21+ 内置 |
| M-9 | agent_bridge vs types.go | `truncate` vs `truncateRunes` 重复实现 |

### Low (6)

| ID | 文件 | 问题 |
|----|------|------|
| L-1 | markdownx/render.go:324-325 | 无用 import 保活声明 |
| L-2 | file_preview.go:603-618 | `isBinaryData` 简单 null-byte 检测 |
| L-3 | agent_bridge.go:555 | `log.Printf` 输出用户消息内容 |
| L-4 | config.go:65-67 | 配置写入非 atomic |
| L-5 | sidebar.go:49 | `fetchingModels` bool 未用 atomic/mutex 保护 |
| L-6 | im_window.go:307 | `BindChannel` 返回值被忽略 |

---

## 五、安全审查 (全项目) — 21 项

### Critical (2)

**C-01. WebUI Token 时序攻击（CWE-208）**
- 文件: `internal/webui/auth.go:35,42`
- 修复: 改用 `subtle.ConstantTimeCompare`。

**C-02. A2A Push Notification SSRF（CWE-918）**
- 文件: `internal/a2a/server.go:773-791`
- 修复: URL allowlist，阻止 RFC 1918/link-local 地址。

### High (7)

**H-01.** WebSocket CheckOrigin 全放行 — `webui/server_websocket.go:17`, `relay/main.go:590`
**H-02.** Relay Token 在 URL Query String — `tunnel/relay_client.go:79`, `relay/main.go:594`
**H-03.** API Key 明文存储 keys.env — `config/api_keys.go:543`
**H-04.** DingTalk AccessToken 日志泄露 — `im/dingtalk_adapter.go:512-513`
**H-05.** Tunnel KDF 固定 salt — `tunnel/crypto.go:30-31`
**H-06.** OAuth State 非 constant-time 比较 — `auth/a2a_oauth.go:97`, `auth/claude_oauth.go:123`, `mcp/oauth.go:541`
**H-07.** CI Apple P12 Password 写入 GITHUB_ENV 明文 — `.github/workflows/release.yml:195`

### Medium (7)

| ID | CWE | 文件 | 问题 |
|----|-----|------|------|
| M-01 | 346 | webui/server_static.go | Token 通过 URL fragment 传输 |
| M-02 | 1021 | webui/server.go | 无 CORS 策略 |
| M-03 | 200 | a2a/server.go:148 | Agent Card 暴露服务器 URL |
| M-04 | 327 | a2a/handler.go:723 | `generateID` 使用 timestamp（可预测） |
| M-05 | 770 | ggcode-relay/main.go | 无 WebSocket 消息大小限制 |
| M-06 | 362 | ggcode-relay/main.go:604 | Room 竞态条件（TOCTOU） |
| M-07 | 326 | ggcode-relay/main.go:689 | 无 TLS |

### Low (5)

| ID | CWE | 文件 | 问题 |
|----|-----|------|------|
| L-01 | 778 | 多处 debug.Log | 可能记录敏感信息 |
| L-02 | 311 | auth/store.go:148 | Auth 目录权限 0755（应为 0700） |
| L-03 | 613 | auth/a2a_token_cache.go | OAuth 缓存无文件锁 |
| L-04 | 400 | ggcode-relay/main.go | 无连接速率限制 |
| L-05 | 759 | config/api_keys.go | Key 迁移无审计日志 |

### 正面安全设计（12 项）

1. A2A API key 使用 `subtle.ConstantTimeCompare`
2. 完善的 3 层 Command Gate
3. AES-GCM + Argon2id Tunnel 加密
4. OAuth2 PKCE S256 正确实现
5. JWT 全面验证（签名+过期+issuer+audience）
6. SQL 参数化查询（无注入风险）
7. Relay Token SHA-256 哈希存储
8. A2A 请求体 4 MiB 限制
9. 路径沙箱遍历防护
10. Config 文件 0600 权限 + 自动修正
11. 明文 API Key 自动迁移
12. Auth Store 原子写入（temp + rename）

---

## 六、SRE/DevOps (全项目) — 26 项

### Critical (2)

**C-1. Python 安装器默认禁用 TLS 证书校验**
- 文件: `python/ggcode_release_installer/cli.py:90-94`
- 问题: `check_hostname = False` + `verify_mode = ssl.CERT_NONE`，下载的二进制和 checksum 均可被 MITM 篡改。
- 修复: 默认启用 TLS 校验，仅 opt-in 环境变量时禁用。

**C-2. Relay Dockerfile 过时 Go 版本 + 缺 `-tags goolm` + 无最佳实践**
- 文件: `ggcode-relay/Dockerfile:1,6`
- 修复: 升级到 `golang:1.26-alpine`，添加 `-tags goolm`，非 root 用户，HEALTHCHECK。

### High (6)

**H-1.** CI 和 release workflow 中 actions 版本不一致 — `.github/workflows/`
**H-2.** CI 无安全扫描步骤（无 govulncheck/gosec/trivy）
**H-3.** Makefile `install` 目标缺 `-tags goolm` — `Makefile:28-31`
**H-4.** 测试中 22+ 处使用 `os.Setenv("HOME")` 而非 `t.Setenv`
**H-5.** CI 缺 golangci-lint 步骤（有 `.golangci.yml` 但 CI 未用）
**H-6.** 无主程序 Dockerfile

### Medium (10)

| ID | 文件 | 问题 |
|----|------|------|
| M-1 | release.yml:60-61 | GoReleaser `version: latest` 未锁定 |
| M-2 | ci.yml:25 | CI 在 PR 上运行 integration 测试（需 API key） |
| M-3 | Dockerfile:7-10 | 最终镜像以 root 运行 |
| M-4 | relay/main.go | 无健康检查端点 |
| M-5 | relay/main.go + 全局 | 无结构化日志（仅 2 文件用 slog） |
| M-6 | 全局 | 无 Prometheus metrics 导出 |
| M-7 | npm.yml:52 | `--provenance=false` 关闭 npm provenance |
| M-8 | docs/ | 缺架构文档和运维手册 |
| M-9 | mobile-release.yml:20 | `cancel-in-progress: true` 可能中断发布 |
| M-10 | release.yml:37-38 | `grep -v` 过滤脆弱 |

### Low (8)

| ID | 文件 | 问题 |
|----|------|------|
| L-1 | Makefile:13 | build-desktop ldflags 未注入 internal/version |
| L-2 | .goreleaser.yaml:97 | changelog 排除重要 chore 提交 |
| L-3 | verify-ci.sh:18-19 | `GIT_CONFIG_GLOBAL=/dev/null` 可能残留 |
| L-4 | publish-site-branch.sh:170 | `git push --force` |
| L-5 | docs/releases/README.md | `release-process.md` 文件不存在 |
| L-6 | ci.yml | 无 job timeout-minutes |
| L-7 | publish-pypi.yml | 未运行 Python 测试 |
| L-8 | .goreleaser.yaml:19 | 未指定 `goarm` 版本 |

---

## 七、测试与代码质量 (全项目) — 42 项

### 关键统计

| 指标 | 数值 |
|------|------|
| Go 源文件数 | 535 |
| Go 测试文件数 | 366 |
| 源代码行数 | 169,533 |
| 测试代码行数 | 128,387 |
| 测试/源代码比 | 75.7% |
| 测试函数总数 | 4,820 |
| `t.Parallel()` 使用次数 | 仅 10 |
| `time.Sleep` 测试中使用 | 495 次 |
| `os.Chdir` 测试中使用 | 92 次 |
| `goleak` 库 | 未使用 |

### 测试覆盖缺口 (Critical)

| ID | 模块 | 问题 |
|----|------|------|
| Q-C02 | `internal/daemon/` | 1050 行代码零测试覆盖 |
| Q-C03 | Makefile / CI | `make test` 和 CI 均未使用 `-race` flag |

### God Files Top 10

| 排名 | 文件 | 行数 | 测试 |
|------|------|------|------|
| 1 | `internal/im/qq_adapter.go` | 1,538 | ~800 |
| 2 | `internal/tui/tunnel.go` | 1,483 | ~700 |
| 3 | `cmd/ggcode/root.go` | 1,430 | ~200 |
| 4 | `cmd/ggcode/daemon.go` | 1,399 | 0 |
| 5 | `internal/im/daemon_bridge.go` | 1,369 | ~1,200 |
| 6 | `desktop/.../agent_bridge.go` | 3,347 | 有限 |
| 7 | `internal/config/config.go` | 1,305 | 1,572 |
| 8 | `internal/tui/i18n_en.go` | 1,291 | 0 |
| 9 | `internal/tui/i18n_zh.go` | 1,269 | 0 |
| 10 | `internal/tui/inspector_panel.go` | 1,263 | 0 |

### 超大函数 Top 5

| 函数 | 文件 | 行数 |
|------|------|------|
| `setupAgent()` | desktop/agent_bridge.go:208 | 344 |
| `inspectorText()` | tui/inspector_panel.go:937 | 326 |
| `DefaultConfig()` | config/config.go:436 | 307 |
| `toolDescription()` | desktop/agent_bridge.go:936 | 290 |
| `NewLSPTools()` | tool/lsp.go:497 | 290 |

---

## 与 Round 6 对比

| 方面 | Round 6 | Round 7 |
|------|---------|---------|
| Critical | 17 | 23 |
| High | 34 | 49 |
| Medium | 45 | 63 |
| Low | 36 | 47 |
| Sub-agent 数 | 6 | 7（新增测试质量专项） |
| 总发现数 | 132 | 182 |
| 新增关注 | Desktop 安全 | 并发安全（race flag）、供应链安全（Python TLS）、God File 拆分 |

### Round 6 已修复项
- Broker `onCommand`/`onConnect` 数据竞争：已通过 tunnel event threading（commit `2095536e`）间接缓解
- `start_command` 显示统一：已修复

### Round 7 新增关注
1. **并发安全测试缺失**：`-race` flag 从未在 CI 或本地使用
2. **供应链安全**：Python TLS 禁用、npm provenance 关闭
3. **A2A goroutine 泄漏**：`CancelTask` 不关 `done` channel
4. **Flutter 性能**：O(n) list rebuild per streaming chunk
5. **测试基础设施**：无 goleak、无并行测试、daemon 零覆盖

---

## 建议的修复优先级

### P0 — 立即修复（安全/稳定性阻塞）
1. WebUI Token 改用 `subtle.ConstantTimeCompare` (sa-5 C-01)
2. A2A `CancelTask` 关闭 `done` channel (sa-1 C-1)
3. Flutter StreamSubscription 泄漏 (sa-2 C-1)
4. Python 安装器启用 TLS 校验 (sa-6 C-1)
5. CI/测试添加 `-race` flag (sa-7 Q-C03)

### P1 — 本周修复
6. Desktop `file_preview` 限制文件服务范围 (sa-4 C-1)
7. Desktop `agent_bridge.go` 数据竞争 (sa-4 C-2/C-3)
8. WebUI Server 字段加锁 (sa-1 C-2)
9. Relay Dockerfile 修复 (sa-6 C-2)
10. Flutter Token 存储安全化 (sa-2 C-2)

### P2 — 下个迭代
11. A2A Push Notification SSRF 防护 (sa-5 C-02)
12. `session_provider.dart` 拆分 (sa-2 C-3)
13. Flutter O(n) streaming 优化 (sa-2 H-1/H-2)
14. `agent_bridge.go` 拆分 (sa-4 H-1)
15. `cmd/ggcode/root.go` + `daemon.go` 拆分 (sa-7 Q-H13/H14)

### P3 — 持续改进
16. 引入 `goleak` goroutine 泄漏检测 (sa-7 Q-M11)
17. `internal/daemon/` 添加测试 (sa-7 Q-C02)
18. 结构化日志迁移 (slog) (sa-6 M-5)
19. i18n 键完整性测试 (sa-7 Q-M01)
20. Flutter 测试覆盖扩展 (sa-2 L-2)
