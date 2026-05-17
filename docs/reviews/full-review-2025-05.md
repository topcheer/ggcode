# ggcode 全量代码评审报告

**评审日期**: 2026-05-17
**评审范围**: 全部 845 个 Go 源文件
**评审团队**: 4 个并行 reviewer

---

## 总览

| 模块 | 评分 | Critical | High | Medium | Low |
|---|---|---|---|---|---|
| Desktop GUI | 6.5/10 | 3 | 6 | 12 | 8 |
| TUI + IM | 7.0/10 | 4 | 6 | 7 | 6 |
| Agent/Config/Core | 7.5/10 | 3 | 8 | 12 | 10 |
| CI/CD + 安全 | 7.5/10 | 0 | 3 | 8 | 7 |
| **合计** | **7.1/10** | **10** | **23** | **39** | **31** |

---

## Critical 问题（必须修复）

### 1. Discord/钉钉 WebSocket 并发写 — 数据竞争
- **文件**: `internal/im/discord_adapter.go:183,247,266`, `internal/im/dingtalk_adapter.go:248`
- gorilla/websocket 的 Conn 不是并发写安全的。heartbeat 和主循环同时 WriteMessage 会导致 panic。
- **修复**: 引入 writeMu mutex，序列化所有 WebSocket 写操作。

### 2. `preWriteHook` 全局变量 — 多 Agent 实例竞争
- **文件**: `internal/tool/atomic_write.go:14`
- 全局函数指针，多 Agent（主 Agent + 子 Agent + swarm）同时运行时会竞争。
- **修复**: 移到每个 Tool 或 Registry 实例上。

### 3. `toolWorkingDir` 全局变量 — Swarm 同伴互相覆盖工作目录
- **文件**: `internal/agent/agent_tool.go:256`
- 全局 map，多个 Agent 实例会覆盖彼此的工作目录。
- **修复**: 使用每个 Tool 的 WorkingDir 字段。

### 4. Desktop `imRound` data race — strings.Builder 并发写入
- **文件**: `desktop/ggcode-desktop/agent_bridge.go:53,324-414`
- imRound 在 agent 事件回调中读写，strings.Builder 不是并发安全的。
- **修复**: 加 mutex 或改为局部变量。

### 5. Desktop `prettifyToolName` map 每次调用重建 — GC 压力
- **文件**: `desktop/ggcode-desktop/chat_view.go:1189-1211`
- 每次渲染 tool 消息重建 ~30 键 map，长对话中造成显著 GC 压力。
- **修复**: 提取为包级别 var。

### 6. Desktop `pendingImage` data race
- **文件**: `desktop/ggcode-desktop/chat_view.go:41-43,174-178`
- tryPasteImageFromClipboard 在独立 goroutine 写 pendingImage，UI goroutine 读。
- **修复**: 通过 fyne.Do 回调到 UI 线程再写。

### 7. HandleInbound 持锁时调外部 bridge — 潜在死锁
- **文件**: `internal/im/runtime.go:371-465`
- SubmitInboundMessage 同步阻塞等待 resp，如果 TUI Update 尝试获取 Manager 锁会死锁。
- **修复**: 文档化约束或改为完全异步。

### 8. WeChat botToken 明文存储
- **文件**: `internal/im/wechat_adapter.go:86,257`
- context_token 持久化到绑定存储文件无加密，debug.Log 打印截断 token。
- **修复**: 加密持久化，移除 debug 日志中的 token 片段。

### 9. `run_command` 沙箱绕过
- **文件**: `internal/tool/run_command.go`
- working_dir 参数不受 AllowedDirs 限制，可通过 `cd restricted && cat secret` 绕过。
- **修复**: 对 working_dir 参数做沙箱验证。

### 10. `config.Save` 并发写入竞争
- **文件**: `internal/config/config_save.go:140-260`
- 两次并发 Save 可能导致文件损坏或丢失更新。
- **修复**: 添加文件级 mutex。

