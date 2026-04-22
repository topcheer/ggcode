# Pre-Compact: My Design vs Opus Implementation — Gap Analysis

逐行对比 `background-precompact-design.md`（我的设计）与 `internal/agent/agent_precompact.go`（Opus 实现），总结我遗漏了什么、Opus 改进了什么。

---

## 🔴 我的设计有 Bug，Opus 修复了

### 1. Goroutine 直接引用 `a.precompact` — 竞争写 + channel double close

**我的设计 (L92):**
```go
go func() {
    defer close(a.precompact.done)       // ← 直接引用 a.precompact
    prov := a.Provider()                 // ← 调用 getter
    a.precompact.err = err               // ← 直接写入 a.precompact
    changed, err := a.contextManager.CheckAndSummarize(bgCtx, prov)
}()
```

**Opus (L82-87, L90):**
```go
go func() {
    defer close(pc.done)                 // ← 用局部捕获的 pc
    defer cancel()                       // ← 独立 defer
    changed, err := cm.CheckAndSummarize(bgCtx, prov)  // ← 用捕获的 cm/prov
    pc.err = err                         // ← 写入局部 pc
}()
```

**问题：** 我的写法在以下序列下会崩溃：
1. Goroutine 开始运行
2. 用户 ctrl+c → `CancelPreCompact()` 被调用 → `a.precompact = nil`
3. Goroutine 尝试 `close(a.precompact.done)` → **nil pointer panic**

即使 CancelPreCompact 不 nil 掉指针，也存在更微妙的竞争：
1. Goroutine A 在 compact 中
2. CancelPreCompact 调用 `a.precompact.cancel()`
3. StartPreCompact 被再次调用（因为 `a.precompact` 未被 nil）→ 创建新的 `precompactState` 覆盖 `a.precompact`
4. Goroutine A 的 `close(a.precompact.done)` 关闭了 **新** 的 done channel
5. 新的 Goroutine B 也 `close(a.precompact.done)` → **panic: close of already closed channel**

Opus 的局部捕获彻底避免了这些问题：goroutine 永远只操作自己的 `pc`，不管 `a.precompact` 指针怎么变。

**教训：** 启动后台 goroutine 时，**永远通过局部变量传递状态，不要在 goroutine 内部通过外层结构的指针间接访问**。

---

### 2. `CancelPreCompact` 我只取消不 nil，Opus 先 nil 再取消

**我的设计:**
```go
func (a *Agent) CancelPreCompact() {
    a.mu.Lock()
    defer a.mu.Unlock()
    if a.precompact != nil && a.precompact.cancel != nil {
        a.precompact.cancel()
    }
    // 不 nil 掉 a.precompact — 等 goroutine 自己关 done
}
```

**Opus (L148-161):**
```go
func (a *Agent) CancelPreCompact() {
    a.mu.Lock()
    pc := a.precompact
    a.precompact = nil           // ← 立即断开
    a.mu.Unlock()
    if pc == nil {
        return
    }
    pc.cancelled = true          // ← 标记取消原因
    if pc.cancel != nil {
        pc.cancel()
    }
}
```

**差异分析：**

我的设计保留了 `a.precompact` 指针，意图是让 `waitForPreCompact` 能等到 `done` 关闭。但这导致一个问题：`StartPreCompact` 检查 `a.precompact != nil` 时会认为"已经有一个在跑"，拒绝启动新的——即使旧的那个已经被取消了。

Opus 的做法是立即 nil 掉 + 标记 `cancelled = true`：
- `StartPreCompact` 看到的是 `nil`，可以立即启动新的
- `waitForPreCompact` 看到的也是 `nil`（如果在 Cancel 之后调用），返回 false，走 inline compact
- Goroutine 通过 `pc.cancelled` 知道自己被取消了，不会做额外清理

**教训：** 取消操作应该是**立即生效的**——断开指针、标记状态、通知 goroutine，三步一起做。不要保留"僵尸引用"期望后续代码来清理。

---

### 3. `waitForPreCompact` 返回值语义不同

**我的设计:** ctx 取消时返回 `true`（"有 pre-compact 在进行"）
**Opus:** ctx 取消时返回 `false`（"没有可用的 compact 结果"）

