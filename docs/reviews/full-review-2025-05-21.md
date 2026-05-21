# Full Codebase Review — 2025-05-21

Reviewers: 5 parallel reviewers covering all modules.

## Summary

| Reviewer | Scope | Critical | High | Medium | Low |
|----------|-------|----------|------|--------|-----|
| tui-reviewer | internal/tui/ | 3 | 6 | 8 | 6 |
| core-reviewer | agent, provider, tool, harness | 4 | 7 | 9 | 7 |
| security-reviewer | auth, a2a, subagent, swarm, permission, tunnel | 4 | 6 | 7 | 6 |
| infra-reviewer | im, daemon, webui, config, session | 1 | 4 | 4 | 3 |
| platform-reviewer | mobile, desktop, CI/CD, release | *pending* | | | |

---

## TUI Layer (tm-2)

### Critical
- **C-1**: chat_bridge.go — 全局计数器无并发保护（低风险但结构脆弱）
- **C-2**: spinner.go:215,342 — regexp.MustCompile 热路径每次重新编译
- **C-3**: submit.go:249-265 — streamBatch ticker goroutine program.Send 可能阻塞

### High
- **H-1**: update_keys.go:517-528 — Enter 键处理中清理逻辑是死代码
- **H-2**: update_keys.go:281-306 — HarnessCheckpointConfirm 复用 diffCursor 状态
- **H-3**: tunnel.go:379-425 — handleTunnelClientCommand 从非 TUI goroutine 访问 Model
- **H-4**: tunnel.go:490-514 — handleTunnelClientConnect 数据竞争（直接访问 Model 字段）
- **H-5**: subagent_follow.go:276-278 — tool call/result 匹配可能偏移
- **H-6**: submit.go:286-291 — writingStatusSent 直接 Send 与 batch 并发

### Medium
- **M-1**: view.go:118-144 — conversationPanelHeight 重复计算渲染
- **M-2**: update_keys.go:14-577 — handleKeyPress 577 行，圈复杂度过高
- **M-3**: chat_bridge.go:504-522 — stripAnsiForChat 只处理 SGR 格式
- **M-4**: 多文件 — 硬编码字符串未走 i18n
- **M-5**: submit.go:58-59 — AppendMessageToDisk 错误被吞掉
- **M-6**: submit.go:87-109 — goroutine 闭包捕获 Model 值语义
- **M-7**: tunnel.go:211-214 — truncateRunes 大文本分配大量内存
- **M-8**: model.go:71-254 — Model struct 150+ 字段，panel 注册分散

---

## Agent/Provider/Tool (tm-3)

### Critical
- **C-01**: edit_file.go — 并发写入无文件锁保护
- **C-02**: edit_match.go — 行号锚点可能静默错误匹配
- **C-03**: run_command.go — working_dir 沙盒逃逸向量
- **C-04**: web_fetch.go — DNS 重绑定攻击绕过私有网络保护

### High
- **H-01**: agent.go — maxIterations 默认 0（无限），可能死循环
- **H-02**: agent.go — context 传播在 compact 操作中不完整
- **H-03**: provider/openai.go — 流重试可能发出重复 tool call
- **H-04**: provider/gemini.go — 流迭代器未关闭，连接泄漏
- **H-05**: provider/retry.go — 字符串匹配状态码脆弱
- **H-06**: harness/task.go — 单个损坏文件阻塞整个队列
- **H-07**: agent_tool.go — panic recover 静默丢弃，LLM 无法区分

### Medium
- **M-01**: agent_compact.go — compactMessages 未检查 ctx.Done()
- **M-02**: agent_autopilot.go — 循环防护阈值过于简单
- **M-03**: provider/anthropic.go — StopReason 字符串比较脆弱
- **M-04**: provider/adaptive_cap.go — saveAdaptiveCaps 无锁读取
- **M-05**: harness/events.go — 全量读取 JSONL 到内存
- **M-06**: tool/edit_file.go — 缩进检测只检查前 200 行
- **M-07**: tool/command_gate.go — shell 混淆可绕过阻止列表
- **M-08**: provider/openai.go — 流重试间 usage/outputChars 未重置
- **M-09**: agent.go — reactiveCompactRetries 无上限

