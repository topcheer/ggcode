# ggcode 安全审查报告

**审查员:** security-reviewer  
**日期:** 2025-01-27  
**项目:** ggcode — 终端 AI 编码助手  
**审查范围:** OAuth2/OIDC 认证、凭证管理、命令注入、权限模型、网络安全、输入验证、敏感数据泄露  

---

## 总体评估

ggcode 项目整体安全设计**优秀**，体现了多层防御（Defense-in-Depth）的安全理念。命令执行有三层安全门控，文件操作有沙箱隔离，网络请求有 SSRF 防护，认证体系支持多种行业标准。以下发现按严重性排序，大部分为中低风险。

---

## 🔴 Critical — 严重 (0 项)

无 Critical 级别的安全发现。

---

## 🟡 Warning — 中等风险 (5 项)

### W-01: JWT 验证回退跳过 Issuer/Audience 检查
**文件:** `internal/auth/a2a_oauth.go` 约第 438-453 行  
**类别:** 认证绕过

`validateJWT` 方法首先使用严格的 `jwt.WithIssuer` 和 `jwt.WithAudience` 选项解析 JWT。若解析失败（非过期错误），代码**回退到不验证这些字段**的宽松模式：

```go
// 尝试不严格验证 issuer/audience
token, err = jwt.ParseWithClaims(tokenString, jwt.MapClaims{}, keyFunc)
```

**影响:** 攻击者如果能从使用相同签名密钥的不同 OAuth 提供商获取有效 JWT，可绕过 issuer/audience 验证实现认证。  
**修复建议:** 移除回退逻辑。如果 issuer/audience 不匹配，直接拒绝 token。对于使用非标准 issuer URL 的提供商，应在配置层面解决。

---

### W-02: WebUI 无任何认证
**文件:** `internal/webui/server.go`  
**类别:** 缺少访问控制

WebUI 服务器绑定在 `127.0.0.1:0`（随机端口），但**所有 API 端点均无认证**。任何本地进程可以：
- 读取完整配置（含供应商/端点结构）
- 通过 `PUT /api/vendors/{vendor}/endpoints/{endpoint}/apikey` 设置/修改 API Key
- 通过 WebSocket 以用户身份发送聊天消息
- 通过 `POST /api/restart` 重启 Agent
- 修改 IM 适配器设置

**影响:** 在共享/多用户系统或启用了端口转发的容器环境中可被利用。  
**修复建议:** 添加启动时生成的认证 Token（显示一次，或通过环境变量配置），至少在文档中标注此限制。

---

### W-03: WebSocket 允许任意 Origin（CSWSH 风险）
**文件:** `internal/webui/server.go` 第 1235-1237 行  
**类别:** 跨站 WebSocket 劫持

```go
var upgrader = websocket.Upgrader{
    CheckOrigin: func(r *http.Request) bool { return true },
}
```

**影响:** 任意源的恶意网页可建立 WebSocket 连接。配合 W-02（无认证），如果端口已知/可猜测，恶意网页可发送命令。默认绑定 localhost+随机端口降低了风险，但在反向代理或端口转发场景下风险升高。  
**修复建议:** 限制 `CheckOrigin` 仅接受 `localhost`/`127.0.0.1` 来源。

---

### W-04: A2A Push Notification URL 存在 SSRF 风险
**文件:** `internal/a2a/server.go` 第 755-793 行  
**类别:** 服务端请求伪造 (SSRF)

Push notification 配置通过 JSON-RPC 存储，URL 由用户提供。`firePushNotifications` 使用 `http.DefaultClient`（无超时、无私有网络过滤）向这些 URL 发送 HTTP POST：

```go
resp, err := http.DefaultClient.Do(req)
```

**影响:** 已认证的 A2A 客户端可设置指向内部服务的推送 URL（如 `http://169.254.169.254/latest/meta-data/` 云元数据端点，或 `http://localhost:PORT/internal-endpoint`）。  
**修复建议:** 对推送通知 URL 应用与 `web_fetch.go` 相同的 SSRF 防护，并添加合理超时。

---

### W-05: HMAC 签名使用 clientID 而非 clientSecret
**文件:** `internal/auth/a2a_oauth.go` 约第 427-430 行  
**类别:** 密码学误用

```go
case "HS256", "HS384", "HS512":
    // HMAC — client_secret is the key (for opaque token emulation)
    if v.clientID == "" {
        return nil, fmt.Errorf("HMAC token but no client_id configured")
    }
    return []byte(v.clientID), nil
```

注释说 "client_secret is the key"，但实际使用的是 `clientID`（公开标识符）。  
**影响:** 如果 OAuth 提供商签发 HS256 JWT，任何知道公开 `client_id` 的人都可以伪造 token。这是 JWT 实现中的已知反模式。  
**修复建议:** 要求为 HMAC 验证配置 `client_secret`，或完全拒绝 HS256 而仅允许非对称算法（RS256/ES256）。

