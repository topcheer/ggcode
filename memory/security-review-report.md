# Auth/A2A/Subagent/Swarm/Tunnel 安全层全量评审报告

> 评审日期: 2026-05-21
> 评审范围: internal/auth, internal/a2a, internal/subagent, internal/swarm, internal/knight, internal/permission, internal/tunnel

---

## Critical (4 项)

### C1. JWT issuer/audience 验证失败时静默回退绕过校验
- **文件**: `internal/auth/a2a_oauth.go`, 行 442-453
- **问题**: `validateJWT` 在 `jwt.ParseWithClaims` 带 `WithIssuer`/`WithAudience` 校验失败时，回退到不带校验的 `jwt.ParseWithClaims`。注释说明原因是 "some providers use different issuer URLs"。这意味着攻击者可以用任意 issuer 签发的合法签名 JWT 通过验证。
```go
// Try parsing without strict issuer/audience validation
token, err = jwt.ParseWithClaims(tokenString, jwt.MapClaims{}, keyFunc)
```
- **修复建议**: 移除回退逻辑。如果 provider issuer 不匹配，应修正 `issuerURL` 配置而非跳过校验。若需支持 issuer 别名，应维护白名单列表显式匹配。

### C2. JWT 算法校验基于未验证 header 中的 `alg` 值，存在算法混淆攻击风险
- **文件**: `internal/auth/a2a_oauth.go`, 行 397-435
- **问题**: `validateJWT` 先用 `ParseUnverified` 获取 `alg`（行 410），然后在 `keyFunc` 中基于此预读的 `alg` 做 switch 分支。但 `jwt.ParseWithClaims` 内部解析 header 时会重新读取 `alg`，如果与预读不一致，会导致：
  - 攻击者构造 header 声明 `alg: RS256`（骗过 switch 进入 RSA 分支），但实际签名方法为 HS256
  - RSA 分支返回公钥，但 `jwt.ParseWithClaims` 用此公钥作为 HMAC 密钥验证 HS256 签名（经典算法混淆攻击）
- **修复建议**: 在 `keyFunc` 内使用 `token.Method` 做类型断言，而非预读的 `alg` 字符串：
```go
keyFunc := func(token *jwt.Token) (interface{}, error) {
    switch token.Method.(type) {
    case *jwt.SigningMethodRSA:
        return v.getPublicKey(ctx, kid)
    case *jwt.SigningMethodECDSA:
        return v.getPublicKey(ctx, kid)
    case *jwt.SigningMethodHMAC:
        if v.clientSecret == "" {
            return nil, fmt.Errorf("HMAC token but no client_secret configured")
        }
        return []byte(v.clientSecret), nil
    default:
        return nil, fmt.Errorf("unsupported signing method: %v", token.Header["alg"])
    }
}
```

### C3. HMAC 签名 JWT 使用公开的 `clientID` 作为密钥
- **文件**: `internal/auth/a2a_oauth.go`, 行 426-431
- **问题**: HS256/HS384/HS512 分支使用 `v.clientID`（即 OAuth2 `client_id`）作为 HMAC 密钥。Client ID 是公开信息（出现在授权 URL 和回调中），任何知道 client_id 的人都能伪造 JWT。
```go
return []byte(v.clientID), nil
```
- **修复建议**: 使用 `client_secret` 作为 HMAC 密钥，或完全禁用 HMAC 签名方式（OIDC 规范推荐使用 RS256/ES256 非对称签名）。

### C4. Tunnel relay token 在 WebSocket URL 查询参数中明文传输
- **文件**: `internal/tunnel/relay_client.go`, 行 60
- **问题**: 认证 token 直接拼接在 WebSocket URL 查询参数中：
```go
url := fmt.Sprintf("%s/ws?role=server&token=%s", rc.relayURL, rc.token)
```
Token 会暴露在：服务端访问日志、代理服务器日志、浏览器历史（如果涉及）。此外 token 未做 URL 编码，特殊字符可能破坏 URL 解析。
- **修复建议**: 将 token 放在 WebSocket 握手的 HTTP header 中（如 `Authorization: Bearer <token>`），或至少对 token 做 `url.QueryEscape()` 编码。

---

## High (7 项)

### H1. 全局 `http.DefaultClient` 用于所有 OAuth2/JWKS/Introspection 请求
- **文件**: `internal/auth/a2a_oauth.go`, 行 168, 237, 321, 510, 541, 655
- **问题**: 6 处使用 `http.DefaultClient.Do(req)`。DefaultClient 无超时、无 TLS 自定义、跟随重定向。如果 OIDC discovery URL 被劫持指向内网地址，可造成 SSRF。无超时意味着请求可能永远挂起。
- **修复建议**: 创建专用 `http.Client`，设置 30s 超时、禁止跨域重定向、可配置 TLS：
```go
var secureHTTPClient = &http.Client{
    Timeout: 30 * time.Second,
    CheckRedirect: func(req *http.Request, via []*http.Request) error {
        if len(via) >= 3 { return fmt.Errorf("too many redirects") }
        return nil
    },
}
```

