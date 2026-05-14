# ggcode 安全审查报告

**审查员**: security-reviewer  
**审查范围**: `internal/` 全部核心模块  
**审查日期**: 2025-07-11  

---

## 审查总结

| 风险等级 | 数量 | 说明 |
|---------|------|------|
| 🔴 高危 | 2 | 需要立即修复 |
| 🟡 中危 | 6 | 需要在近期版本修复 |
| 🟢 低危 | 5 | 建议改进，非紧急 |

---

## 🔴 高危漏洞

### H1: WebUI WebSocket CheckOrigin 始终返回 true — CSRF 风险

**文件**: `internal/webui/server_websocket.go:17`

```go
var upgrader = websocket.Upgrader{
    CheckOrigin: func(r *http.Request) bool { return true },
}
```

**问题**: WebSocket 升级请求不验证 `Origin` 头，任意恶意网站可通过跨站 WebSocket 劫持 (CSWSH) 连接到本地 WebUI，读取会话内容、注入命令。

**风险**: 攻击者构造恶意页面，当用户运行 ggcode 时，自动连接 WebSocket 并发送命令或窃取对话数据。

**修复建议**: 验证 Origin 头匹配本地地址：
```go
CheckOrigin: func(r *http.Request) bool {
    origin := r.Header.Get("Origin")
    return origin == "" || strings.HasPrefix(origin, "http://127.0.0.1:") || strings.HasPrefix(origin, "http://localhost:")
}
```

### H2: WebUI Auth Token 比较使用非常量时间 — 时序攻击

**文件**: `internal/webui/auth.go:35,42`

```go
if strings.TrimPrefix(auth, "Bearer ") == s.authToken {
    ...
}
if r.URL.Query().Get("token") == s.authToken {
    ...
}
```

**问题**: Token 比较使用标准字符串相等运算符 (`==`)，而非常量时间比较。攻击者可通过时序侧信道逐步推断 token 内容。相比之下，A2A server 的 API key 认证正确使用了 `subtle.ConstantTimeCompare`。

**修复建议**: 使用 `crypto/subtle.ConstantTimeCompare`:
```go
if subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(auth, "Bearer ")), []byte(s.authToken)) == 1 {
```

---

## 🟡 中危漏洞

### M1: MCP WebSocket 连接缺少 TLS 验证和 SSRF 防护

**文件**: `internal/mcp/client.go:108`

```go
conn, _, err := websocket.DefaultDialer.DialContext(ctx, c.url, headers)
```

**问题**: MCP 客户端连接远程 WebSocket 服务器时，使用默认的 `websocket.DefaultDialer`，没有 SSRF 防护（与 `web_fetch.go` 形成对比），也没有自定义 TLS 配置。MCP HTTP transport 同样缺少 SSRF 防护 (`client.go:92-101`)。

**风险**: 如果 LLM 通过配置注入将 MCP URL 指向内网地址 (如 `http://169.254.169.254/latest/meta-data/`)，可能泄露云环境元数据。

**修复建议**: 对 MCP 远程连接添加与 `web_fetch.go` 相同的私有网络过滤。

### M2: MCP stdio transport 执行任意命令无沙箱

**文件**: `internal/mcp/client.go:119`

```go
c.cmd = exec.CommandContext(ctx, c.command, c.args...)
```

**问题**: MCP 服务器配置中的 `command` 和 `args` 直接作为进程启动，没有经过 `CommandGate` 检查或沙箱限制。如果配置文件中注入恶意 MCP 服务器，将以用户权限执行任意命令。

**修复建议**: 
1. 对 MCP 服务器 command 做基本路径验证（拒绝包含 shell 元字符的命令）
2. 添加 MCP 服务器安装确认机制（首次连接需用户批准）

### M3: keys.env 写入 API 密钥时未转义单引号 — 注入风险

**文件**: `internal/config/api_keys.go:535`

```go
fmt.Fprintf(&b, "export %s='%s'\n", k, existing[k])
```

**问题**: 如果 API 密钥包含单引号 (`'`)，它会跳出引号上下文，导致 shell 注入。例如密钥值为 `x'; rm -rf /; echo '` 时，`keys.env` 将包含恶意命令。虽然 `keys.env` 不是自动 source 的（需要手动操作），但仍构成风险。

**修复建议**: 转义单引号：`strings.ReplaceAll(existing[k], "'", "'\\''")`

### M4: Bypass/Autopilot 模式下命令门控 Ask 被静默降级

**文件**: `internal/tool/run_command.go:205-206`

```go
if t.isBypassMode() {
    debug.Log("run_command", "ASK->ALLOW (bypass mode): %s", gateResult.Reason)
}
```

**问题**: Bypass 和 Autopilot 模式将所有 "Ask" 级别的命令静默放行，包括 `$()` 命令替换、sudo、网络管道等。虽然有 debug 日志，但无用户可见的审计追踪。

**风险**: 在 prompt injection 攻击场景下，恶意输入可诱导 LLM 执行危险命令而无需用户确认。

**修复建议**: 在 Autopilot 模式下，至少对 `sudo` 和 `curl|sh` 等高危命令保持用户确认。

### M5: WebUI bind 地址和 CORS 配置宽松

**文件**: `internal/webui/server.go:247`

```go
func (s *Server) Start(addr string) (string, error) {
```

**问题**: WebUI 默认监听 `127.0.0.1:0`，这是安全的。但代码没有强制约束地址参数，调用者可传入 `0.0.0.0` 使其暴露在网络上。同时 WebSocket upgrader 的 `CheckOrigin: true`（见 H1）使这种暴露更加危险。