---

## 🟢 Info — 低风险 (7 项)

### I-01: Auth Store 目录权限过于宽松
**文件:** `internal/auth/store.go` 第 146 行  
**详情:** `~/.ggcode/` 目录以 `0o755` 创建（全局可读/可遍历）。文件本身正确使用 `0o600`，但在多用户系统上其他用户可发现文件存在。  
**建议:** 使用 `0o700` 创建 `~/.ggcode/` 目录。

---

### I-02: sanitizeConfigForAPI 暴露完整 Vendor/MCP 结构
**文件:** `internal/webui/server.go` 第 1768-1788 行  
**详情:** `sanitizeConfigForAPI` 直接返回 `cfg.MCPServers` 和 `cfg.Vendors` 完整结构体。MCP server 的 `env` 块可能包含 `${VAR}` 引用（暴露环境变量名），endpoint 的 `base_url` 等内部信息也会泄露。`handleVendorDetail` 和 `handleEndpointDetail` 正确地用 `has_api_key` 布尔值替代了实际 key，但配置汇总端点过于全面。  
**建议:** 对配置 API 响应中的敏感字段应用递归遮蔽。

---

### I-03: Shell 命令依赖正则门控而非形式化转义
**文件:** `internal/tool/run_command.go` 第 232 行  
**详情:** 命令通过 `util.NewShellCommandContext` 直接传递给 `bash -c`。虽然 `CommandGate` 提供了多层正则过滤，但不执行形式化的 shell 转义。复杂的 shell 解析边界情况可能绕过正则规则。  
**建议:** 现有多层防护已经很强。建议添加对抗性命令模式的模糊测试套件以持续验证。

---

### I-04: Device Code 记录到 Debug 日志
**文件:** `internal/auth/a2a_oauth.go` 第 267 行  
**详情:** `debug.Log("a2a.oauth", "device code: %s", deviceResp.UserCode)` 将用户码写入 debug 日志。虽然 User Code 是短暂的（通常 15 分钟有效），且设计上是需要用户手动输入的，但如果 debug 日志被共享或持久化，可能泄露给未授权方。  
**建议:** 考虑在 debug 日志中仅记录 User Code 的前 4 个字符。

---

### I-05: AllowPrivate 标志绕过所有 SSRF 防护
**文件:** `internal/tool/web_fetch.go` 第 29 行  
**详情:** `AllowPrivate bool` 字段完全禁用 SSRF 防护。目前仅测试代码设置为 `true`，但如果意外在生产代码路径中启用，所有 SSRF 保护将被绕过。  
**建议:** 将此字段设为非导出（unexported），或通过编译标签（build tag）启用。

---

### I-06: Copilot Device Flow 缺少 client_secret
**文件:** `internal/auth/copilot.go` 第 151-154 行  
**详情:** Copilot Device Flow 的 token 请求仅发送 `client_id` 和 `device_code`，不包含 `client_secret`。根据 AGENTS.md 文档，GitHub OAuth Apps 是 confidential clients，PKCE 需要 client_secret 用于 token exchange。不过 Device Flow（RFC 8628）本身不需要 client_secret，这可能是正确的设计选择。  
**建议:** 在代码注释中明确说明 Device Flow 不需要 client_secret 的设计决策。

---

### I-07: 钉钉 app_secret 通过配置文件明文存储
**文件:** `internal/im/dingtalk_adapter.go` 第 110-112 行  
**详情:** IM 适配器（钉钉、Slack、Discord、Telegram 等）的 token/secret 直接从配置文件的 `extra` 字段读取并以字符串形式保存在内存中。虽然支持 `${ENV_VAR}` 环境变量替换，但文档和错误提示未强制推荐使用环境变量。  
**建议:** 在配置文档中推荐 IM 适配器凭证使用 `${ENV_VAR}` 语法。

---

## ✅ Good — 优秀实践 (12 项)

### G-01: PKCE 实现安全
**文件:** `internal/auth/claude_oauth.go`, `internal/auth/pkce.go`  
使用 `crypto/rand` 生成密码学安全的 code verifier/challenge，正确实现 S256。OAuth 回调有 state 参数验证。

### G-02: 三层命令门控
**文件:** `internal/tool/command_gate.go`  
Block/Ask/Allow 三层模型，覆盖灾难性命令、注入模式、提权操作、破坏性操作。预检查捕获控制字符和 Unicode 空白字符绕过尝试。半角分号分割后逐段检查灾难性命令。

### G-03: 全面的 SSRF 防护
**文件:** `internal/tool/web_fetch.go`  
- 主机名解析到私有 IP 检查
- 自定义 `DialContext` 防止 DNS rebinding 攻击
- 重定向链私有网络过滤
- 最大重定向次数限制（10次）
- 响应体大小限制（10MB）
- 失败时默认拒绝（fail-closed）