### H2. OIDC Discovery 未验证返回的 issuer 与配置一致
- **文件**: `internal/auth/a2a_oauth.go`, 行 498-530
- **问题**: `refreshJWKS` 从 OIDC discovery URL 获取 `jwks_uri`，但不验证返回文档中的 `issuer` 字段是否与配置的 `issuerURL` 匹配。RFC 8414 明确要求 `issuer` 必须匹配 discovery URL。不验证允许伪造 JWKS 端点提供攻击者控制的公钥。
- **修复建议**: 解析 discovery 文档后校验 `discoveryDoc["issuer"] == v.issuerURL`，不一致则拒绝。

### H3. 沙箱路径遍历风险 — 缺少 EvalSymlinks 和边界校验
- **文件**: `internal/permission/sandbox.go`, 行 30-55
- **问题**: `IsAllowed` 对传入 path 做 `filepath.Clean` 后逐级向上检查前缀匹配，但：
  1. 未调用 `filepath.Abs()` 确保绝对路径
  2. 未调用 `filepath.EvalSymlinks()` 解析符号链接（符号链接可指向 allowedDir 外部）
  3. `strings.HasPrefix(path, dir+string(os.PathSeparator))` 的边界检查：如果 `allowedDir` 是 `/home/user`，路径 `/home/userX/secret` 也通过 `strings.HasPrefix(path, "/home/user/")`（因为添加了 PathSeparator，实际上这条是安全的），但如果 allowedDir 以 `/` 结尾可能有问题
- **修复建议**: 入口处先 `filepath.Abs(filepath.Clean(path))`，再 `filepath.EvalSymlinks()` 解析真实路径，对 allowedDir 同样做 EvalSymlinks，然后做前缀匹配。

### H4. A2A 任务 ID 可预测（时间戳 + 自增计数器）
- **文件**: `internal/a2a/handler.go`, 行 723-726
- **问题**: `generateID()` 使用纳秒时间戳 + 进程内原子计数器：
```go
func generateID() string {
    n := atomic.AddUint64(&taskSeq, 1)
    return fmt.Sprintf("%d-%d", time.Now().UnixNano(), n)
}
```
攻击者可预测 task ID，在 A2A 网格中枚举或伪造任务引用（例如在 `tasks/get`、`tasks/cancel` 请求中使用预测的 ID）。
- **修复建议**: 加入 `crypto/rand` 随机成分：`return fmt.Sprintf("%d-%d-%s", time.Now().UnixNano(), n, randomHex(8))`

### H5. JWT 过期检查在回退后可能被绕过
- **文件**: `internal/auth/a2a_oauth.go`, 行 460-467
- **问题**: 由于 C1 的回退逻辑，`jwt.ParseWithClaims` 不带 `WithExpiration` 选项被调用时，jwt 库不会自动检查 `exp` claim。虽然后续有手动检查（行 461-466），但这依赖 `exp` 存在且为 float64 类型。如果 `exp` 缺失或类型不对，过期检查被跳过。
- **修复建议**: 在 `jwt.ParseWithClaims` 中始终启用 `jwt.WithExpirationRequired()` 选项，或确保手动检查覆盖所有 edge case。

### H6. Token 缓存文件未校验磁盘权限
- **文件**: `internal/auth/a2a_token_cache.go`, 行 42-44
- **问题**: `loadFromDisk` 直接 `os.ReadFile`，不检查文件权限。写入时（`store.go` 行 139-155）使用 `os.OpenFile(name, ..., 0600)` 设置了 0600，但如果用户手动 chmod 或文件系统 umask 不同，token 文件可能对其他用户可读。
- **修复建议**: 读取后检查文件 mode，如果不是 0600 则警告或拒绝加载：
```go
info, _ := os.Stat(path)
if info.Mode().Perm() != 0600 {
    return nil, fmt.Errorf("token file %s has insecure permissions %o", path, info.Mode().Perm())
}
```

### H7. Claude OAuth callback 未使用 constant-time 比较验证 state
- **文件**: `internal/auth/claude_oauth.go`, 行 123
- **问题**: `state != expectedState` 使用普通字符串比较。虽然 state 是 32 字节随机值（高熵），timing attack 在此场景下实际利用难度极高，但安全最佳实践要求使用 constant-time 比较。
- **修复建议**: 使用 `subtle.ConstantTimeCompare([]byte(state), []byte(expectedState)) == 1`

---

## Medium (8 项)

