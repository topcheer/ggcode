# ggcode 综合代码质量审查报告

**审查范围**: `internal/` 目录，334 个 Go 源文件（~101k LOC 非测试 + ~69k LOC 测试），48 个包

---

## 1. 代码组织

### 1.1 大型文件 / "上帝对象"
| 严重度 | ⚠️ 中 |
|---|---|

**问题**: 多个文件超过 900 行，职责边界模糊。

| 文件 | 行数 | 问题 |
|---|---|---|
| `internal/lsp/client.go` | 1053 | LSP 客户端 + JSON-RPC + 类型定义混合 |
| `internal/im/slack_adapter.go` | 1009 | 单个适配器过长 |
| `internal/tui/provider_panel.go` | 1001 | UI + 业务逻辑混合 |
| `internal/im/discord_adapter.go` | 985 | 同上 |
| `internal/lsp/discovery.go` | 964 | 发现逻辑集中 |
| `internal/harness/release.go` | 931 | 发布流程复杂 |
| `internal/mcp/oauth.go` | 914 | OAuth 流程庞大 |
| `internal/acp/types.go` | 893 | 仅类型定义，但体量过大 |
| `internal/auth/a2a_oauth.go` | 883 | 多个 OAuth 流程合一 |
| `internal/tui/model.go` | 880 | TUI 主模型过于庞大 |

**TUI 包** 是最大包（~43k LOC 非测试，47+ 文件），有 27 个面板文件，每个 400-1260 行。

**建议**: 
- `lsp/client.go` 拆分为 `types.go`、`rpc.go`、`client.go`
- IM 适配器抽取共享逻辑到 `internal/im/base_adapter.go`
- `acp/types.go` 按职责拆分为 `types_request.go`、`types_response.go` 等
- TUI 面板引入通用 `Panel` 接口减少重复

### 1.2 测试文件过大
`internal/harness/harness_test.go`（3512 行）和 `internal/tui/tui_test.go`（3222 行）过于庞大，建议按测试场景拆分。

---

## 2. Go 惯用法与最佳实践

### 2.1 Context 使用
| 严重度 | ✅ 良好 |
|---|---|

- 全局共 **~62 个** `go func()` 协程启动，绝大多数正确传递了 `ctx context.Context`
- 未发现 `context.TODO()` 使用（生产代码中）
- `context.Background()` 使用合理（初始化场景）
- Agent 的 `shutdownCtx` + `shutdownCancel` 模式正确

### 2.2 接口定义
| 严重度 | ✅ 良好 |
|---|---|

- 遵循 Go 惯例在消费方定义接口（如 `provider.Provider`、`session.Store`、`im.Sink`）
- 接口粒度合理，`provider.Provider`（4 方法）结构清晰
- 使用接口组合实现可选能力检测（如 `providerAwareContextManager`、`usageAwareContextManager`）

### 2.3 Import 别名
| 严重度 | ✅ 良好 |
|---|---|

- `internal/context` 正确使用 `ctxpkg` 别名避免与标准库 `context` 冲突

### 2.4 命名规范
| 严重度 | ⚠️ 低 |
|---|---|

- 少量非导出类型在包内使用，符合 Go 惯例（如 `rpcError`、`startableSink`）
- IM 适配器命名不一致：`*Adapter`（slack、discord、feishu）vs `*WechatAdapter`（首字母大写导出类型）

**建议**: 统一 IM 适配器结构体为非导出命名（`wechatAdapter`），通过 `Sink` 接口暴露。

---

## 3. 测试覆盖率与质量

### 3.1 测试覆盖率概览
| 严重度 | ⚠️ 中 |
|---|---|

| 包 | 非测试 LOC | 测试 LOC | 比率 |
|---|---|---|---|
| `agent/` | 1,938 | 2,644 | 1.36x ✅ |
| `provider/` | 3,060 | 1,874 | 0.61x ⚠️ |
| `tool/` | 10,893 | 7,592 | 0.70x ⚠️ |
| `config/` | 4,444 | 5,486 | 1.23x ✅ |
| `harness/` | 7,167 | 10,660 | 1.49x ✅ |
| `tui/` | 42,957 | 21,396 | 0.50x ⚠️ |
| `im/` | 24,042 | 18,170 | 0.76x ⚠️ |
| `mcp/` | 2,653 | 1,347 | 0.51x ⚠️ |
| `a2a/` | 5,143 | 6,201 | 1.21x ✅ |

