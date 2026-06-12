# 工具层全量代码评审报告

## 评审范围
- `internal/tool/` — 内置工具集（命令执行、LSP、文件编辑、搜索等）
- `internal/plugin/` — 插件加载系统（Go插件、命令插件、MCP插件）
- `internal/permission/` — 权限模型与沙箱
- `internal/hooks/` — 钩子执行系统
- `internal/mcp/` — MCP客户端、OAuth 2.1认证、协议适配器

## 模块概要

### internal/tool/
提供LLM可调用的全部内置工具。核心架构包括：
- **Tool接口**（`tool.go`）：统一的 `Name/Description/Parameters/Execute` 接口
- **Registry**（`tool.go`）：线程安全的工具注册表，支持 Clone 用于子代理隔离
- **CommandGate**（`command_gate.go`）：三层命令安全门控（Allow/Ask/Block）
- **RunCommand**（`run_command.go`）：带自动后台化的命令执行器
- **LSP工具集**（`lsp.go`，895行）：完整的 LSP 客户端工具封装
- **EditFile/MultiEditFile**（`edit_file.go`, `edit_match.go`）：基于匹配的文件编辑

### internal/mcp/
完整的 MCP (Model Context Protocol) 客户端实现：
- **Client**（`client.go`，904行）：支持 stdio/HTTP/WebSocket 三种传输
- **OAuthHandler**（`oauth.go`，914行）：OAuth 2.1 + PKCE + Device Flow
- **Adapter**（`adapter.go`）：将 MCP 工具桥接为 `tool.Tool` 接口

### internal/permission/
五级权限模式（supervised → plan → auto → bypass → autopilot），包含：
- **PathSandbox**（`sandbox.go`）：基于目录的文件访问控制
- **DangerousDetector**（`dangerous.go`）：危险命令检测
- **PermissionPolicy**（`policy.go`）：权限策略接口

### internal/plugin/
插件管理器，支持三种加载方式：
- Go `.so` 共享库插件
- 外部命令包装为工具
- MCP 服务器工具桥接

### internal/hooks/
Pre/Post 工具执行钩子系统，支持模式匹配和输出注入。

---

## 发现的问题

### Critical（严重）

#### C1. 插件命令执行 — 命令注入风险
**文件**: `internal/plugin/plugin.go:113-114`
```go
if params.Args != "" {
    args = append(args, strings.Fields(params.Args)...)
}
```
**问题**: `strings.Fields(params.Args)` 会按空白拆分用户输入，但不会处理 shell 引号、转义字符或特殊字符。如果 `params.Args` 包含 `-e "some; rm -rf /"` 等 payload，整个参数会直接传递给 `exec.CommandContext`，虽然不是 shell 注入（因为是参数数组），但 `strings.Fields` 的拆分行为与用户预期不符（不会处理引号）。
**建议**: 使用专门的 shell 参数解析库（如 `kballard/go-shellquote`），或至少在文档中明确说明参数传递方式。

#### C2. Go .so 插件加载无安全校验
**文件**: `internal/plugin/loader.go:63`
```go
plug, err := plugin.Open(p)
```
**问题**: 直接通过 `plugin.Open()` 加载 `.so` 文件，没有：
1. 对 `.so` 文件路径做沙箱校验（可以是任意绝对路径）
2. 没有数字签名校验
3. 没有哈希完整性校验
4. 加载后的 `New()` 函数直接执行，无沙箱隔离
**影响**: 恶意 `.so` 文件可以在 ggcode 进程内执行任意代码（与宿主进程同权限）。
**建议**: 
- 限制只从 `~/.ggcode/plugins/` 目录加载 `.so` 文件
- 添加可选的签名验证机制
- 至少添加路径校验，拒绝从系统目录加载

#### C3. Hooks RAW_INPUT 通过环境变量传递可能泄露敏感信息
**文件**: `internal/hooks/runner.go:80`
```go
c.Env = append(os.Environ(), "GGCODE_RAW_INPUT="+env.RawInput)
```
**问题**: 将完整的工具输入 JSON（可能包含 API key、密码等敏感字段）通过环境变量传递。环境变量在 `/proc/<pid>/environ` 中可见（Linux），也可通过 `ps eww` 查看。
**影响**: 敏感数据泄露给具有本地访问权限的其他用户或进程。
**建议**: 使用临时文件或管道传递敏感数据，避免通过环境变量。

---

### Major（重要）

