# Shift Handoff（跨会话交接简报）

新会话自动注入同 workspace 上一会话的交接简报（`## Previous Session Handoff` 段），消除"换班失忆"：cron/定时/一次性任务每次从零读上下文，不知道上一个会话在做什么。

## 行为

- **触发条件**：仅**全新会话**（非 resume——resume 会话已恢复完整历史，无需简报）
- **注入内容**：上一会话的标题、结束时间（"N hours ago"）、目标（首条用户消息）、末态（末条 assistant 消息）
- **预算**：整体 ≤800 rune，goal/state 各截断 280 rune——固定小额 token 成本
- **时效窗**：默认 **72 小时**（覆盖隔夜与周末间隔，不复活陈旧工作）
- **确定性拼接**：零 LLM 调用，一次会话 Load（`internal/agentruntime/handoff.go`）

## 配置开关

```yaml
disable_handoff: true   # 关闭注入（默认开启）
```

## 设计参照

- METR：agent 可完成任务时域每 ~7 个月翻倍，跨 context window 延续为核心工程难题
- Anthropic《Effective harnesses for long-running agents》：progress 文件 + session 起始 get-up-to-speed 仪式——本特性是其在单机编码 agent 上的最小落地形态