---

## High 问题（应当修复）

### Desktop (6 个)
1. **Thinking 动画 goroutine 泄漏** — 用户关闭窗口后永不退出
2. **WeChat QR 轮询 goroutine 泄漏** — 关闭窗口后继续运行 5 分钟
3. **临时文件不清理** — 剪贴板图片和 icon 文件残留磁盘
4. **Shortcuts 重复注册** — 每次 startChat 都添加新 handler
5. **Session 恢复 race** — rebuildFromMessages 与 streaming 并发
6. **Adapter config apply 代码重复** — 同样逻辑出现 3+ 次

### TUI/IM (6 个)
1. **fanOutSend 阻塞** — 无响应适配器会阻塞 agent loop
2. **seenMessages map 无大小限制** — 群聊高峰可无限增长
3. **裸 goroutine** — dingtalk/whatsapp 适配器 panic 会崩溃进程
4. **Discord 交互回调忽略 context 取消**
5. **TUI Model 值接收者 + 指针字段** — 脆弱架构
6. **pty_harness.go 裸 goroutine**

### Agent/Config/Core (8 个)
1. **Subagent Spawn goroutine 泄漏** — 超时时 goroutine 不退出
2. **Swarm CancelAll 无宽限期** — 可能丢失部分输出
3. **Session Store append 无 fsync** — 崩溃丢消息
4. **deepCopyConfig 失败返回 nil** — 可能导致 panic
5. **reflect 设置 unexported field** — 结构体变更会 panic
6. **PathSandbox 符号链接绕过** — allowed_dirs 可被绕过
7. **contextManager 未加锁访问**
8. **config.vendor_defaults.go 自动生成未验证**

### CI/安全 (3 个)
1. **GGCODE.md CI 文档与实际不一致** — build tags 描述错误
2. **npm provenance 被禁用** — 降低供应链可追溯性
3. **Windows smoke test continue-on-error** — 损坏二进制可能发布

---

## 整体评价

### 优点
1. **接口设计清晰** — Tool、PermissionPolicy、Provider、ContextManager 解耦良好
2. **优雅关闭** — Agent.Close() 正确级联取消到子 agent 和 swarm
3. **原子文件写入** — writeFileAtomic 模式一致使用
4. **IM Manager 状态机** — 并发控制大部分正确，适配器接口设计清晰
5. **CI/CD 成熟** — 脚本健壮、三平台构建、SBOM 生成
6. **无 secret 泄露** — 所有 API key 使用环境变量
7. **Build tags 一致** — goolm tag 在 CI、发布、本地开发间统一

### 不足
1. **全局变量过多** — preWriteHook、toolWorkingDir 等应改为实例级
2. **Goroutine 生命周期管理** — 多处泄漏，缺乏 context 取消
3. **代码重复** — IM panel handler、adapter config apply 逻辑重复严重
4. **测试隔离不足** — 部分 TUI/IM 测试依赖时序
5. **Desktop 大 session 性能** — 同步渲染所有消息会冻结 UI

---

## Top 10 优先修复项

| 优先级 | 问题 | 模块 | 工作量 |
|---|---|---|---|
| 1 | Discord/钉钉 WebSocket 写竞争 | IM | 小 |
| 2 | preWriteHook 全局竞争 | Core | 中 |
| 3 | toolWorkingDir 全局竞争 | Core | 中 |
| 4 | config.Save 并发写入 | Config | 小 |
| 5 | Desktop imRound data race | Desktop | 小 |
| 6 | Desktop thinking goroutine 泄漏 | Desktop | 小 |
| 7 | Desktop QR 轮询 goroutine 泄漏 | Desktop | 小 |
| 8 | run_command 沙箱绕过 | Core | 中 |
| 9 | fanOutSend 阻塞 agent loop | IM | 中 |
| 10 | Windows smoke test continue-on-error | CI | 小 |
