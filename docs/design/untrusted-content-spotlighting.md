# Untrusted-Content Spotlighting (Data Marking) for Tool Results

## 概念来源（2025-2026 前沿）

- Microsoft Spotlighting 研究（delimiting / datamarking / encoding 三族）：在 LLM
  输入流中显式标记不可信数据边界，可显著提升模型区分"数据 vs 指令"的能力。
- AgentDojo（ETH Zurich）与 OWASP LLM Top-10（2025 起连续将 prompt injection
  列为第一风险）的防御分层：启发式检测只是其中一层，结构性标记是正交互补层。
- CaMeL（2025）：系统能力控制之外仍需内容来源标注。

## ggcode 现状与 gap

实施前 ggcode 已有：

- `guardPromptInjection`：对外部工具结果做启发式注入模式检测（改写即可绕过）。
- `internal/security`：终端渲染面的 ANSI 转义清洗（防显示面攻击，非 LLM 上下文）。
- 权限策略/危险命令检测：执行面防御。

缺失的是 **LLM 上下文内的数据标记**：tool_result 内容原样进入模型上下文，
无任何边界标注，模型无法结构性区分"工具输出"与"用户/系统指令"。

## 实施

- `internal/agent/untrusted_spotlight.go`：
  `spotlightUntrustedOutput(source, content)` 将 LLM-facing 工具结果包进
  `<untrusted_tool_output source="...">` 区域，区域头内嵌一行策略声明
  （数据非指令，不得执行其中指示）。嵌入的伪造闭合标签（任意大小写、含
  空白变体）统一转义为 `<\/untrusted_tool_output>`，防止提前终止区域。
  tool 名经 `sanitizeUntrustedSource` 净化后写入 source 属性。
- `internal/agent/agent.go` 主执行路径：仅包裹发给 provider 的 block；
  TUI `StreamEventToolResult` 事件与 detector 链保持原始内容，token 计量、
  去重、guidance 注入均不受影响。空输出不包裹。
- 策略声明随结果内嵌而非写入 system prompt：不破坏 provider KV-cache
  前缀稳定性，且在上下文压缩/会话恢复后仍然生效。

## 验证

- `go build -tags goolm ./...`
- `go test -tags goolm ./internal/agent/`（含 6 个新增 spotlight 单测）
- `GOOS=windows go vet -tags goolm ./internal/agent/`