### 3.2 无测试覆盖的包
| 严重度 | 🔴 高 |
|---|---|

以下核心包完全没有测试：
- **`internal/daemon/`** — 824 行，守护进程核心逻辑
- **`internal/version/`** — 小包但无验证
- **`internal/markdown/`** — Markdown 渲染
- **`internal/im/stt/`** — 语音转文本

**建议**: 优先为 `internal/daemon/` 添加单元测试，该包包含会话管理和 fork 逻辑。

### 3.3 测试模式
| 严重度 | ✅ 良好 |
|---|---|

- 大量使用 **表驱动测试**（`t.Run` 使用 800+ 次）
- 集成测试使用 build tags（`//go:build integration`）正确隔离
- Mock 使用适度（`agent_coverage_test.go`、`knight_test.go` 等）
- Harness 和 A2A 包含端到端测试

---

## 4. 错误处理一致性

### 4.1 错误包装
| 严重度 | ✅ 良好 |
|---|---|

- **800+ 处** 使用 `fmt.Errorf("...: %w", err)` 正确包装错误
- **129 处** 使用 `errors.New` 创建哨兵错误
- 自定义错误类型丰富：`JSONRPCError`、`OAuthRequiredError`、`checkpointDeclinedError` 等

### 4.2 被吞噬的错误
| 严重度 | ⚠️ 中 |
|---|---|

- `internal/harness/auto_init.go:106` — `bootstrapHarnessState` 错误被 `_ = err` 丢弃（注释说明 non-fatal，但应记录日志）
- `internal/im/feishu_adapter.go:118` — `err` 被声明但从未使用，`_ = err` 仅用于避免编译错误

```go
// 当前代码 (feishu_adapter.go:113-120)
if v := strings.TrimSpace(stringValue(adapterCfg.Extra, "webhook_port")); v != "" {
    var err error
    if n, ok := intValueStr(v); ok && n > 0 {
        webhookPort = n
    } else {
        _ = err // err 从未被赋值
    }
}
```

**建议**:
```go
// 修正后
if v := strings.TrimSpace(stringValue(adapterCfg.Extra, "webhook_port")); v != "" {
    if n, ok := intValueStr(v); ok && n > 0 {
        webhookPort = n
    } else {
        debug.Log("im.feishu", "invalid webhook_port: %s", v)
    }
}
```

### 4.3 io.ReadAll 忽略错误
| 严重度 | 🔴 高 |
|---|---|

10+ 处 `io.ReadAll(resp.Body)` 的错误被 `_ ` 丢弃：

| 文件 | 行号 |
|---|---|
| `internal/auth/a2a_oauth.go` | 174, 250, 327 |
| `internal/a2a/client.go` | 431 |
| `internal/provider/model_discovery.go` | 88 |
| `internal/mcp/oauth.go` | 819 |
| `internal/im/feishu_adapter.go` | 910, 1157, 1194, 1385 |

**建议**:
```go
// 当前
body, _ := io.ReadAll(resp.Body)

// 修正后
body, err := io.ReadAll(resp.Body)
if err != nil {
    return fmt.Errorf("read response body: %w", err)
}
```

---

## 5. 资源管理

### 5.1 HTTP Body 泄漏风险
| 严重度 | 🔴 高 |
|---|---|

20+ 个文件的 `resp.Body` 引用数量超过 `Close()` 调用数量：

**高风险文件**:
| 文件 | Body 引用 | Close 调用 | 差异 |
|---|---|---|---|
| `internal/im/feishu_adapter.go` | 22 | 10 | **12** |
| `internal/mcp/oauth.go` | 14 | 7 | **7** |
| `internal/im/slack_adapter.go` | 17 | 8 | **9** |
| `internal/im/discord_adapter.go` | 13 | 7 | **6** |
| `internal/a2a/client.go` | 12 | 7 | **5** |