```go
// Opus
case <-ctx.Done():
    return false   // 没有 usable result
```

返回 `false` 更合理。调用方 `RunStreamWithContent` 的逻辑是：
```go
a.waitForPreCompact(ctx)   // 返回值被忽略！
if err := a.maybeAutoCompact(...); err != nil { ... }
```

等等——**Opus 根本没用返回值**。`waitForPreCompact(ctx)` 的返回值被丢弃了。后面的 `maybeAutoCompact` 会自己检查 token 阈值。

这比我的设计更简洁。我的设计还在纠结"返回 true/false 代表什么语义"，Opus 直接把它当成了一个 **drain 操作**：如果有后台 compact，等它结束；如果用户取消了，不等；不管哪种情况，后面 `maybeAutoCompact` 自己判断需不需要再 compact。

**教训：** 当后续逻辑已经自带幂等检查（`maybeAutoCompact` 会看 threshold），前驱操作的返回值就不需要精确区分"成功/失败/取消"，只要保证"不会破坏后续逻辑"就够了。

---

## 🟡 我遗漏的细节，Opus 补上了

### 4. 锁内捕获 `cm` 和 `prov`，避免 unlock 后 nil

**我的设计 (L73-74):**
```go
tokens := a.contextManager.TokenCount()
threshold := a.contextManager.AutoCompactThreshold()
```
在锁内读取 threshold 和 tokens，但 goroutine 中用 `a.contextManager` 和 `a.Provider()` 重新获取。

**Opus (L56-57, L90):**
```go
a.mu.Lock()
cm := a.contextManager      // 捕获接口值
prov := a.provider           // 捕获接口值
// ... unlock ...
go func() {
    changed, err := cm.CheckAndSummarize(bgCtx, prov)  // 用捕获的值
}()
```

**差异：** 我 unlock 后在 goroutine 里调用 getter，此时 `a.contextManager` 可能已经被 `SetContextManager()` 替换了。虽然不会 panic（Go 接口值安全），但 goroutine 会对 **旧的** contextManager 做 compact，然后结果被丢弃——浪费了一次 LLM 调用。

Opus 的捕获方式意味着 goroutine 操作的就是 lock 时刻快照的 cm/prov。如果 `SetContextManager` 在中间替换了，`CancelPreCompact` 会取消这个 goroutine（因为 `Clear()` 和 `SetContextManager()` 都调用了 `CancelPreCompact()`）。

**教训：** 在锁内一次性捕获所有后台 goroutine 需要的依赖，unlock 后不再通过 getter 方法访问共享状态。

### 5. `PreCompactStatus()` — 我完全没想到的 UX 功能

```go
type PreCompactStatus struct {
    Running   bool
    StartedAt time.Time
    StartTok  int
}

func (a *Agent) PreCompactStatus() PreCompactStatus {
    // 非阻塞 drain done channel 检测完成
    select {
    case <-pc.done:
        return PreCompactStatus{}  // 已完成 → 返回空
    default:
    }
    return PreCompactStatus{Running: true, ...}
}
```

这个方法让 TUI 可以在状态栏显示 "compacting..." 指示器。目前还没被 TUI 消费，但预留好了。

**关键细节：** `select { case <-pc.done: ... default: }` 非阻塞检查——不会阻塞调用方。如果 compact 已完成，返回零值（不显示任何东西），而不是返回 `Running: true` 然后瞬间变 false 导致 UI 闪烁。

**教训：** 设计后台操作时，同步考虑**状态可观测性**。不只是"启动-等待-取消"，还要有"当前在做什么"的查询接口。

### 6. `startedAt` 和 `startTok` — 可观测性字段

我的 `precompactState` 只有 `done`、`cancel`、`err` 三个字段。Opus 加了 `startedAt`、`startTok`、`cancelled`。

- `startedAt` — 日志中可以输出耗时（`time.Since(pc.startedAt).Round(time.Millisecond)`）
- `startTok` — 日志和 UI 可以显示压缩了多少 token
- `cancelled` — 区分"失败"和"被外部取消"，两者都设 `err` 但语义不同

