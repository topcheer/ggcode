# Full Code Review — Round 5

**Date**: 2025-05-23
**Review Team**: 6 sub-agents (Go Backend / Flutter / Desktop / SRE / Security / UX)
**Commit Baseline**: `8fb669c5`

---

## 综合统计

| 审查维度 | Critical | High | Medium | Low | 合计 |
|----------|----------|------|--------|-----|------|
| Go 后端 (API/错误/资源) | 0 | 3 | 13 | 11 | 27 |
| Flutter 移动端 | 3 | 13 | 16 | 12 | 44 |
| Desktop GUI | 0 | 2 | 10 | 8 | 20 |
| SRE 可靠性 | 0 | 4 | 15 | 11 | 30 |
| 安全 | 2 | 4 | 9 | 6 | 21 |
| TUI/UX 一致性 | 0 | 2 | 12 | 8 | 22 |
| **合计** | **5** | **28** | **75** | **56** | **164** |

---

## Critical — 必须立即修复 (5)

| ID | 组件 | 问题 | 影响 |
|----|------|------|------|
| SEC-001 | ggcode-relay | Relay 服务器无认证，任何能到达的人可创建/加入 room 并拦截 tunnel 流量 (CWE-306) | 全部 tunnel 通信暴露 |
| SEC-002 | relay + webui | WebSocket `CheckOrigin` 返回 `true`，CSRF 攻击可伪造连接 (CWE-346) | 跨站 WebSocket 劫持 |
| FL-01 | session_provider.dart | 2518 行巨型 god file，含 10+ Notifier/Model/Provider，无法维护和测试 | 代码质量灾难 |
| FL-02 | session_provider.dart:151-176 | Stream subscription 永不 cancel，reconnect 时泄漏 3 个监听器，导致重复事件处理 | 聊天状态损坏 |
| FL-03 | .env (committed) | 包含 Apple App Store Connect 真实 API 凭证 | 凭证泄露 |

---

## High — 优先修复 (18)

### 安全 (4)
| ID | 问题 |
|----|------|
| SEC-003 | Relay token 在 URL query param 中传输，可通过日志/Referrer 泄露 (CWE-598) |
| SEC-004 | Relay 无 TLS，`http.ListenAndServe()` 接受明文 `ws://` (CWE-319) |
| SEC-005 | IM adapter 的 `bot_token`/`appsecret` 通过 config API 泄露（sanitizeMap 只检查 api_key/api_secret） |
| SEC-013 | WebUI REST API 无 CSRF 保护 (CWE-352) |

### 可靠性 (4)
| ID | 问题 |
|----|------|
| SRE-004 | 整个代码库零 metrics 基础设施，无 Prometheus/counters |
| SRE-009 | WebUI `Server.Close()` 只关闭 listener，不优雅 drain WebSocket |
| SRE-021 | 无 Dockerfile，relay 部署不可复现 |
| SRE-026 | WebUI API 无 rate limiting，可被 flood 攻击耗尽 token |

### Flutter (4)
| ID | 问题 |
|----|------|
| FL-04 | `ChatMessage.toJson()` 可能序列化 `streaming: true`，缓存恢复后显示卡住的流式指示器 |
| FL-05 | `ChatMessage` 不实现 `==`/`hashCode`，`copyWith` 无法设置 nullable 字段回 null |
| FL-06 | 无路由框架，无 deep link 支持，Android 返回键直接退出 |
| FL-07 | 测试几乎为零：smoke test 空实现，无 widget/integration test |

### Desktop (2)
| ID | 问题 |
|----|------|
| D-003 | `startChat()` 直接写 `chatViewRef.bridge` 无锁，与 `pollRefresh` 数据竞争 |
| D-010 | TUI/Desktop ~1500 行重复代码（formatAskUserResult 等），将产生行为漂移 |

### Go 后端 (3)
| ID | 问题 |
|----|------|
| RES-01 | swarm `Manager.Shutdown()` 不等待 goroutine 退出，teardown 时可能访问已释放资源 |
| ERR-01 | MCP `Client.Close()` 丢弃 `cmd.Wait()` 错误，可能泄漏僵尸进程 |
| RES-05 | MCP `Client.Close()` 不关闭 `httpClient`，HTTP 连接池永不释放 |

### TUI (2)
| ID | 问题 |
|----|------|
| TUI-006 | `/knight` 命令约 34 个用户可见字符串硬编码英文，零 i18n |
| TUI-012 | TUI/Desktop 各自独立 tool 分类系统，渲染逻辑差异大（截断阈值差 15 倍） |

---

## Medium — 按类别分组 (75)

### 架构 & 代码质量
- broker `onCommand`/`onConnect` 回调无锁保护
- broker `outbound` 切片无上限，OOM 风险
- relay `room.history` 无界增长
- TUI Model 120+ 字段值语义拷贝，map 拷贝开销大
- TUI 20+ optional panel pointer fields，应重构为 panel stack
- Flutter `AppColors` 全局可变状态绕过 widget tree rebuild
- Flutter 翻译系统用可变全局 Map，语言切换不触发 rebuild

### 数据持久化
- session JSONL 崩溃后可能有残缺 JSON 行，Load 会失败
- harness event log 不调 `f.Sync()`
- agent loop panic 未在 `RunStreamWithContent` 顶层 recover

### SRE 运维
- 无结构化日志（纯文本格式）
- 无 log level（只有 verbose/normal 两档）
- relay 无 TLS、无 signal handler、无优雅关停
- 无 circuit breaker，provider 20 次重试最坏 10 分钟
- 无内存限制、无磁盘用量限制
- CI integration test 无 retry 机制

### Flutter UI
- `ChatScreen` 1169 行违反 SRP
- Message bubble `_parseTextSegments` 每次 build 重新解析
- `ask_user_screen` 无 SafeArea，键盘遮挡输入
- crypto.dart key 派生不安全（直接 UTF-8 编码，无长度校验）
- Android release 未启用 minify/shrink

### Desktop
- `pollRefresh` 100ms 无 dirty check 浪费 CPU
- `SetChatMessages` 每次流式 chunk 替换整个切片
- HTML 预览开放整个目录的 HTTP 访问
- Mermaid 代码无大小限制直接发到外部服务

---

## 修复路线图

### Phase 1 — 紧急修复（1-2 天）
1. SEC-001/002: Relay 加认证 + WebSocket CheckOrigin 校验
2. FL-02: Stream subscription 生命周期管理
3. FL-03: 轮换 .env 中暴露的 Apple 凭证
4. SEC-005: sanitizeMap 增加 bot_token/appsecret 等字段

### Phase 2 — 短期修复（1 周）
5. RES-01/ERR-01/RES-05: 资源管理修复
6. SRE-009/011: 优雅关停
7. SRE-026: WebUI API rate limiting
8. D-003: Desktop bridge swap 加锁
9. TUI-006: `/knight` 命令 i18n 化

### Phase 3 — 中期改进（2-4 周）
10. FL-01: 拆分 session_provider.dart
11. D-010/TUI-012: 提取共享 display 包
12. SRE-004: 添加 metrics 基础设施
13. FL-06/07: 添加 go_router + 测试
14. SEC-003/004: Relay token 移到 header + TLS

### Phase 4 — 长期重构（1-2 月）
15. TUI-004/005: Panel 系统重构
16. SRE-021: 添加 Dockerfile + 运维文档
17. FL-04/05: ChatMessage 用 freezed
18. 全量 i18n 覆盖
