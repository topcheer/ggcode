# Knight 自动进化系统设计

> 创建时间：2026-04-19
> 状态：设计完成，待实现

## 一、总体架构

```
┌─────────────────────────────────────────────────────────┐
│  Daemon 进程                                             │
│                                                          │
│  ┌──────────┐     ┌──────────┐     ┌──────────────┐    │
│  │ 主 Agent  │────→│  Knight   │────→│  Skill 进化   │    │
│  │ (交互式)  │事件  │ (后台精灵) │定时  │  系统         │    │
│  └──────────┘     └──────────┘     └──────────────┘    │
│       │                │                    │           │
│       ↓                ↓                    ↓           │
│   用户交互         后台任务              两级 Skill       │
│   IM/TUI         补充测试/分析          系统+项目级       │
│                                                          │
│  ┌──────────────────────────────────────────────────┐   │
│  │  Token 预算管理器                                  │   │
│  │  日预算 5M tokens，JSONL 按天记录                   │   │
│  └──────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────┘
```

## 二、Skill 两级作用域

### 2.1 目录结构

```
~/.ggcode/skills/                    ← 系统级（跟用户走）
  ├── docker-debug/SKILL.md
  ├── go-build-test/SKILL.md
  └── git-workflow/SKILL.md

~/.ggcode/skills-staging/            ← 系统级 staging（Knight 写）
  ├── knight-20260419-docker.md
  └── knight-20260420-git.md

项目/.ggcode/skills/                  ← 项目级（跟项目走）
  ├── build-flow/SKILL.md
  ├── api-conventions/SKILL.md
  └── db-migration/SKILL.md

项目/.ggcode/skills-staging/          ← 项目级 staging（Knight 写）
  ├── knight-20260419-build.md
  └── knight-20260420-api.md

项目/.ggcode/skills-changelog.jsonl   ← 变更日志
项目/.ggcode/skills-snapshots/        ← 版本快照（回滚用）

~/.ggcode/knight/                     ← Knight 运行时数据
  ├── usage-2026-04-19.jsonl          ← 每日 token 用量
  ├── tasks.json                      ← 任务队列
  └── state.json                      ← Knight 状态
```

### 2.2 作用域判断规则

Knight 创建 skill 时自动判断作用域：

| 模式特征 | 作用域 | 例子 |
|---------|--------|------|
| 涉及项目相对路径 `internal/xxx` | 项目级 | "本项目用 `go test -tags=!integration`" |
| 涉及 API 端点、数据库表名、业务术语 | 项目级 | "本项目的 REST 约定" |
| 涉及项目特定的目录结构或文件名 | 项目级 | "React Native 项目的调试流程" |
| 涉及项目约定的命名规则 | 项目级 | "本项目的 Go 接口命名规范" |
| 纯工具操作序列（docker, git, go） | 系统级 | "Docker 调试三步法" |
| 涉及环境配置、平台特性 | 系统级 | "macOS 开发环境配置" |
| 涉及 `~/.ggcode/` 或环境变量 | 系统级 | "ggcode 配置热加载" |
| 通用的错误修复模式 | 系统级 | "Go PTL 错误修复流程" |
| 无法确定 | 项目级 | 更安全，不污染全局 |

### 2.3 现有基础设施

ggcode 已有的 Skill 加载能力（不需要重写）：

- `commands.Loader` 已扫描 `~/.ggcode/skills/` 和 `.ggcode/skills/`
- `buildSkillsSystemPrompt` 已做渐进式披露（索引注入 system prompt，4000 字符上限）
- `skill` 工具已支持按需加载全文
- frontmatter 解析已支持 YAML + Markdown 格式
- 项目级同名 skill 自动覆盖系统级

### 2.4 Skill 文件格式