---

## Auth/A2A/Security (tm-5)

### Critical
- **C1**: a2a_oauth.go:443-453 — JWT 验证回退绕过 issuer/audience 检查
- **C2**: a2a_oauth.go:397-434 — JWT alg 字段来自未验证解析，算法混淆攻击
- **C3**: a2a_oauth.go:426-431 — HMAC JWT 用公开 client_id 做密钥
- **C4**: relay_client.go:60 — Tunnel relay token 在 WebSocket URL 中泄露

### High
- **H1**: 所有 OAuth2/JWKS 调用使用 http.DefaultClient（无超时）
- **H2**: a2a_oauth.go:498-530 — OIDC Discovery 未验证返回 issuer
- **H3**: sandbox.go — 缺少 EvalSymlinks，路径遍历风险
- **H4**: handler.go:723-726 — A2A task ID 可预测（时间戳+计数器）
- **H5**: GetTask 返回可变内部指针
- **H6**: Token cache 文件权限加载时未强制执行

### Medium
- JWKS 缓存无重放保护
- Swarm teammates 无硬性上限
- Sub-agent 共享 config 引用
- Session token 只有 128 位
- Permission mode TOCTOU 竞争条件
- Knight 预算检查非原子性
- JWKS 缓存硬编码 TTL

---

## Top Priority Fixes (Across All Reviews)

1. **JWT 算法混淆** (sec C2) — 最高安全优先级
2. **JWT issuer 回退** (sec C1) — 安全
3. **Tunnel token URL 泄露** (sec C4) — 安全
4. **WebSocket CheckOrigin 无验证** (infra C-1) — CSRF
5. **HTTP 无请求体大小限制** (infra H-1) — DoS
6. **WebSocket 无连接数限制** (infra H-2) — 资源耗尽
7. **DNS 重绑定** (core C-04) — SSRF
8. **tunnel 数据竞争** (tui H-4) — 稳定性
9. **edit_file 并发保护** (core C-01) — 数据完整性
10. **maxIterations 默认值** (core H-01) — 死循环风险
11. **IM seenMessages 无界增长** (infra H-4) — 内存泄漏
12. **regexp 热路径** (tui C-2) — 性能

## IM/Daemon/WebUI/Config (tm-4)

### Critical
- **C-1**: server_websocket.go:17 — WebSocket CheckOrigin 始终返回 true，无 CSRF 防护

### High
- **H-1**: server.go:328 — WebUI HTTP 处理无请求体大小限制（OOM/DoS）
- **H-2**: server_websocket.go:84 — WebSocket 无连接数限制（资源耗尽）
- **H-3**: config_save.go:62 — Config Save 默认权限 0644 可能泄露 API 密钥
- **H-4**: runtime.go:46 — IM seenMessages map 无界增长（内存泄漏）

### Medium
- **M-1**: store.go:872 — Session appendRecordLine 非原子写（可容忍，有容错）
- **M-2**: server_websocket.go:280 — Legacy WS mode 的 wsSend 使用全局 RLock
- **M-3**: store.go:245 — Session store 无 path traversal 检查
- **M-4**: config_save.go:371 — saveInstanceDeltaYAML 使用非原子 os.WriteFile

### Low
- **L-1**: follow.go — Daemon keyboard goroutine 泄漏
- **L-2**: auto.go:66 — Memory LoadAll 无排序保证
- **L-3**: server.go:304 — WebUI token 通过 URL query param 传递

### Positive Observations
- Echo suppression 设计正确
- WebSocket bridge mode 并发安全 (per-connection writeCh)
- Session JSONL 有 malformed line 容错 + checkpoint 支持
- Config env 扩展无注入风险
- AtomicWriteFile 工具实现正确

---

*platform-reviewer 报告待补充。*
