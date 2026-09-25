# r74 — Selective Escalation: `ask_user` fallback semantics (HiL alignment)

- 轮次: r74 (2026-09-25)
- 分支: `r74-escalation-fallback`
- 结论: 实施型轮次，零新增 detector

## 1. 在线研究信号

- **HiL-Bench (arXiv:2604.09408), "Do Agents Know When to Ask for Help?"**:
  frontier coding agents 在任务说明不完整/有歧义时成功率从 89% 跌至 4%。
  瓶颈不是能力而是判断（judgment）——知道何时自主行动、何时求助。
  现有 benchmark 只奖励执行正确性，"幸运猜测"与"确证后行动"同分。
  (Scale.com 亦有解读文章。)
- 多个工程博客（harnessengineering.academy、mindra.co、dkbalachandar 等）
  独立收敛到同一方法论：**escalation pattern / ladder of autonomy**——
  低风险工作自主完成，结果关键时求助；escalation 应有预定义协议且
  "不应该被表述为失败"。
- Simon Willison (2026-09) 对 Jev/System-One "decision models"（返回
  置信度分数的决策模型）的评注：harness 侧获取校准置信度仍是开放问题。

## 2. 与历史轮次的边界（非重复声明）

- r73 = agent loop 终止控制（semantic early-stopping / consensus hard-stop）；
  r74 = 人机交互面的求助门（escalation to human），互补不重叠。
- r68 = hook deny stickiness / approval fatigue（权限系统审批疲劳）；
  r74 = 歧义/信息不足驱动的结构化澄清，机制不同。
- r57 = prompt layers（分层 prompt 架构）；r74 只有一段模式无关的
  澄清指导 + 工具降级语义，无分层结构改动。
- 未新增任何 detector/checker。

## 3. Gap（实施前实际状态）

`ask_user` 工具本身已存在（TUI/IM daemon/desktop 安装 handler；MCP
elicitation 走 `AskDirect` 桥）。但存在两个用户可感知缺口：

1. **非 autopilot 模式的主 prompt 没有任何 escalation 指导**——
   "When to escalate" 段只出现在 autopilot 分支；supervised/auto/bypass
   下 agent 仅凭工具描述一句话判断何时求助。
2. **无 handler 会话（pipe、headless）的失败语义是误导性的**——
   错误文案为 "ask_user is only available in interactive TUI sessions"
   （与事实不符：IM/desktop 也有 handler），且没有行为指导，agent
   要么换措辞重试（浪费轮次）要么静默猜测——正是 HiL-Bench 指出的
   失败模式。

## 4. 实施

| 文件 | 变更 |
|------|------|
| `internal/tool/ask_user.go` | 新增 `AskUserToolName` 常量；no-handler 分支改为 directed fallback 文案（不重试、采用最安全可逆假设、最终答复显式声明假设）；`AskDirect` 错误文案对齐 |
| `internal/agentruntime/prompt.go` | 模式无关的 `## Clarifications` 段：仅当 `ask_user` 已注册时注入（swarm/sub-agent 天然排除） |
| `internal/tool/ask_user_test.go` | 断言新 fallback 文案；新增 `TestAskUserAskDirectRequiresHandler` |
| `internal/agentruntime/prompt_clarifications_test.go` | 新增：指导段随 ask_user 注册出现/缺席 |
| `internal/agentruntime/mcp_elicitation_test.go` | 断言对齐新错误文案 |
| `docs/guide/modes.md` | 新增 "Selective Escalation (`ask_user`)" 章节 |

## 5. 验证

```
go build -tags goolm ./...                                  # PASS
go test -tags goolm -p 1 -parallel 1 ./internal/tool        # PASS
go test -tags goolm -p 1 -parallel 1 ./internal/agentruntime # PASS
```

## 6. Backlog（未实施，供后续轮次参考）

- IM/A2A 远程会话的 ask_user 回传协议（把问题路由回发起方用户）
- 澄清交互的会话级统计（answered/partial/unanswered 比率）呈现
- 若 provider 侧出现 decision-model 形态输出，可将置信度接入
  escalation 策略