#### M1. MCP Client.mu 全局锁限制并发
**文件**: `internal/mcp/client.go:299-301`
```go
func (c *Client) sendRequest(ctx context.Context, method string, params interface{}, result interface{}) error {
    c.mu.Lock()
    defer c.mu.Unlock()
```
**问题**: 所有 MCP 请求共享同一个互斥锁。如果一次请求超时（30分钟默认），其他所有请求都会被阻塞。WebSocket 读取循环也会被阻塞。
**建议**: 考虑使用带超时的锁获取（`TryLock` 或 `context` 感知的锁），或采用请求队列模型避免全局阻塞。

#### M2. OAuth token 交换日志泄露敏感信息
**文件**: `internal/mcp/oauth.go:623`
```go
debug.Log("mcp-oauth", "exchange_response server=%s status=%d body=%s", h.serverName, resp.StatusCode, string(body))
```
**问题**: token endpoint 的完整响应体被写入 debug 日志，其中包含 `access_token`、`refresh_token` 等敏感信息。
**影响**: 如果 debug 日志被持久化或转发，可能导致凭据泄露。
**建议**: 移除 body 的完整日志，或仅记录 token 的前几个字符 + 长度。

#### M3. PathSandbox 不处理 TOCTOU 竞态
**文件**: `internal/permission/sandbox.go:79-95`
```go
func (s *PathSandbox) Allowed(path string) bool {
    resolved := resolvePath(path)
    // ...
    for _, dir := range s.allowedDirs {
        if strings.HasPrefix(resolved, dir+string(os.PathSeparator)) || resolved == dir {
            return true
        }
    }
```
**问题**: `Allowed()` 在检查时解析路径，但实际操作发生在稍后。攻击者可以在检查和操作之间创建符号链接（TOCTOU 竞态条件），绕过沙箱。
**影响**: 在多线程/并发环境中，agent 可能在路径检查后被重定向到沙箱外的文件。
**建议**: 在工具实际打开文件时使用 `os.OpenFile` + `fd` 操作，在打开后通过 `/proc/self/fd/<n>` 验证实际路径。

#### M4. MCP Manager 双重闭包变量冗余
**文件**: `internal/plugin/mcp_loader.go:410-416`
```go
for _, plugin := range m.plugins {
    plugin := plugin          // first closure capture
    if MCPDisabled(plugin.Name()) {
        continue
    }
    pluginCopy := plugin      // second copy — redundant
    safego.Go("plugin.mcp.connectWithRetry", func() { m.connectWithRetry(ctx, pluginCopy) })
}
```
**问题**: `plugin := plugin` 已经创建了闭包捕获的副本，`pluginCopy` 是多余的。虽然功能无害，但说明代码意图不清晰。
**建议**: 统一使用一种变量捕获模式（推荐 `plugin := plugin` 即可）。

#### M5. CommandGate 正则绕过风险 — 编码/转义
**文件**: `internal/tool/command_gate.go`
**问题**: CommandGate 依赖正则表达式匹配命令字符串。以下场景可能绕过：
1. **十六进制/八进制编码**: `$'\x72\x6d'` 等价于 `rm`，但正则不匹配
2. **变量间接引用**: `cmd=rm; $cmd -rf /` 不会被匹配
3. **Base64 解码执行**: `echo cm0gLXJmIC8= | base64 -d | sh`
4. **换行符分割**: 部分正则不处理 `\n` 分割的多行命令
**建议**: 添加对十六进制/八进制编码、变量间接引用、base64 解码模式的检测规则。文档明确说明门控的安全边界——它是防御层之一，不能替代操作系统级安全。

#### M6. OAuth Device Flow 无最大重试次数限制
**文件**: `internal/mcp/oauth.go:798-853`
```go
for {
    select {
    case <-ctx.Done():
        return nil, ctx.Err()
    case <-ticker.C:
        // ... poll indefinitely until ctx cancel
```
**问题**: `PollDeviceToken` 无限循环轮询，仅依赖 context 取消。如果 context 没有超时，可能永远轮询。
**建议**: 添加基于 `expires_in` 的硬性超时上限（设备码本身有过期时间）。

#### M7. MCP Client.Close() 进程 kill 竞态
**文件**: `internal/mcp/client.go:240-273`
```go
func (c *Client) Close() error {
    c.Abort()           // kills process
    c.mu.Lock()
    // ...
    if (transport == "stdio" || transport == "") && cmd != nil {
        done := make(chan struct{})
        safego.Go("mcp.client.waitProcess", func() {
            _ = cmd.Wait()    // wait on already-killed process
            close(done)
        })
```
**问题**: `Abort()` 已经 kill 了进程，然后 `Close()` 又调用 `cmd.Wait()`。在 kill 和 wait 之间存在微小竞态，但更关键的是 3 秒超时后直接返回，可能泄漏僵尸进程。
**建议**: 确保 `Abort` 在 `Close` 的锁保护下执行，或记录 wait 超时警告。