```markdown
---
name: go-build-test
description: 构建 Go 项目并运行测试
scope: global                         # global | project
platforms: [darwin, linux]            # 平台限制
requires: [go]                        # 依赖的命令/工具
triggers:                             # 自动激活条件（未来用）
  tools: [run_command]
  patterns: ["go build", "go test"]
created_by: knight                    # user | knight | agent
created_from: session-20260419        # 来源 session
created_at: 2026-04-19
updated_at: 2026-04-19
usage_count: 0
last_used: null
effectiveness_scores: []              # [4, 5, 3] 使用后评分
frozen: false                         # 冻结后 Knight 不可修改
---

# 构建 Go 项目

## 标准流程
1. `go build ./...` 确认编译通过
2. `go test -count=1 ./...` 运行测试
3. `go vet ./...` 静态检查

## 注意事项
- CGO_ENABLED=0 用于交叉编译
- `go test -tags=!integration` 跳过集成测试

## 失败排查
- 编译错误：检查 go.mod 是否需要 `go mod tidy`
- 测试超时：加 `-timeout 60s`
```

## 三、Knight 精灵

### 3.1 Knight 是什么

Daemon 模式下运行的后台 agent，闲时持续优化项目。不是独立进程，而是 daemon 内的调度器 + 独立的 agent 实例。

### 3.2 Knight 的任务来源

```
定时任务：
  - 每 1 小时：分析最近的 session，提炼 skill 候选
  - 每 6 小时：验证所有 skill 有效性（依赖检查 + 执行验证）
  - 每天 02:00：检查项目测试覆盖率
  - 每周一：清理过时 skill、整理 MEMORY.md

事件触发：
  - 主 agent 完成一轮 → 分析本轮是否值得提炼 skill
  - 新 skill 创建 → 安排验证
  - 用户指令 → /knight run "补充 internal/im 的测试"

闲时优化（无用户交互 5 分钟后）：
  - 补充测试用例（只写 _test.go）
  - 跑回归测试 + 修复逻辑问题
  - 文档和代码一致性检查
```

### 3.3 Knight 的权限

| 操作 | 主 Agent | Knight |
|------|---------|--------|
| 读源码 | ✅ | ✅ |
| 写源码 | ✅ | ❌（除非是修复测试发现的 bug，需 staging 审批） |
| 写 `_test.go` | ✅ | ✅ |
| 写 `.ggcode/skills/` | ✅ | ❌ |
| 写 `.ggcode/skills-staging/` | ✅ | ✅ |
| 写 `GGCODE.md` | ✅ | ✅（只追加） |
| 删除 skill | ✅ | ❌ |
| 跑命令 | ✅ | ✅（只读命令为主） |
| 调 LLM | ✅ | ✅（独立 token 预算） |

### 3.4 Knight 的 Skill 操作流程

```
Knight 分析 session / 发现模式
  ↓
判断作用域（系统级 or 项目级）
  ↓
生成 skill 文件到对应 staging 目录：
  系统级：~/.ggcode/skills-staging/knight-20260419-xxx.md
  项目级：.ggcode/skills-staging/knight-20260419-xxx.md
  ↓
自动验证：
  ├─ 格式检查（frontmatter 必填字段完整）
  ├─ 去重检查（和已有 skill 比较相似度 > 80% 则跳过）
  ├─ 依赖检查（requires 里的命令是否存在）
  └─ 执行验证（流程类 skill 实际跑一遍）
  ↓
验证结果：
  ├─ 全部通过 + trust_level=auto → 自动晋升到 skills/
  ├─ 全部通过 + trust_level=staged → IM 通知用户审批
  └─ 未通过 → 留在 staging，记录失败原因
  ↓
晋升时：
  - 自动创建 snapshot（skills-snapshots/）
  - 写 changelog（skills-changelog.jsonl）
```

### 3.5 信任等级

```yaml
knight:
  # trust_level 控制 Knight 的自治权
  # readonly: 只分析不写任何东西
  # staged:   写 staging 但晋升需要用户审批（默认）
  # auto:     自动晋升通过验证的 skill
  trust_level: staged
```

## 四、Token 预算管理

### 4.1 每日预算