**教训：** 即使初始版本不需要，在状态结构体中预留诊断字段成本很低但价值很高。

### 7. `defer cancel()` 的位置

**我的设计：** 没有显式 defer cancel（依赖 goroutine 结束时 bgCtx 自然超时）
**Opus (L88):** `defer cancel()` 在 `defer close(pc.done)` 之后

```go
go func() {
    defer close(pc.done)   // 后声明，先执行（LIFO）
    defer cancel()          // 先声明，后执行
    // ...
}()
```

等等，LIFO 意味着执行顺序是：先 `close(pc.done)` 再 `cancel()`。这不对吧？

让我再想一下... 不对，Go defer 是 LIFO：
1. 先注册 `close(pc.done)` → 后执行
2. 再注册 `cancel()` → 先执行

所以实际执行顺序是：`cancel()` → `close(pc.done)`。

这个顺序很重要：
- `cancel()` 释放 bgCtx 的资源（context 内部的 timer goroutine）
- `close(pc.done)` 通知等待方"我完成了"
- 先 cancel 再 close，确保等待方收到 done 信号时 ctx 资源已经清理

**教训：** 多个 defer 的顺序不是随意的，要显式推演 LIFO 执行顺序。

---

## 🟢 触发点差异 — defer 顺序

**我的设计：** 在 agent run 之后、`agentDoneMsg` 之前调用 `StartPreCompact()`
```go
// 逻辑顺序（不是 defer）:
runAgentSubmission(...)
m.agent.StartPreCompact()
m.program.Send(agentDoneMsg{})
```

**Opus (submit.go L101-108):** 全部放在 defer 中
```go
defer func() {
    if m.agent != nil {
        m.agent.StartPreCompact()    // 先启动 compact
    }
    if m.program != nil {
        m.program.Send(agentDoneMsg{})  // 再通知 TUI
    }
    cancel()
}()
```

defer LIFO 执行顺序：`cancel()` → `Send(agentDoneMsg)` → `StartPreCompact()`

不对，再次检查——defer 是注册的逆序。整个 defer func 是一个注册，所以 func 内部按顺序执行：先 `StartPreCompact` 再 `Send(agentDoneMsg)`。

这个顺序很巧妙：
- 先启动 pre-compact（不阻塞，瞬间返回）
- 再发 agentDoneMsg 给 TUI（TUI 开始渲染结果）
- pre-compact 在后台默默运行

如果反过来（先 Send 再 StartPreCompact），TUI 收到 doneMsg 时可能开始新一轮操作（比如 pending submission），而 pre-compact 还没启动，存在微小的 race window。

---

## 🟢 CancelPreCompact 的调用点 — 覆盖比我设想的更全

我设想只在 `Clear()` 中调用。Opus 在三个地方调用：
1. `Clear()` — 用户清空对话
2. `SetContextManager()` — context manager 被替换
3. `handleCompactCommand()` — 用户手动 `/compact`

第 3 点特别好——用户手动 `/compact` 时，如果后台有一个 pre-compact 在跑，必须先取消它，否则两个 compact 会竞争写同一个 contextManager。

---

## 总结：从 Opus 学到的 5 个核心原则

1. **局部捕获原则：** 后台 goroutine 通过参数或闭包捕获访问共享状态，永远不通过外层结构的指针间接访问。防止指针被替换后操作错误对象。

2. **取消即断开原则：** 取消操作立即 nil 掉指针 + 标记状态 + 通知 goroutine，三步一起做。不保留"僵尸引用"。

3. **锁内快照原则：** 在锁内一次性捕获 goroutine 需要的所有依赖（接口值、配置快照），unlock 后不再回读共享状态。

4. **幂等消费原则：** 后台操作的结果被后续逻辑消费时，不需要精确区分"成功/失败/取消"，只要后续逻辑自带幂等检查（如 maybeAutoCompact 的 threshold 检查），就可以简化返回值语义。

5. **可观测性预留原则：** 即使初始版本不需要，也给状态结构体加上 `startedAt`、诊断字段和 `Status()` 查询方法。成本极低，调试时价值极高。