---

### Minor（次要）

#### m1. edit_file.go 白空格可视化仅处理 Tab
**文件**: `internal/tool/edit_file.go:349-351`
```go
func visualizeWhitespace(line string) string {
    return strings.ReplaceAll(line, "\t", "<TAB>")
}
```
**问题**: 只将 tab 替换为 `<TAB>`，不处理行尾空格、不间断空格等。
**建议**: 使用更完整的可视化，如标记行尾空格为 `<TRAILING-SPACE>`。

#### m2. CommandGate destructiveWarnings 重复编译正则
**文件**: `internal/tool/command_gate.go:314-332`
```go
destructivePatterns := []struct {
    pattern *regexp.Regexp
    warning string
}{
    {regexp.MustCompile(`...`), "..."},
    // 每次 Check 调用都重新编译
```
**问题**: `destructiveWarnings()` 每次调用都重新编译所有正则表达式。
**建议**: 将 `destructivePatterns` 移为 `CommandGate` 结构体的字段，与 `blockRules`/`askRules` 一同初始化。

#### m3. hooks ExtractFilePath 脆弱的 JSON 解析
**文件**: `internal/hooks/runner.go:111-133`
```go
func ExtractFilePath(toolName string, rawInput string) string {
    for _, key := range []string{"file_path", "path", "filename", "file"} {
        idx := strings.Index(rawInput, `"`+key+`"`)
```
**问题**: 使用字符串搜索而非 JSON 解析来提取文件路径。对于嵌套 JSON 或格式化 JSON 可能误匹配或提取错误值。
**建议**: 使用 `json.Unmarshal` 后按字段名提取。

#### m4. MCP Plugin.Tools() 总是返回 nil
**文件**: `internal/plugin/mcp_loader.go:269-271`
```go
func (m *MCPPlugin) Tools() []tool.Tool {
    return nil
}
```
**问题**: `MCPPlugin` 实现了 `Plugin` 接口但 `Tools()` 始终返回 nil。这实现了 `Plugin` 接口但语义上不准确。
**建议**: 要么让 `Tools()` 返回实际的 MCP 工具列表，要么让 `MCPPlugin` 不实现 `Plugin` 接口（它有自己的 `Connect` 模式）。

#### m5. Hooks matchTool 的 glob 匹配错误被静默忽略
**文件**: `internal/hooks/runner.go:156`
```go
matched, _ := filepath.Match(pattern, toolName)
```
**问题**: `filepath.Match` 在模式无效时返回错误，但被忽略。非法模式会静默不匹配。
**建议**: 在配置加载时验证 glob 模式有效性，或至少在运行时记录无效模式。

#### m6. MCP Client readHeaderFramedMessage 无 Content-Length 上限
**文件**: `internal/mcp/client.go:625-657`
```go
contentLength := -1
// ...
body := make([]byte, contentLength)
if _, err := io.ReadFull(reader, body); err != nil {
```
**问题**: 如果 `Content-Length` 头被恶意设置为极大值（如 2GB），会直接分配相应大小的内存。
**建议**: 添加 `contentLength` 上限检查（如 100MB），超过时返回错误。

#### m7. PermissionMode 循环顺序不可配置
**文件**: `internal/permission/mode.go:75-88`
**问题**: `Next()` 方法硬编码了 `supervised → plan → auto → bypass → autopilot → supervised` 的循环。这是设计选择但可能不符合所有用户工作流。
**建议**: 考虑让循环顺序可配置，或至少在文档中清晰说明这个行为。

#### m8. Sandbox resolvePath 对不存在路径的 fallback
**文件**: `internal/permission/sandbox.go:37-75`
```go
func resolvePath(path string) string {
    // ...
    return abs // fallback to original
```
**问题**: 当路径完全不存在且无法解析时，fallback 到原始绝对路径。这可能导致 `Allowed()` 对一个尚不存在的路径做出错误判断。
**影响**: 在 `write_file` 创建新文件时，沙箱可能允许在预期目录外创建文件（如果新文件的父目录前缀恰好匹配）。
**建议**: 对于写操作，验证父目录存在且在沙箱内。

---

### Suggestion（建议）

#### S1. 工具参数描述应包含更多安全提示
**文件**: `internal/tool/run_command.go:147`
```go
"command": {
    "type": "string",
    "description": "Shell command to execute..."
}
```
**建议**: 在 LLM 面向的工具描述中明确说明命令执行的限制和安全策略，帮助 LLM 理解边界。