### M1. A2A 认证无重放保护
- **文件**: `internal/a2a/server.go`, 行 229-272
- **问题**: Bearer token 验证成功后不记录 jti (JWT ID) 或时间窗口。截获的 token 在过期前可无限重放。API Key 认证更是完全无状态，无任何重放防护。
- **修复建议**: 对 JWT 验证增加 `jti` 去重（短期缓存，如 5 分钟）；对 API Key 认证增加请求签名时间戳校验。

### M2. JWKS 缓存过期时间硬编码，未使用 HTTP Cache-Control header
- **文件**: `internal/auth/a2a_oauth.go`, 行 549
- **问题**: JWKS key 缓存过期时间硬编码（约 1 小时），不考虑 HTTP `Cache-Control: max-age` 或 `Expires` header。如果 key 轮换比缓存时间快，会接受已撤销的 key。
- **修复建议**: 解析 JWKS 响应的 `Cache-Control: max-age` header 动态设置缓存时间。

### M3. Swarm teammate 数量无硬上限保护
- **文件**: `internal/swarm/manager.go`
- **问题**: `MaxTeammatesPerTeam` 来自配置文件，没有代码级硬上限。如果配置误设为极大值（如 10000），可以创建大量 teammate 导致 OOM。当前默认值是合理的（3-10），但没有防护机制。
- **修复建议**: 在 `SpawnTeammate` 中增加硬上限检查（如 `hardLimit = 50`），超过拒绝。

### M4. Permission mode 切换存在 TOCTOU 竞态
- **文件**: `internal/permission/config_policy.go`
- **问题**: `ConfigPolicy` 的 `Check` 方法（读）和 `SetOverride`/mode 切换（写）使用 `sync.RWMutex`，但工具权限检查和工具执行之间存在时间窗口。例如 agent 在 `auto` mode 下检查某工具允许，但在执行前 mode 被切换为 `supervised`，工具仍会执行。
- **修复建议**: 将权限 decision 做快照式读取并在工具执行期间锁定，或使用 Copy-on-Write 模式确保执行期间 decision 不变。

### M5. Knight budget CanSpend/Record 非原子
- **文件**: `internal/knight/budget.go`, 行 58-127
- **问题**: `CanSpend()` 和 `Record()` 是两个独立方法。虽然各自持有 mutex，但多个并发 knight task 可能在 `CanSpend()` 返回 true 后、`Record()` 之前都通过检查，导致实际使用超过预算。
```go
// Thread A: CanSpend() → true (remaining: 1000)
// Thread B: CanSpend() → true (remaining: 1000)
// Thread A: Record(800) → used: 800
// Thread B: Record(800) → used: 1600 (exceeds budget)
```
- **修复建议**: 将 `CanSpend` + `Record` 合并为原子操作 `TrySpend(task string, estimatedTokens int) bool`，在同一把锁内完成检查和预扣。

### M6. Tunnel session token 仅 128 bit 且无过期机制
- **文件**: `internal/tunnel/session.go`, 行 28
- **问题**: `generateToken()` 使用 `crypto/rand` 生成 16 字节（128 bit）。对于长期会话 token 来说熵值偏低（建议 256 bit）。且 session 无过期机制，token 一旦泄露可永久使用。
- **修复建议**: 增加到 32 字节（256 bit），并增加 session 过期时间（如 24 小时后自动断开重连）。

### M7. Sub-agent 与父 agent 共享 provider 实例
- **文件**: `internal/subagent/runner.go`, 行 98
- **问题**: `cfg.AgentFactory(cfg.Provider, ...)` 中 `cfg.Provider` 是父 agent 的 provider 引用。如果 provider 内部有可变状态（如速率限制器、token 计数器），子 agent 的请求会影响父 agent 的速率配额。虽然这不是直接的安全漏洞，但违反了隔离原则。
- **修复建议**: 为子 agent 创建独立的 provider 实例（或至少独立的 rate limiter/token counter）。

### M8. A2A skill 中 `SkillFullTask` 允许所有工具且无迭代限制
- **文件**: `internal/a2a/handler.go`, 行 608
- **问题**: `SkillFullTask` 配置为 `{AllowedTools: nil, ReadOnly: false, MaxIterations: 0}`，即允许所有工具、无迭代限制。远程 A2A 客户端可通过 `full-task` skill 执行任意工具调用（包括 `run_command`），无额外限制。
- **修复建议**: 为 `full-task` 设置合理的 `MaxIterations`（如 20），或至少增加 `DangerousTool` 二次确认机制。

---

## Low (5 项)

