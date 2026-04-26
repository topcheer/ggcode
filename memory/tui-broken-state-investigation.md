---
name: TUI 破坏状态调查记录
description: make clean && make 后首次运行 TUI 时鼠标/键盘操作导致界面被破坏的根因调查
type: project
---

## 问题现象

- `make clean && make` 后首次运行 ggcode TUI，有较高概率出现"界面被破坏"
- **启动时 UI 完全正常**（输入框、主内容区域、右边栏、MCP 状态、IM 状态都正常）
- **鼠标/键盘操作后立即出现乱码**（控制序列或输入字符显示在屏幕左下角）
- **Ctrl+C 一次就退出**（正常需要两次：第一次 clear，第二次 quit）
- 所有终端类型都有此问题（WarpTerminal、Apple_Terminal 等）
- 杀掉重启大概率恢复正常

## 排除的假设

1. ~~Alt screen 没进入~~ — UI 启动时完全正常，说明 alt screen 正确进入了
2. ~~tea.ClearScreen() 能修复~~ — 已验证无效
3. ~~bubbletea v2.0.5 bug~~ — 升级到 v2.0.6 后问题依然存在
4. ~~stdout 被替换~~ — 检查了代码，stdout 未被修改
5. ~~IM adapter 干扰~~ — 无论是否绑定 IM 都会出现

## 当前最可能的根因

**bubbletea 的 `readLoop` 在启动早期遇到错误后退出**。

证据链：
- Ctrl+C 一次就退出 → raw mode 的 Ctrl+C 拦截是通过 readLoop 解析 KeyMsg 实现的
- readLoop 退出后 Ctrl+C 会产生真实 SIGINT，bubbletea 的 signal handler 捕获后直接退出
- 鼠标/键盘操作显示乱码 → 输入不被 readLoop 消费，直接在 raw mode 终端中回显
- `make clean && make` 后更容易出现 → 冷启动时 cancelReader 或 StreamEvents 初始化可能有竞态

### bubbletea readLoop 时序

```
Run()
├── initTerminal() → term.MakeRaw(stdin) ← raw mode 生效
├── initInputReader(false)
│   ├── uv.NewCancelReader(p.input) ← 创建 cancelReader
│   ├── uv.NewTerminalReader(cancelReader, term)
│   └── go p.readLoop() ← goroutine 启动
│       └── StreamEvents(ctx, msgs)
│           └── streamData(ctx, readc)
│               └── sendBytes(ctx, readc)
│                   └── cancelReader.Read(buf) ← 如果这里报错，readLoop 退出
├── startRenderer() ← renderer 正常启动
├── Init() + render(model) ← 初始 UI 正常渲染
└── eventLoop() ← 开始处理消息
    └── 不检查 readLoopDone ← readLoop 退出后 eventLoop 不知道
```

### 为什么 readLoop 可能退出

`cancelReader.Read()` 可能返回错误的情况：
- stdin fd 被意外关闭
- cancelReader 内部状态不一致
- 终端 probe 响应导致 Read 异常

### 无法确认的原因

bubbletea 内部代码无法直接添加 debug 日志。readLoop 退出时只通过 `p.errs` channel 发送错误，但 eventLoop 可能没来得及处理。

## 待验证的假设

1. 在 ggcode 中添加 stdin fd 的监控，检查 fd 是否在 Run() 前后被修改
2. 使用 `strace`/`dtrace` 追踪 stdin 的 read 系统调用，确认 readLoop 是否退出
3. 在出问题时用 `stty -a < /dev/ttysxxx` 检查终端 raw mode 状态
4. 检查 macOS Gatekeeper/quarantine 是否在新编译的二进制上触发了额外延迟

## 已做的修改

- bubbletea v2.0.5 → v2.0.6（保留，无风险小版本升级）
- `tea.ClearScreen()` 方案（已回退，无效）