#### S2. MCP OAuth 错误消息可加入用户指导
**文件**: `internal/mcp/oauth.go:497`
```go
return "", fmt.Errorf("no OAuth client_id: server does not support dynamic client registration and no client_id was configured; set oauth_client_id in your MCP server config")
```
**建议**: 这个错误信息很好（清晰且有解决方案），建议在其他关键错误路径也保持同样的用户友好级别。

#### S3. CommandGate 应导出规则配置接口
**文件**: `internal/tool/command_gate.go`
**建议**: 当前 block/ask 规则硬编码在 `NewCommandGate()` 中。考虑支持从配置文件加载自定义规则，允许用户按项目调整安全策略。

#### S4. edit_match.go 的模糊匹配算法可提取为独立包
**文件**: `internal/tool/edit_match.go`
**建议**: `resolveOldText` 中的行号解析、缩进匹配、模糊搜索逻辑通用性较强，可以考虑提取为独立的编辑匹配库。

#### S5. Registry.Clone 性能优化
**文件**: `internal/tool/tool.go:148-161`
```go
func (r *Registry) Clone() *Registry {
    r.mu.RLock()
    defer r.mu.RUnlock()
```
**建议**: 对于高频 Clone 场景（多个子代理同时创建），可以考虑使用 sync.Pool 缓存 Registry 实例或采用 copy-on-write 策略。

#### S6. 统一 Plugin 接口设计
**文件**: `internal/plugin/plugin.go` + `internal/plugin/mcp_loader.go`
**建议**: `Plugin` 接口的 `Init(config)` 方法对 MCP 插件无意义（返回 nil），而 MCP 插件的核心是 `Connect()`。考虑拆分接口：
- `StaticPlugin`（命令/Go 插件）
- `DynamicPlugin`（MCP 插件，需要 Connect 生命周期）

#### S7. MCP Client 应支持请求级超时
**文件**: `internal/mcp/client.go:299`
**建议**: 当前超时由调用方的 `ctx` 控制，但 `Client` 没有默认的请求级超时。如果调用方忘记设置超时，请求可能永远挂起。建议添加默认的请求级超时（如 60 秒）。

---

## 架构评价

### 优点
1. **分层安全模型**: CommandGate + PermissionPolicy + PathSandbox 三层防护设计合理
2. **工具注册机制**: `Registry` + `Tool` 接口简洁高效，`Clone` 机制确保子代理隔离
3. **MCP 集成完整**: 支持 stdio/HTTP/WS 三种传输，OAuth 2.1 + PKCE + Device Flow 认证齐全
4. **错误处理**: 工具层统一区分 "用户可见错误"（`IsError: true`）和 "系统错误"（Go error），设计清晰
5. **自动后台化**: 长时间运行的命令自动转为后台任务，避免阻塞 agent loop
6. **并发安全**: Registry、MCPPlugin、MCPManager 都正确使用了读写锁

### 待改进
1. **插件安全模型**: Go `.so` 插件缺乏沙箱和校验，是最显著的安全缺口
2. **MCP Client 全局锁**: 单锁设计在高并发场景下会成为瓶颈
3. **命令门控正则 vs AST**: 基于正则的命令检测无法处理编码/间接引用等高级绕过
4. **Hook 系统原始**: 文件路径提取用字符串搜索而非 JSON 解析

## 测试覆盖评价

各模块均有对应的测试文件：
- `internal/tool/`: 42个测试文件，覆盖命令门控、编辑匹配、LSP、Git工具等
- `internal/plugin/`: 3个测试文件（plugin_test.go, mcp_loader_test.go, mcp_pure_test.go）
- `internal/hooks/`: 1个测试文件（runner_test.go）
- `internal/mcp/`: 未看到独立的测试文件（MCP 测试可能在 plugin 层）
- `internal/permission/`: 无独立测试文件

**测试缺口**:
- `internal/permission/sandbox.go` — 无单元测试，TOCTOU 竞态和路径规范化边界需要测试
- `internal/mcp/oauth.go` — OAuth 流程缺乏单元测试（PKCE 生成、token 刷新、设备流轮询）
- `internal/hooks/runner.go` — `ExtractFilePath` 的 JSON 边界用例未覆盖
- `internal/plugin/loader.go` — `.so` 插件加载的恶意输入测试

---

## 总结

工具层整体架构设计良好，安全分层清晰，并发处理规范。最需要关注的是：
1. **Go .so 插件加载安全**（Critical）— 需要添加路径限制和可选签名校验
2. **Hook 敏感数据泄露**（Critical）— 需要改用管道/文件传递 RAW_INPUT
3. **MCP Client 全局锁**（Major）— 需要评估并发场景下的性能影响
4. **PathSandbox TOCTOU**（Major）— 在高安全场景下需要 fd 级校验