```
默认：5,000,000 tokens/天（5M）
配置项：knight.daily_token_budget

统计范围：input_tokens + output_tokens
不含 cache_read / cache_write（缓存 token 成本低）
```

### 4.2 用量记录

按天 JSONL 文件，每条 LLM 调用一条记录：

```
~/.ggcode/knight/usage-2026-04-19.jsonl

{"time":"2026-04-19T15:30:00","task":"skill-analysis","input":1200,"output":800,"total":2000}
{"time":"2026-04-19T15:35:00","task":"test-generation","input":500,"output":300,"total":800}
```

每天一个文件，隔天自动不读（天然过期）。

### 4.3 预算检查逻辑

```go
func (k *Knight) canStartTask(estimatedTokens int) bool {
    used := k.todayUsage()           // sum(今日 jsonl 里所有 total)
    budget := k.dailyTokenBudget()   // 默认 5,000,000
    remaining := budget - used
    
    if remaining < 100_000 {         // 余额不足，不启动
        return false
    }
    return true
}

func (k *Knight) recordUsage(task string, usage provider.TokenUsage) {
    total := usage.InputTokens + usage.OutputTokens
    // 追加到今日 jsonl
    appendUsageRecord(k.usageFile(), task, usage.InputTokens, usage.OutputTokens, total)
}
```

### 4.4 不设单任务上限

单任务不限 token，但日预算保护了总量。如果一个大任务把日预算吃完了，后面的任务就等明天。Knight 的 agent 实例和主 agent 的 `onUsage` 回调一样，每次 LLM 调用后累加到用量文件。

## 五、Skill 生命周期管理

### 5.1 使用追踪

每次 LLM 调用 `skill` 工具加载某个 skill 时：
- `usage_count++`
- `last_used = now`
- 更新 SKILL.md 的 frontmatter

### 5.2 效果评估

每次使用 skill 完成任务后，在 agent 循环末尾追加评估：

```
System prompt 追加：
  你刚刚使用了 skill "{name}"。
  它对完成任务有帮助吗？用 1-5 分评估。
  在回复末尾包含 [SKILL_RATING:N]。
```

解析 LLM 回复中的 `[SKILL_RATING:N]`，追加到 `effectiveness_scores`。

### 5.3 淘汰规则

Knight 定期执行健康检查：

| 条件 | 动作 |
|------|------|
| 30 天未使用（usage_count=0 或 last_used > 30天） | 标记 `stale`，通知用户 |
| effectiveness 平均分 < 3/5（至少 3 次评分） | 标记 `low_quality`，通知用户 |
| requires 里的命令不存在 | 标记 `broken`，通知用户 |
| 用户冻结（frozen=true） | Knight 不可修改 |
| 用户删除 | 直接删除 |

Knight 只标记，**永远不自动删除**。标记后的 skill 留给用户决定：
- `/knight approve` → 恢复
- `/knight reject` → 删除
- 不操作 → 下次提醒

### 5.4 Skill 更新

Knight 发现 skill 需要更新时：
1. 读取现有 skill
2. 生成 patch 版本到 staging
3. 记录 diff 到 changelog
4. 等待用户审批（或 trust_level=auto 时自动晋升）

已有 skill 的 snapshot 保留最近 3 个版本，供回滚。

## 六、用户交互

### 6.1 IM 通知

Knight 通过 IM 推送报告：

```
🌙 Knight 报告（2026-04-19）：
📊 今日用量：1.2M / 5M tokens
✅ 分析了 3 个 session，发现 1 个可复用模式
📝 新 skill 候选："Go 项目构建与测试流程"
🔍 验证：格式✓ 去重✓ 依赖✓ 执行✓
👉 /knight approve 晋升 / /knight reject 拒绝

🌙 Knight 报告：
✅ 为 internal/im 补充了 5 个测试用例
✅ 回归测试全部通过
📊 覆盖率：67% → 73%
```

### 6.2 TUI 命令