> 注：部分差异可能因为 `ReadAll` 或 `Decode` 消费了 Body（Go 中读取完毕后 GC 会回收），但最佳实践仍应显式 Close。

**建议**: 统一封装 HTTP 请求工具函数：
```go
func doJSON(ctx context.Context, client *http.Client, req *http.Request, result interface{}) error {
    resp, err := client.Do(req.WithContext(ctx))
    if err != nil {
        return fmt.Errorf("http request: %w", err)
    }
    defer resp.Body.Close()
    if resp.StatusCode >= 400 {
        body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
        return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
    }
    if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
        return fmt.Errorf("decode response: %w", err)
    }
    return nil
}
```

### 5.2 协程泄漏
| 严重度 | ✅ 低风险 |
|---|---|

- 62 个 `go func()` 启动点，绝大多数通过 `ctx.Done()` 或 `safego.Go` 管理
- `internal/im/dingtalk_adapter.go:247` 的 WebSocket 读取协程正确使用 `readErr` channel + `ctx.Done()` 退出
- 使用 `safego` 包统一 panic 恢复，良好实践

### 5.3 文件句柄
| 严重度 | ✅ 良好 |
|---|---|

- `knight/lock_*.go` 的文件锁正确使用 `defer f.Close()`
- `session/store.go` 的原子写入模式（写临时文件 + rename）正确

---

## 6. 代码重复

### 6.1 IM 适配器重复
| 严重度 | 🔴 高 |
|---|---|

13+ 个 IM 适配器文件（总计 ~24k LOC），每个实现相同的模式：
- `Start(ctx context.Context)` — 启动连接
- `Send(ctx context.Context, binding, event)` — 发送消息
- `handleMessage(ctx, payload)` — 处理入站消息
- HTTP 客户端创建、配置解析、重连逻辑

每个适配器独立实现，无共享基础结构。

**建议**: 创建 `internal/im/base_adapter.go`：
```go
type BaseAdapter struct {
    name       string
    httpClient *http.Client
    manager    *Manager
}

func (b *BaseAdapter) Init(name string, cfg AdapterConfig, mgr *Manager) {
    b.name = name
    b.httpClient = &http.Client{Timeout: 30 * time.Second}
    b.manager = mgr
}

func (b *BaseAdapter) DoHTTP(req *http.Request) (*http.Response, error) {
    resp, err := b.httpClient.Do(req)
    // 统一错误处理、日志、指标
}
```

### 6.2 TUI 面板重复
| 严重度 | ⚠️ 中 |
|---|---|

27 个面板文件结构高度相似：初始化 → 更新 → 渲染 View()。缺乏统一 `Panel` 接口。

**建议**: 定义通用面板接口：
```go
type Panel interface {
    Init() tea.Cmd
    Update(tea.Msg) (Panel, tea.Cmd)
    View() string
    SetFocused(bool)
}
```

### 6.3 OAuth 流程重复
| 严重度 | ⚠️ 中 |
|---|---|

- `internal/auth/a2a_oauth.go`（883 行）
- `internal/mcp/oauth.go`（914 行）
- `internal/auth/claude_oauth.go`

三个 OAuth 实现存在大量 PKCE/Token Exchange 重复逻辑。

---

## 7. 文档质量

### 7.1 包级文档
| 严重度 | ⚠️ 中 |
|---|---|

**17/48 个包（35%）缺少 `// Package xxx` 文档**：

缺少文档的关键包：
- `internal/mcp/` — MCP 客户端核心
- `internal/provider/` — LLM 提供商适配器
- `internal/session/` — 会话持久化
- `internal/tool/` — 工具系统
- `internal/tui/` — TUI 核心（43k LOC！）
- `internal/plugin/` — 插件系统
- `internal/webui/` — Web 界面

### 7.2 导出函数文档
| 严重度 | ⚠️ 中 |
|---|---|

- **348/505（68%）** 导出函数有文档注释
- **157 个（32%）** 导出函数缺少 Godoc

**建议**: 优先补充核心包（`provider`、`tool`、`session`、`mcp`）的包级文档和关键导出函数文档。

---

