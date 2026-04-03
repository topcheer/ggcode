# Bug: TUI 输入框消失

## 问题描述
在最近的 viewport 滚动功能合并后（commit 64c7584 及后续），TUI 底部的文本输入框（text input）不可见或不可用。用户无法在底部看到输入光标/提示符，导致无法输入新消息。

## 复现步骤
1. 启动 ggcode TUI
2. 进行几轮对话
3. 观察：底部输入区域不可见

## 当前状态
- commit `9b0d4ed` 尝试修复了 viewport 空白行填充问题，但输入框仍然不可见
- `internal/tui/app.go` View() 函数中 `m.input.View()` 确实被调用（line 764），但渲染结果可能在屏幕外或被覆盖

## 可疑点
1. **`textinput` 未设置宽度**：`WindowSize` 消息处理中调用了 `m.viewport.SetSize(msg.Width, viewportHeight)`，但从未调用 `m.input.SetWidth(msg.Width)`。bubbles textinput 默认宽度可能为 0 或不匹配终端宽度。
2. **空白行填充缺失**：当 `totalLines < visibleLines` 时（内容短于可见区域），View 只渲染 `totalLines` 行内容，没有填充空白行到 `visibleLines`，导致输入框位置不正确。
3. **footerLines 计算可能不准确**：help bar 的行数假设为固定值，但实际渲染可能多行。

## 验收标准
1. TUI 启动后，底部始终可见文本输入框和光标
2. 输入框宽度匹配终端宽度
3. 内容少时，输入框仍然在终端底部
4. 内容多时（需要滚动），输入框仍然固定在底部
5. 滚动操作不影响输入框可见性
6. 打字、回车发送功能正常

## 建议修复方向
1. 在 `WindowSize` handler 中添加 `m.input.SetWidth(msg.Width)`
2. 当 `totalLines < visibleLines` 时，在内容和输入框之间填充空白行
3. 确认 `footerLines` 计算与实际渲染一致

## 影响范围
- 文件：`internal/tui/app.go`
- 函数：`View()` 和 `Update()` 中的 `WindowSize` 处理