| 命令 | 说明 |
|------|------|
| `/knight status` | 查看今日用量、任务队列、最近报告 |
| `/knight run "xxx"` | 手动给 Knight 派任务 |
| `/knight review` | 查看 staging 中的新 skill |
| `/knight approve [name]` | 批准 skill 晋升 |
| `/knight reject [name]` | 拒绝并删除 staging 中的 skill |
| `/knight freeze <name>` | 冻结 skill，Knight 不可修改 |
| `/knight unfreeze <name>` | 解冻 |
| `/knight skills` | 查看所有 skill 及健康度 |
| `/knight rollback <name>` | 回滚到上一版本 |
| `/knight budget` | 查看剩余预算 |

## 七、实现计划

### 阶段一：基础设施（1 周）

| 任务 | 文件 | 说明 |
|------|------|------|
| Skill 索引器 | `internal/knight/skill_index.go` | 扫描两级 skills/ 和 skills-staging/，构建索引 |
| Skill 验证器 | `internal/knight/skill_validator.go` | 格式、去重、依赖、执行验证 |
| Skill 晋升器 | `internal/knight/skill_promoter.go` | staging → skills/ 的晋升 + snapshot + changelog |
| Token 预算 | `internal/knight/budget.go` | JSONL 按天记录 + 日预算检查 |
| 配置结构 | `internal/config/knight.go` | knight 配置解析 |

### 阶段二：Knight 核心（1 周）

| 任务 | 文件 | 说明 |
|------|------|------|
| 调度器 | `internal/knight/scheduler.go` | 定时任务 + 事件触发 + 闲时检测 |
| Agent 工厂 | `internal/knight/runner.go` | 创建受权限限制的 agent 实例 |
| Session 分析器 | `internal/knight/analyzer.go` | 分析 session JSONL，提炼 skill 候选 |
| 作用域判断 | `internal/knight/scope.go` | 根据 skill 内容自动判断全局/项目级 |
| 测试生成器 | `internal/knight/testgen.go` | 分析代码，生成测试用例 |

### 阶段三：用户交互（3-5 天）

| 任务 | 文件 | 说明 |
|------|------|------|
| Knight 命令 | `internal/tui/knight_commands.go` | /knight 系列命令 |
| IM 通知 | `internal/im/knight_emit.go` | Knight 报告推送到 IM |
| Skill 效果评估 | `internal/knight/assessor.go` | 使用后评分 + 淘汰检查 |
| TUI 面板 | `internal/tui/knight_panel.go` | Knight 状态面板（可选） |

### 阶段四：进化闭环（3-5 天）

| 任务 | 文件 | 说明 |
|------|------|------|
| 生命周期管理 | `internal/knight/lifecycle.go` | usage_count、effectiveness、淘汰 |
| Skill patch | `internal/knight/patcher.go` | Knight 发现 skill 过时时生成 patch |
| 自动验证 | `internal/knight/auto_validate.go` | 定期验证所有 skill 有效性 |
| 跨项目学习 | `internal/knight/cross_project.go` | 从项目级 skill 中提炼通用模式升级为系统级 |

## 八、关键设计决策

| 决策 | 选择 | 理由 |
|------|------|------|
| Knight 进程模式 | daemon 内调度器，非独立进程 | 共享 provider/session 基础设施，减少资源开销 |
| Skill 存储 | 文件系统（Markdown + YAML） | Git 友好、可人工编辑、和现有 commands 系统一致 |
| Token 统计 | JSONL 按天文件 | 简单、天然过期、和 session store 风格一致 |
| 单任务限制 | 无 | 日预算已保护总量，单任务不限制避免打断有价值的深度分析 |
| 作用域判断 | 基于内容特征启发式 | 不需要用户手动指定，自动判断 |
| 安全模型 | staging + 验证 + 审批 | Knight 永远不直接写 skills/ |
| 淘汰方式 | 标记 + 通知，不自动删除 | 防止误删有价值的 skill |