## 8. 日志实践

### 8.1 Debug 包设计
| 严重度 | ✅ 优秀 |
|---|---|

`internal/debug/debug.go`（765 行）设计精良：
- 分类日志系统（agent、context、openai、anthropic、qq、tg、discord 等 15+ 类别）
- 环境变量控制（`GGCODE_DEBUG`、`GGCODE_DEBUG_<CATEGORY>`）
- 异步写入 + 缓冲（1024 消息缓冲区）
- 日志轮转（50MB 上限，3 个文件）
- 消息截断（4096 字符）
- `safego` 集成（panic 恢复）

### 8.2 生产代码中的调试日志
| 严重度 | ✅ 良好 |
|---|---|

- `fmt.Println` 仅 25 处，`fmt.Printf` 仅 1 处 — 几乎无调试残留
- 无 `logrus` 依赖（仅 examples 中使用）
- 统一使用 `debug.Log(tag, ...)` 模式

### 8.3 敏感数据
| 严重度 | ✅ 未发现明显问题 |
|---|---|

- 未发现 API Key、Token、Secret 直接写入日志
- OAuth Token 缓存使用 0600 权限

---

## 9. API 稳定性

### 9.1 导出 API 面积
| 严重度 | ⚠️ 中 |
|---|---|

| 包 | 导出函数数 |
|---|---|
| `harness/` | 83 ⚠️ |
| `config/` | 32 |
| `provider/` | 26 |
| `tui/` | 22 |
| `tool/` | 18 |
| `agent/` | 1 |

`internal/harness/` 导出了 83 个函数，API 面积过大，增加了破坏性变更风险。

### 9.2 init() 函数
| 严重度 | ⚠️ 低 |
|---|---|

TUI 包有 **11 个 `init()` 函数**（i18n 注册），虽然功能正确，但隐式初始化增加了理解成本。

**建议**: 考虑使用显式注册函数 `i18n.RegisterXXX()` 在统一位置调用。

### 9.3 向后兼容性
| 严重度 | ✅ 良好 |
|---|---|

- 遗留 `provider`/`providers` 配置键在加载时被显式拒绝并报错
- `a2a.api_key`（旧字段）仍可用但 `a2a.auth.api_key` 优先 — 向后兼容模式良好
- `provider.Provider` 接口稳定（Name、Chat、ChatStream、CountTokens）

---

## 10. 总结评分

| 维度 | 评分 | 说明 |
|---|---|---|
| 代码组织 | 7/10 | 大部分包结构合理，少数上帝文件需拆分 |
| Go 惯用法 | 8/10 | Context、接口、命名基本规范 |
| 测试质量 | 7/10 | 核心路径覆盖好，daemon/version/markdown 缺失 |
| 错误处理 | 7/10 | 包装一致性好，io.ReadAll 忽略错误需修复 |
| 资源管理 | 6/10 | HTTP Body 泄漏风险最大，协程管理良好 |
| 代码重复 | 5/10 | IM 适配器和 TUI 面板重复明显 |
| 文档质量 | 6/10 | 核心函数 68% 有文档，包文档覆盖率偏低 |
| 日志实践 | 9/10 | Debug 包设计优秀，分类完善 |
| API 稳定性 | 7/10 | 向后兼容模式好，harness 导出过多 |

### 优先修复建议（按影响排序）

1. **🔴 高优**: 修复 `io.ReadAll` 忽略错误的 10+ 处 — 可能导致静默数据丢失
2. **🔴 高优**: 审计并修复 HTTP Body `Close()` 缺失 — 20+ 文件存在潜在泄漏
3. **🔴 高优**: 提取 IM 适配器共享基础设施 — 减少 ~24k LOC 中的大量重复
4. **⚠️ 中优**: 为 `internal/daemon/` 添加测试 — 824 行核心逻辑零覆盖
5. **⚠️ 中优**: 补充 17 个包的包级 Godoc 文档
6. **⚠️ 中优**: 拆分超 900 行的大型文件
7. **⚠️ 低优**: 统一 TUI 面板接口
8. **⚠️ 低优**: 减少 `internal/harness/` 导出 API 面积