### G-04: 符号链接感知的路径沙箱
**文件:** `internal/permission/sandbox.go`  
`resolvePath` 函数递归解析符号链接防止沙箱逃逸，对不存在的路径采用最长存在前缀解析策略。

### G-05: Token 持久化安全
**文件:** `internal/auth/a2a_token_cache.go`  
- OAuth token 文件以 `0600` 权限写入
- 缓存目录以 `0700` 权限创建
- 每 `{provider}-{clientID[:12]}` 独立缓存防止跨实例覆盖
- Auth Store 使用原子写入（tmp + rename）

### G-06: A2A 认证设计完善
**文件:** `internal/a2a/server.go` 第 228-271 行  
- API Key 使用 `subtle.ConstantTimeCompare` 防止时序攻击
- Bearer Token 通过 JWKS 验证
- mTLS 支持在 TLS 握手层验证
- 无认证时默认仅允许 localhost
- 4 MiB 请求体限制防止 OOM

### G-07: 会话文件权限正确
**文件:** `internal/session/store.go`  
- 会话目录 `0700`，索引文件 `0600`
- JSONL 追加写入使用 `O_APPEND` + `0600`
- 随机 ID 使用 `crypto/rand` 生成

### G-08: 敏感路径保护
**文件:** `internal/permission/config_policy.go` 第 231-252 行  
Bypass/Autopilot 模式下对 `~/.aws/credentials`、`~/.docker/config.json`、`/etc/**` 等敏感路径的写入操作降级为 Ask，防止提示注入攻击覆盖用户凭证。

### G-09: 原子文件写入
**文件:** `internal/util/atomic_write.go`  
通过 temp file + fsync + rename 模式避免崩溃导致文件截断。保留已有文件权限。

### G-10: 权限模式设计合理
**文件:** `internal/permission/mode.go`, `config_policy.go`  
五种权限模式递进：`supervised → plan → auto → bypass → autopilot`。Plan 模式严格只读。Auto 模式拒绝危险操作。Bypass/Autopilot 仍保护工作区边界。

### G-11: API Key 明文检测
**文件:** `internal/config/api_keys.go`  
`DetectPlaintextAPIKeys` 扫描配置文件中的明文秘密，推荐使用环境变量替换。

### G-12: MCP 进程隔离
**文件:** `internal/mcp/client.go`  
MCP 服务器进程使用 `setsid` 创建新会话组，隔离 TTY 访问。环境变量通过配置注入而非命令行参数。

---

## 发现汇总表

| ID | 级别 | 类别 | 组件 | 发现 |
|----|------|------|------|------|
| W-01 | 🟡 Warning | 认证绕过 | auth/a2a_oauth | JWT 验证回退跳过 issuer/audience |
| W-02 | 🟡 Warning | 缺少认证 | webui | WebUI 所有端点无认证 |
| W-03 | 🟡 Warning | CSWSH | webui | WebSocket 允许任意 Origin |
| W-04 | 🟡 Warning | SSRF | a2a/server | Push notification URL 未过滤私有网络 |
| W-05 | 🟡 Warning | 密码学 | auth/a2a_oauth | HMAC 使用公开 clientID 作签名密钥 |
| I-01 | 🟢 Info | 凭证暴露 | auth/store | 目录权限 0755 |
| I-02 | 🟢 Info | 信息泄露 | webui | 配置 API 暴露完整 vendor/MCP 结构 |
| I-03 | 🟢 Info | 防御深度 | tool/run_command | Shell 命令依赖正则而非形式化转义 |
| I-04 | 🟢 Info | 信息暴露 | auth | Debug 日志记录 Device Code |
| I-05 | 🟢 Info | 设计 | tool/web_fetch | AllowPrivate 标志绕过 SSRF 防护 |
| I-06 | 🟢 Info | 设计 | auth/copilot | Device Flow 无 client_secret（可能正确） |
| I-07 | 🟢 Info | 凭证管理 | im/adapters | IM 凭证支持但未推荐环境变量 |

---

## 修复优先级建议

### 高优先级
1. **W-01:** 移除 JWT issuer/audience 验证回退
2. **W-02:** 为 WebUI 添加可选认证 Token
3. **W-03:** 限制 WebSocket CheckOrigin 为 localhost

### 中优先级
4. **W-04:** 对 push notification URL 添加 SSRF 防护和超时
5. **W-05:** 修复 HMAC key 使用 clientSecret 而非 clientID
6. **I-02:** 对配置 API 响应应用递归敏感字段遮蔽

### 低优先级
7. **I-01:** 使用 0700 权限创建 `~/.ggcode/` 目录
8. **I-03:** 添加命令模式模糊测试套件
9. **I-04:** 在 debug 日志中截断 User Code
10. **I-05:** 将 AllowPrivate 设为非导出字段
11. **I-07:** 文档推荐 IM 凭证使用环境变量