**修复建议**: 在 `Start()` 方法中验证 addr 仅绑定 localhost 地址。

### M6: A2A `allow_unauthenticated` 可暴露完整 agent 控制权

**文件**: `internal/a2a/server.go:264-266`

```go
if s.allowUnauthenticated {
    return true
}
```

**问题**: 当配置 `a2a.auth.allow_unauthenticated: true` 时，A2A 服务器允许任何来源的请求执行任务，没有认证。如果同时配置了 `a2a.host: 0.0.0.0`，则暴露在网络上。

**修复建议**: 当 `allow_unauthenticated` 为 true 时，强制绑定 `127.0.0.1`，或至少在日志中输出醒目的安全警告。

---

## 🟢 低危问题

### L1: PathSandbox 未防范 TOCTOU 竞争条件

**文件**: `internal/permission/sandbox.go:79-94`

**问题**: `Allowed()` 方法在检查时解析符号链接，但在实际文件操作前路径可能已被修改（例如符号链接被替换）。这是一个经典的 TOCTOU 问题。

**风险**: 在实践中风险较低，因为攻击窗口极小，且攻击者需要在同一机器上有本地访问权限。

### L2: `command_gate.go` 的 block 规则仅匹配常见 shell 命令

**文件**: `internal/tool/command_gate.go:85-124`

**问题**: Block 规则使用正则匹配，可通过多种方式绕过：
- 使用 shell 变量间接引用：`$rm -rf /`（但 `$()` 会被 Ask 规则捕获）
- 使用 base64 编码执行：`echo cm0gLXJmIC8= | base64 -d | sh`（但 `|sh` 会被 Ask 捕获）
- 使用十六进制转义：`$'\x72\x6d' -rf /`

**风险**: 多层防护（pre-checks + block + ask + permission policy）降低了绕过风险。实际攻击需要绕过所有层。

### L3: OAuth Token 缓存文件使用可预测的文件名

**文件**: `internal/auth/a2a_token_cache.go:134`

```go
func (tc *TokenCache) path(provider string) string {
    return filepath.Join(tc.dir, provider+".json")
}
```

**问题**: Token 缓存文件名基于 provider 名称，可预测。虽然文件权限为 0600（正确），但在多用户共享临时目录场景下仍有风险。

### L4: Provider HTTP 通信未验证 TLS 证书 pinning

**文件**: `internal/provider/http_transport.go`

**问题**: Provider HTTP transport 使用 Go 默认的 TLS 验证，没有证书 pinning。这允许具有 CA 信任的中间人攻击。

**风险**: 在实践中风险极低，因为 API 端点通常已使用标准 CA 签发的证书。

### L5: Session 文件未加密存储

**文件**: `internal/session/` (JSONL 格式)

**问题**: 会话历史以明文 JSONL 文件存储在 `~/.ggcode/sessions/`。如果用户在对话中输入了密码、API 密钥等敏感信息，这些信息将以明文保存在磁盘上。

**修复建议**: 提供 session 加密选项（如使用 age 或 GPG 加密）。

---

## 安全亮点（做得好的方面）

1. **多层命令安全防护**: `CommandGate` 实现了三层防护（pre-checks → block → ask），涵盖控制字符检测、Unicode 空白字符、 catastrophic 命令阻断等。参考了 Claude Code 的 `bashSecurity.ts` 设计。

2. **SSRF 防护完善**: `web_fetch.go` 实现了完整的 SSRF 防护，包括：
   - 私有 IP 段过滤（含 IPv6 映射地址 `::ffff:127.0.0.0/104`）
   - DNS 解析后 IP 验证（防止 DNS rebinding）
   - 重定向链中的私有地址检测
   - Cloud 元数据端点屏蔽 (`metadata.google.internal`)

3. **API 密钥安全管理**: 提供完整的明文密钥检测 → 迁移到 `keys.env`（0600权限） → 替换为 `${ENV_VAR}` 引用的工具链。

4. **A2A 多认证方案**: 支持 API Key（constant-time 比较）、OAuth2+PKCE、Device Flow、OIDC+JWKS、mTLS 五种认证方式，安全实现质量高。

5. **路径沙箱**: `PathSandbox` 正确解析符号链接防止沙箱逃逸，Bypass 模式对写入操作仍执行沙箱检查。

6. **原子文件写入**: 使用 `atomicWriteFile`（写临时文件+rename）防止写入中断导致文件损坏。

7. **敏感信息脱敏**: WebUI API 通过 `sanitizeMap` 脱敏 API key、env、headers 等字段。

8. **WebUI Token 认证**: 使用 `crypto/rand` 生成 32 字节随机 token，长度足够（64 hex chars）。

---

## 修复优先级建议

| 优先级 | 问题 | 预计工作量 |
|-------|------|-----------|
| P0 | H1: WebSocket CheckOrigin | 30 分钟 |
| P0 | H2: 常量时间 token 比较 | 15 分钟 |
| P1 | M3: keys.env 单引号转义 | 15 分钟 |
| P1 | M4: Autopilot 高危命令审计 | 2 小时 |
| P1 | M1: MCP SSRF 防护 | 3 小时 |
| P2 | M2: MCP 命令验证 | 4 小时 |
| P2 | M5: WebUI 地址绑定验证 | 1 小时 |
| P2 | M6: allow_unauthenticated 安全约束 | 1 小时 |
| P3 | L1-L5 | 按需排期 |