### L1. Debug 日志泄漏 OAuth 授权 URL 参数
- **文件**: `internal/auth/a2a_oauth.go`, 行 126
- **问题**: `debug.Log("a2a.oauth", "auth URL: %s", authURL.String())` 将包含 `state` 和 `code_challenge` 的完整授权 URL 写入日志。虽然这些是单次有效的，但 debug 日志可能被持久化。
- **修复建议**: 仅日志 base URL，不包含 query 参数中的敏感值。

### L2. A2A 客户端 TLS 未显式配置最低版本
- **文件**: `internal/a2a/client.go`
- **问题**: 客户端 HTTP 请求未显式配置 TLS 最低版本。Go 默认使用 TLS 1.2+（从 Go 1.18 起），所以实际风险很低，但显式配置是最佳实践。
- **修复建议**: 确保所有 A2A client 的 HTTP transport 配置 `tls.Config{MinVersion: tls.VersionTLS12}`。

### L3. Broker 消息序列号未包含在 AEAD AAD 中
- **文件**: `internal/tunnel/broker.go`
- **问题**: 消息有递增的 `Seq` 字段用于排序，但 `Seq` 未包含在 AES-GCM 的 associated data (AAD) 中。理论上如果 nonce 重用（不应发生），攻击者可重排消息而不被检测到。
- **修复建议**: 将 `Seq` 作为 AAD 传入 `aead.Seal`/`aead.Open`，防止重排。

### L4. Debug 日志可能打印 token 片段
- **文件**: `internal/auth/a2a_oauth.go`, 多处
- **问题**: 多个 `debug.Log` 调用打印 token 响应相关信息，可能在 debug 模式下泄漏 access_token 片段。
- **修复建议**: 对 debug log 中的 token 值做脱敏处理（仅打印前 4 字符 + "..."）。

### L5. JWKS 缓存 TTL 硬编码为固定值
- **文件**: `internal/auth/a2a_oauth.go`, 约 549
- **问题**: JWKS 缓存 TTL 硬编码，不可根据 key rotation 策略调整。
- **修复建议**: 从 JWKS endpoint 的 `Cache-Control` header 动态获取。

---

## 总体评价

| 模块 | 安全等级 | 主要风险 |
|------|---------|---------|
| auth (OAuth2 PKCE) | **Good** | PKCE 实现正确（`crypto/rand` 32 字节 + S256），state 验证到位 |
| auth (JWT) | **Critical** | 算法混淆风险(C2)、issuer 回退绕过(C1)、HMAC 用 clientID(C3) |
| A2A Server Auth | **Good** | constant-time API key 比较、localhost 默认绑定、mTLS 支持 |
| A2A Protocol | **Medium** | 无重放保护、可预测 task ID、full-task skill 无限制 |
| Tunnel Crypto | **Good** | AES-256-GCM + `crypto/rand` nonce，实现正确 |
| Tunnel Relay | **High** | Token 在 URL 参数中泄漏(C4) |
| Permission | **Medium** | 沙箱路径需 EvalSymlinks(H3)，mode 切换有 TOCTOU(M4) |
| Subagent | **Good** | 信号量并发限制、超时和取消完整、panic recovery |
| Swarm | **Good** | teammate 有配置上限但缺硬上限(M3) |
| Knight | **Good** | 预算桶机制完善(budget_buckets.go)，但 CanSpend/Record 非原子(M5) |

### 优先修复排序
1. **C2** — JWT 算法混淆（可导致任意 token 伪造）
2. **C1** — issuer 回退绕过（可导致跨域 token 接受）
3. **C4** — Tunnel token URL 泄漏
4. **C3** — HMAC 用 clientID
5. **H1** — http.DefaultClient SSRF/无超时
6. **H2** — OIDC Discovery issuer 不验证
7. **H3** — 沙箱 EvalSymlinks

### 积极发现（安全实践良好）
- **PKCE 实现**: 32 字节 `crypto/rand` + S256 code challenge = 完全符合 RFC 7636
- **State 参数**: 32 字节 `crypto/rand`，callback 中正确验证
- **API Key 比较**: 使用 `crypto/subtle.ConstantTimeCompare`，防 timing attack
- **A2A localhost 默认**: 无认证时默认绑定 127.0.0.1，防止 LAN 未授权访问
- **Tunnel AES-GCM**: 使用 `crypto/cipher.NewGCM` + `crypto/rand` nonce，加密实现正确
- **Token 文件权限**: 写入时使用 0600 权限
- **Token 缓存隔离**: 使用 `{provider}-{clientID[:12]}` 文件名，不同 client 不会互相覆盖
- **Subagent 取消**: context 传播 + cancel 函数注册，取消链路完整
- **Swarm 取消**: `CancelAll()` 跨所有 team 遍历取消，中断信号传播完整
- **Knight 预算**: 6 个独立桶 (analysis/eval/maintenance/proposal/skill_tuning/adhoc) 防止单类任务占满预算
