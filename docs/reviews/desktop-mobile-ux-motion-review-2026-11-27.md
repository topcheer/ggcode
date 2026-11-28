# ggcode Desktop / Mobile 体验·动效·视觉 Review（2026-11-27）

> 只读审查，未改动任何业务代码。三路独立代码审查 + 量化盘点 + 真实界面截图，关键 P0 结论已二次抽查验证。
> 对齐既有资产：`docs/research/desktop-streaming-ux.md`（流式 UX 研究）、`docs/design/webui-redesign-zcode-inspired.md`（token 表与"丝滑感清单"）。本次发现的一个核心事实是：**这两份文档里的结论大部分没有落到桌面端代码里**（`.msg-enter`/`.tool-shimmer`/`.streaming-cursor` 写好了 0 处引用，`useStreamBuffer` 写好了没接入）。

## 0. 范围与方法

| 端 | 代码位置 | 栈 | 规模 |
|---|---|---|---|
| Desktop | `desktop/ggcode-desktop-wails/frontend/src` + Wails 窗口层 | React 19 + Vite + 纯 CSS（无动效库、无 Tailwind） | 16.4k 行，`ChatView.tsx` 3716 行 |
| Mobile | `mobile/flutter/lib` | Flutter 3.50 + Riverpod 3 + Material 3 | 16.3k 行 |

- 量化盘点：transition/keyframes/颜色字面量/CSS 变量/字号/圆角/z-index/内联样式、Animated* 用量/Theme 读取/Colors.*/Haptic/PopScope 等，全部给出 grep 数字。
- 真实界面：桌面壳通过注入 Wails shim 在浏览器中渲染（暗色主页、侧栏、设置页三张截图），移动端在 iOS 模拟器构建安装后截图首屏。
- 标注"需真机确认"的项依赖 WebView/IME/平台行为，代码层面只能证明缺少防护。

## 1. 一句话诊断

两端的问题都不是"缺动画"，而是**缺少统一的动效与视觉规范，且已有的规范没有落地**：

- **桌面**：1016 处内联 `style={{}}` 对 30 处 `className`；45 个 transition 里 36 个各自手写 `0.15s`；两套 `:root` token 互相覆盖、8 个变量用了从未定义；4 组流式动效 CSS 与 `useStreamBuffer` 全部闲置；light 主题多处对比度不达标；缩放功能实际无效。
- **移动**：状态切换动效几乎为零（`AnimatedSize/Switcher/Opacity` 0 处，仅 4 个呼吸循环）；268 处静态 `AppColors.*` 对 11 处 `Theme.of`；light 主题 shell 文本不可见；流式每 chunk 触发两次全量列表重建；输入框单行。

## 2. 桌面端

### 2.1 已经做得好的（保留）

- 语义化颜色 token 且 light/dark 成对（`App.css:1-73`），`color-scheme` 随主题切换让原生控件跟主题。
- 全局 reduced-motion 策略，连内联动画一并关停（`style.css:537-556`）。
- 全局键盘焦点环（`style.css:271-281`）；Toast 语义正确（`role=status aria-live=polite`，`Toast.tsx:62-88`），进场用了全库唯一一条"设计过"的缓动 `cubic-bezier(.16,1,.3,1)` 250ms。
- 窗口层：全平台 frameless，macOS 红绿灯自绘含 hover 字形与 i18n aria-label（`TopDragBar.tsx:100-117`），Windows caption 按钮含原生 `#e81123` 关闭 hover；最小 900×600；窗口状态持久化。
- 消息 `React.memo` 边界有效（`MessageCard`/`MessageContent`，`ChatView.tsx:186,3068`），流式期间对象身份保持（`chatStreamState.ts:69-75`）。
- 自动滚动：rAF 合并、≥80ms 节流、近底阈值、按 tab、未读徽章 + 跳转按钮（`ChatView.tsx:965-992, 2639-2687`）。
- 工具行默认折叠、错误自动展开、结果 200 行截断 + 复制（`:3428-3455`）；长代码块折叠到 420px；推理块完成后自动折叠为 "Thought for Ns"。
- Markdown 细节：链接下划线动画、`<kbd>`、任务列表 `accent-color`、外链标记、打印样式（`style.css:184-231,496-534`）；数字使用 tabular numerals 防抖动。

### 2.2 P0 — 用户可见 bug / 阻断（12 项）

| # | 问题 | 证据 | 用户症状 | 修法 | 量 |
|---|---|---|---|---|---|
| D1 | **IME 组合期 Enter 直接发送、Escape 取消运行** | `ChatView.tsx:1890-1893` Enter→`handleSend()`，`:1988-1991` Escape→`handleCancel()`；全文件 0 处 `isComposing` | 拼音选词按 Enter 把半截拼音发出去；用 Escape 清候选词却把 agent 跑着的任务杀了（需 Wails WebView 真机确认） | `handleKeyDown` 与全局处理器开头：`if (e.nativeEvent?.isComposing \|\| e.keyCode === 229) return` | S |
| D2 | **审批弹窗一弹出，Enter 即"允许"** | `ApprovalDialog.tsx:496-508` 监听 `window` keydown，Enter→`allow`/Shift+Enter→`always_allow`，无焦点检查、无宽限期、无 IME 检查 | 用户正在输入框打字，审批弹出，习惯性按回车 → 批准了 `run_command` | 弹窗挂载即 focus 并做焦点陷阱；仅当 `activeElement` 在弹窗内才响应；打开后 400ms 内忽略按键；跳过 `isComposing` | S |
| D3 | **缩放（⌘+/−）完全无效** | `useZoom.ts:23-24` 只改 `html{font-size}` 与 `--app-zoom`（后者 0 处使用）；`App.css:88` 把 `html,body,#root` 钉死 13px，CSS 中 0 处 rem，442 处内联 px 字号 | 状态栏显示 110%，界面纹丝不动 | 短期：`body{zoom: var(--app-zoom)}`（WKWebView/WebView2 支持）；长期：字号/间距 rem 化 | S/M |
| D4 | **`--text-tertiary` 承载文字处对比度全部不达标** | dark `#484F58`（`App.css:70`）对 bg/card/surface = 2.28/2.09/1.84:1；light `#8C959F`（`:18`）= 3.04/2.71/2.52:1（AA 需 4.5）；用于 10–12px 元数据（`Sidebar.tsx:602,612`、`CommandPalette.tsx:172,205`、`StatusBar.tsx:436`） | 侧栏时间戳/模型、命令面板提示、状态栏几乎看不清 | dark 提到 `#8b949e` 级，light `#59636e`；原值降级为边框/图标 token | S |
| D5 | **light 主题 danger 文字 2.3:1** | `ContextMenu.tsx:90,100,105`、`ShareDialog.tsx:110,186` 硬编码 `#f87171` | 浅色下"删除"菜单项与分享错误提示几乎不可见 | 改 `var(--color-error)`（light 5.36:1，dark 5.65:1） | S |
| D6 | **非 macOS 启动永远先画深色；light 用户每次启动闪深色** | `appearance_other.go:5-7` 非 darwin 一律返回 dark → `main.go:113-115` 背景钉 `#0D1117`；`index.html:11` body 背景也钉死 | Windows/Linux light 用户每次启动白→黑→白闪烁 | `index.html` 内联脚本在首帧前按 `ggcode-theme` + `prefers-color-scheme` 设背景与 `.dark`；非 darwin 默认白色或读系统 (`AppsUseLightTheme`) | S |
| D7 | **Mermaid 图永远不渲染** | `ChatView.tsx:1020-1051` effect 只依赖 `[sessionId]`，且在历史异步加载前运行；`theme:'dark'` 硬编码 | 所有图显示为源码 `<pre>` | 依赖改 `[sessionId, messages]`，`data-done` 守卫，theme 跟随 `.dark` | S |
| D8 | **全局 smooth-scroll 让自动跟随失效并自我关闭** | `style.css:285-287` `div[style*="overflow"]{scroll-behavior:smooth}` 命中消息容器；每次 `scrollTop = scrollHeight`（`:972`）变成动画，`programmaticScrollRef` 被第一帧消费后，后续帧被判定为手动滚动并在 >200px 时关闭跟随（`:2536-2546`） | 长会话加载后到底了但跟随是关的，下一条回复长到视野外；属性子串选择器还强制每次内联样式变更重算样式 | 删除该规则，需要平滑处显式 `scrollTo({behavior:'smooth'})` | S |
| D9 | **用户气泡永远靠左 + hover 时间戳是死的** | `ChatView.tsx:3202` 用户气泡 `alignSelf:'flex-end'`，但父级 `.message-row` 只有 `position:relative`（`style.css:429-431`）；时间戳是 `.message-row` 的**前置兄弟**（`:2599-2603`），而 hover 规则选的是后代（`:444`） | 用户消息右侧永远留 20% 空白；hover 时间戳永不出现；同时用户/助手消息又各自常显时间戳，工具/系统/推理没有 | `.message-row{display:flex;flex-direction:column}`，时间戳移入行内，删重复时间戳 | S |
| D10 | **阅读中每 10 秒被拉回底部** | `ChatView.tsx:1007-1018` 流式期间每 2s 检查"关闭跟随且上次手动滚动≥10s"则重新开启并跳底 | 正在翻看前文被反复打断 | 删除定时器；靠未读药丸，用户滚到底部时才重新开启跟随 | S |
| D11 | **模型/工具/LAN 对端输出的 HTML 未消毒** | `safeMarkdown`（`:43-50`）直出 `dangerouslySetInnerHTML`（`:206,209,3239,3154`）；`LanChatView.tsx:10-17,546`；`rehype-sanitize` 已安装但 0 处引用 | 注入的 `<img onerror>` 在可访问 `window.go.main.App` 的 WebView 中执行 | `safeMarkdown`/`renderMarkdown` 加 DOMPurify，或聊天改走已安装的 react-markdown + rehype-raw + rehype-sanitize | M |
| D12 | **图片附件跨会话泄漏** | `pastedImages`（`:1442`）只在发送时清空（`:1573`），`sessionId` effect（`:818-860`）不重置 | 会话 A 贴的图随会话 B 的第一条消息发出 | `sessionId` effect 中 `setPastedImages([])`；若想保留则按草稿键持久化 | S |

### 2.3 P1 — 动效与一致性缺口（桌面）

| # | 问题 | 证据 | 修法 |
|---|---|---|---|
| D13 | **流式消息每个 delta 全量重解析 markdown + 替换 innerHTML + 重高亮全部代码块 + 重挂监听** | `:186-219`；`enhanceCodeBlocks` 每次重建后对所有块 `highlightElement`（`:64-71`），未知语言走自动检测（`:65-69`）；`useStreamBuffer` 0 处使用；Go 侧每个 delta 一个事件（`wailskit/chat.go:2087-2090`） | 见 §2.5 流式管线 |
| D14 | 所有弹窗/菜单/侧栏**零进场动效、零退场动效** | About/Share/Pairing/AskUser/Approval/Shortcuts/CommandPalette/ContextMenu 直接 mount；Toast 删除是 unmount（`Layout.tsx:112-114`），`slice(-5)` + `column-reverse` 让旧 toast 跳位；侧栏/右栏条件挂载无过渡（`Layout.tsx:489-531`），分组折叠瞬时（`Sidebar.tsx:503`） | `.dlg-enter/.dlg-exit`（200ms/150ms）+ backdrop fadeIn；toast 保留 150ms `exiting`；侧栏用 `margin-left`/grid 过渡替代条件挂载；折叠用 `grid-template-rows:0fr→1fr` |
| D15 | 布局属性动画 + `transition: all` | toast 进度条动画 `width`（`style.css:413-416`）；状态栏 context 条 `width 0.3s`（`StatusBar.tsx:202`）；`transition:'all'` ×6 | 改 `transform: scaleX()`；`all` 改显式属性列表 |
| D16 | 焦点环会改变元素形状；输入框无可见焦点 | `*:focus-visible{… border-radius:2px}`（`style.css:274`）不是 outline 属性，圆角按钮被聚焦时变 2px 方角；22 处输入框 `outline:'none'` 无 `:focus-within` | 焦点规则去掉 `border-radius`；加 `.field:focus-within{border-color:primary; box-shadow:0 0 0 2px …}` |
| D17 | 不可键盘聚焦的交互元素 | 状态栏 context 药丸 `div onClick`（`StatusBar.tsx:185-191`）；AskUser 单选/多选行 `div onClick` 无 role/tabIndex（`AskUserDialog.tsx:191-225`）；CommandPalette 行无 listbox/option 语义（`:183-210`）；ContextMenu 无 menu/menuitem | 改 `<button>` 或补 ARIA + `aria-activedescendant` |
| D18 | 弹窗惯例缺失 | AboutDialog 无 `role=dialog aria-modal`（`Dialogs.tsx:47`）；Pairing 无 Esc/遮罩关闭（`PairingCodeDialog.tsx:23-28`）；删除会话用浏览器 `window.confirm`（`Sidebar.tsx:174`、`CronManagement.tsx:47`）——在无边框桌面窗口里弹 web 原生框 | 共享 `useDialog(close)` 钩子（Esc + 遮罩 + 焦点回归）；`confirm` 改内联确认 |
| D19 | Windows 字形与快捷键标签 | Windows caption 用文字字形 `─ □ ✕`，`#a0a0a0` 对 `#F6F8FA` = 2.46:1（`TopDragBar.tsx:92,127-143`）；全平台硬编码 `⌘`；**NavRail 把 Cron 标成 ⌘7、LAN Chat 也标 ⌘7**（`NavRail.tsx:25,111`），而 Layout 实际 `⌘7→lanchat, ⌘8→cron`（`Layout.tsx:370`）——截图快照里已实际看到两个 "(⌘7)" | Segoe MDL2 字形或 lucide 图标；`modKey = isMac ? '⌘' : 'Ctrl+'`；修正 ⌘8 |
| D20 | 仅暗色可用的字面量进了共享面 | `rgba(255,255,255,.1)` hover（`TopDragBar.tsx:124`、`ShareDialog.tsx:150`）；`#ef4444/#f59e0b/#22c55e` 绕过 token（`StatusBar.tsx:182`、`Onboarding.tsx:11-12`、`NavRail.tsx:139`、`ChatView.tsx:3478-3504`、`ApprovalDialog.tsx` 16 处） | 全部换语义 token |
| D21 | 滚动条几乎不可见 | 生效的是 `App.css:105-120`（覆盖了 `style.css:240-256`），thumb 为 `--color-surface` 对 bg（light ≈1.2:1，dark ≈1.5:1），且继承了透明边框 | thumb 用 `color-mix(text-tertiary 55%, transparent)` |
| D22 | light 模式 markdown 链接 hover 2.54:1；dark 链接文本 4.08:1 | `style.css:208,217` | 链接文本用 `--color-info`（dark 7.49:1 / light 5.19:1） |
| D23 | ⌘⇧T 切主题绕过 React 状态 | `Layout.tsx:345-349` 直接改 `.dark` 与 localStorage，`useTheme` 状态过期 → ⌘K"Toggle theme"变 no-op，`auto` 模式静默丢失 | 调用 hook 的 `toggleTheme()` |
| D24 | 滚动位置保存了但从不恢复 | `scrollRestorePendingRef` 只被初始化/置空（`:887,919`），`:857` 加载时删掉了保存的键 | 加载前置 pending，`useLayoutEffect` 按 `messages.length` 应用，删 `:857` |
| D25 | 旧历史不可达 | 只保留最近 50 条（`:850-851`），无"加载更早" | `historyOffset` + 顶部"加载 50 条"行，保持 `scrollTop` |
| D26 | 搜索每 tick 重算并抢滚动 | `:1896-1903` 每次 `messages` 变化对所有消息小写化；`:1997-2007` 每 tick `scrollIntoView`；匹配只整条着色 | 匹配 memo 到 `[messages.length, tailLength, query]`；只在 `searchMatchIdx/query` 变化时滚动；`<mark>` 包裹子串 |
| D27 | Cancel 无本地兜底 | `:1796-1800` 只调 `App.CancelMessage()`；`isStreaming/runActiveRef` 全靠 `run_done` 事件复位（`:1201-1208`） | 乐观复位 + 系统行"已取消"，忽略迟到的 `run_done` |
| D28 | 流式光标脱离文本 | `:3402-3410` 光标是块级 `MessageContent` 之后的兄弟节点 | 把光标插进最后一个 markdown 段（复用闲置的 `.streaming-cursor`） |
| D29 | 流式中 Send 与 Stop 并列、无队列提示 | `:2846-2865` | 流式中改按钮语义（"立即发送 → 引导运行中的 agent"）或显示"已排队"药丸 |
| D30 | diff 渲染不一致 | `enhanceDiffBlocks` 覆盖 hljs 输出（`:164-184`）；`DiffRender.tsx` 行号永远为空（`:10-27`）且聊天不用它；`ApprovalDialog.computeDiff` O(m·n) + 循环内 filter，每次打开算两遍 | 删 `enhanceDiffBlocks` 让 hljs diff 语法着色 + CSS 计数器行号；修 `parseDiff`；diff 只算一次；超 3000 行给"过大"提示 |
| D31 | 无正文行长限制 | 助手 `maxWidth:85%`（`:3342`），宽窗口下行长 170+ 字符 | `max-width: min(85%, 80ch)`，工具行全宽 |
| D32 | 43 行硬编码英文（含 DOM 拼出的 `Copy`/`✓ Copied`/`Expand`） | `:86,99,125,135,2107,2182…` | `t()` 传入 `enhanceCodeBlocks` 或改 React `CodeBlock` 组件 |

### 2.3.1 P2 — 细节项（桌面，全部 S 量）

| # | 问题 | 证据 | 修法 |
|---|---|---|---|
| D33 | textarea 只在键入时自增；欢迎卡/拖放/粘贴按钮设置 `input` 时不调整高度 | `:2827-2832`（onChange 内 resize）、`:2568-2571`、`:937-945`、`:1741,1770` | `useLayoutEffect(() => {el.style.height='auto'; el.style.height=Math.min(el.scrollHeight,200)+'px'}, [input])` |
| D34 | 非图片文件拖放静默忽略，而遮罩写着 "Drop images to attach" | `:1636-1637`、`:2107` | 非图片文件插入 `` `${path}` `` 引用（同 `:939` 格式）并 toast |
| D35 | `visibilitychange` 监听只加不删；ChatView 按工作区重挂载 → 监听累积 | `:666-673`、`Layout.tsx:515` | 处理器提为具名常量并在 cleanup 中移除 |
| D36 | 每次 render 为每条消息 new 两个 `Date`（500 条上限 ≈ 每 tick 1000 次） | `:2574-2580` | 在 `materializeHistory`/`appendAndTrim` 时算一次 `dayKey` |
| D37 | 平滑 `scrollTo/scrollIntoView` 不尊重 reduced motion | `:1974,1980,2005,2646` | `prefersReducedMotion()` 返回 `'auto'` |
| D38 | LAN 消息 key 回退 index；每条新消息强制 `scrollIntoView`，用户上翻时也被拉走 | `LanChatView.tsx:522`、`:188-189,294-295,341-342` | 接收时分配稳定 id；复用 `isNearBottom` |
| D39 | hljs 主题 CSS 只在 FileBrowser 引入，ChatView 依赖其恰好是全局 | `FileBrowser.tsx:14-15`、`ChatView.tsx:6` | 两处 import 移到 `main.tsx`/`App.tsx` |
| D40 | 图标尺寸漂移：lucide 12 种尺寸；toast `X` 13、侧栏 `Plus` 15、状态栏 11/12 | `Toast.tsx:104`、`Sidebar.tsx:372`、`StatusBar.tsx:351` | 统一 12/16/20（13px 文本内允许 14） |
| D41 | 自带 `nunito` 字体文件从未引用；`ShareDialog` 的 `pulse` keyframe 定义在 ChatView | `assets/fonts/`、`ShareDialog.tsx:147-161`、`ChatView.tsx:2883` | 删除未用字体；`spin/pulse/fadeIn` 收进 App.css，删 3 处内联 `<style>` 副本 |
| D42 | macOS 红绿灯继承 `--wails-draggable`，按下拖动会变成拖窗口；双击标题栏缩放未接 | `TopDragBar.tsx:59,107-115`、`:49-60` | 按钮设 `'--wails-draggable':'no-drag'`；接 `WindowToggleMaximise` |
| D43 | token 级小错：`--topbar-height:48px` 未用而实际 36px；Skeleton 默认圆角字面量 4 | `TopDragBar.tsx:50`、`Skeleton.tsx:10` | 对齐 token |

### 2.4 视觉系统层面的结构问题（根因）

| 维度 | 现状（量化） | 结论 |
|---|---|---|
| 样式组织 | **1016 处 `style={{}}` vs 30 处 `className`**；hover 靠 JS：25 `onMouseEnter`/23 `onMouseLeave`/44 处 `currentTarget.style.` 写入，对 9 条 `:hover` 规则 | CSS 层无法演进：没有类名就没有 hover/focus/动画/媒体查询可言；动效只能写成内联 transition，这就是 36 个 `0.15s` 各写一遍的来源 |
| token | `style.css:17-38` 的 20 个变量被 `App.css:1-50` 按导入顺序**完全覆盖**；**8 个变量用了从未定义**（`--color-hover`、`--color-nav-hover`、`--accent`、`--bg-secondary/tertiary`、`--border-color`、`--color-danger`、`--color-text-tertiary`）；3 个定义了没用 | 两套 token 并存 → 实际生效值与开发者以为的不一致；`Toast.tsx:98` 的关闭按钮背景因 `--color-hover` 未定义而透明 |
| 死代码 | `style.css:289-379`（91 行流式动效：`.msg-enter/.tool-shimmer/.streaming-cursor/.reasoning-active` + 4 个 keyframes）**0 处引用**；`spin` 定义 3 次；skeleton shimmer 定义 2 次且时长不同（1.5s vs 1.4s）；`color-scheme` 声明 3 次 | 研究文档说"本周期已实现"，实际只是写进了 CSS |
| 动效 | 45 个 transition：`0.15s`×36、`0.2s`×8、`0.1s`×3、`0.3s`×3、`0.12s`×2、`0.08s`×2；缓动显式 `ease`×16，其余默认，仅 1 条设计过的 `cubic-bezier`；**0 个 motion token** | 没有节奏：微交互/标准/进场/退场/强调没有分级，退场动效普遍缺失 |
| 排版 | 15 种 px 字号（8–32px，含 8px/9px），442 处内联 `fontSize`，`--font-size-*` 仅 7 处使用；`html` 基准 13px 与 `--font-size-base:15px` 矛盾；**字体栈无 CJK 回退、无 Segoe UI**（`App.css:34-35`）；Inter/JetBrains Mono 从 **Google Fonts CDN** 加载（`index.html:7-9`）——离线首启/受限网络回退 Arial，中文回退系统默认；自带的 `nunito-v16-latin-regular.woff2` 从未引用 | 13 个 locale 却没有中文字体方案；桌面应用不该依赖 CDN 字体 |
| 图标 | lucide 12 种尺寸（10–48）；17 个手绘内联 `<svg>`；35 个 unicode 字形"图标"（`⌘ ↑ ↓ ↵ Σ ● ─ □ ✕ ▸`）；**欢迎页四张建议卡用 emoji**（🔍📝🧪🐛，截图中可见） | 同屏三套图标语言；emoji 跨平台不一致且显廉价 |
| 圆角/阴影/z-index | token 4/6/8/12 vs TSX 84 处字面量（3/4/6/8/9/14/999/50%）；16 种 shadow 字符串；z-index 11 个值无刻度（10…10000），CommandPalette 遮罩 z 99/100 低于状态栏弹层 z 1000 | 需要一套 `--radius-*/--shadow-*/--z-*` |
| 品牌 | NavRail/About 用 PNG 图标，标题栏/欢迎页用渐变 "G" 字标（`#8b5cf6`） | 两套品牌标记 |
| 状态重复 | 顶部一排 `Workspace / AUTO / Ready / Effort: auto` 芯片，底部状态栏又有 `auto / Ready / ⚡`（截图可见）；底部 `ctx ▭▭ 0%  auto  in 0  out 0  ⊙100%  ⊕` 约 10px、标签晦涩 | 同一信息两处展示、字号过小、用词不面向用户 |

### 2.5 流式渲染管线（针对现状的改法）

现状：Go 每 delta 发一个事件 → ChatView 每 delta 一次 `setMessages` → 流式消息 `useMemo` 全文重解析 → `innerHTML` 全量替换 → 全部代码块重高亮 + 重挂 4–5 个监听。另有每秒 `agentElapsed` 与每次 `usage_update` 各触发一次整列表 reconcile。

1. **事件边界合批**：`EventsOn('chat:stream')` 中 `text/reasoning` 走 `useStreamBuffer.buffer()`，其他事件前先 `flush()` 保证顺序；把纯 rAF 改为 ≥30ms 最小间隔（120Hz 屏不翻倍）；`done/run_done/error/visibilitychange` 时 flush。
2. **流式消息拆成"已稳定前缀 + 活动尾部"**：在最后一个围栏外的 `\n\n` 处切分；前缀走现有 memo 的 HTML 路径（不再变化，高亮只跑一次），尾部 `pre-wrap` 纯文本 + 光标；`streaming` 结束时合并。每 delta 的 markdown/innerHTML/高亮风暴随之消失，未闭合围栏/表格的重排闪烁也一并消失。
3. **停止语言自动检测**：仅 `lang && hljs.getLanguage(lang)` 时高亮；>2000 行跳过。
4. **把每秒/每 usage 的渲染隔离出列表**：抽 `<MessageList>` 按 props memo，`agentElapsed` 计时器移入指示器组件。
5. 虚拟化不是第一杠杆（历史 50 + 在线 ≤500）；与"加载更早"一起上 `react-window VariableSizeList`（已是依赖，0 处使用）。

## 3. 移动端

### 3.1 已经做得好的（保留）

- 流式跟随守卫：距底 ≤120px 才跟随，历史回填强制跟随，跳转按钮只在阈值翻转时 `setState`（`chat_screen.dart:51-110,213-220`）。
- 工具结果默认折叠带 2 行/120 字预览（`:1436-1457,1500-1516`）；代码块 >15 行自动折叠 + 复制（`message_bubble.dart:691-793`）；推理完成后自动折叠（`chat_provider.dart:862-881`）。
- 审批/提问的双击与死隧道守卫（`approval_sheet.dart:103-123`、`ask_user_screen.dart:289-301`）；提问页下滑取消（`main.dart:190-201`）；拖动收键盘。
- 连接状态可视化完整：按状态的 AppBar 图标、分阶段同步遮罩、中继同步横幅、状态芯片。
- 六套持久化调色板（含 OLED/Nord）；对话框/底部Sheet/卡片共用 `AppRadii`。
- 图片选图下采样 1024px/q70；缩略图损坏守卫；文件卡正确使用 `Theme.of`。
- 宽度 ≥600 双栏会话栏；iPad 多任务允许；ProMotion 已开；缓存落盘 350ms 去抖（做得对）。

### 3.2 P0

| # | 问题 | 证据 | 症状 | 修法 | 量 |
|---|---|---|---|---|---|
| M1 | **light 主题 shell 输出文本不可见** | `message_bubble.dart:861-865` `color: foreground ?? const Color(0xFFE5E7EB)` 应用到每个 span，底色 `surfaceElevated` 在 light 为 `#EAECF2`（`app_theme.dart:137`）→ 对比度 ≈1.1:1 | 浅色模式下命令输出一片空白 | 默认色改 `AppColors.textPrimary`（把基础色传入 `_AnsiState`） | S |
| M2 | **扫码页按系统返回直接退出 App** | `connect_screen.dart:191-193` 用 bool 切换 `_showScanner` 而非路由；ConnectScreen 是 `home`（`main.dart:296`）；全库 0 处 `PopScope` | 扫码时按返回手势 → App 退出 | `PopScope(canPop: !_showScanner, …)` 或改 `MaterialPageRoute`（同 `chat_screen.dart:671-673`） | S |
| M3 | **流式每 chunk 两次状态写入 + 两次全量列表重建** | `chat_provider.dart:394-407` 每 chunk 重建整个列表 + `lastIndexWhere` 扫描（`:862-881`），0 处 Timer/节流；`main.dart:241-248` 每 chunk 触发 `persistLiveProjection` → `captureLiveProjection` 三次 map 拷贝 + `workspaceCacheProvider` 状态写入（`workspace_cache.dart:1878-1939`）；`displayedMessagesProvider` 同时 watch 两者；ChatScreen 单个 `build` watch 8 个 provider（`:168-175`，0 处 `select`）；`ListView.builder` 行无 key/RepaintBoundary/findChildIndexCallback（`:253-266`）；流式气泡每次重跑正则分段 + `MarkdownBody` 全文重解析 | 长回答掉帧发热，120Hz 更明显 | (a) `ChatNotifier` 16–33ms Timer 合并 chunk；(b) `persistLiveProjection` 去抖到与落盘相同的 350ms；(c) 流式尾部走独立 Provider + 最后一行的 `Consumer`；(d) 行 `key: ValueKey(msg.id)` + `findChildIndexCallback` + `RepaintBoundary` | M |

### 3.3 P1

| # | 问题 | 证据 | 修法 |
|---|---|---|---|
| M4 | **用户气泡文字对比度不达标** | 渐变 `accentSoft→accent` 上用 `textPrimary`（`message_bubble.dart:99-107,165`）：Midnight 2.25:1 / 3.06:1，Light 2.85:1 / 3.22:1；ask_user 提交按钮同对（`ask_user_screen.dart:121-141`） | 增加调色板角色 `onAccent`（浅蓝上用深字，饱和蓝上用白字） |
| M5 | **零状态切换动效，只有 4 个呼吸循环** | `AnimatedContainer/Switcher/Opacity/Size/CrossFade` 0/0/0/0/0；显式动画仅 4 个 `AnimationController`（1400/1400/1200/2800ms 全是 repeat）；瞬时切换：工具结果展开（`chat_screen.dart:1457`）、代码展开（`message_bubble.dart:776`）、推理（`:577`）、子 agent 卡（`subagent_panel.dart:130`）、工作区分组（`session_rail.dart:201`）、跳转 FAB `if`（`:278`）、横幅（`:229-246`）、同步遮罩（`:309-314`）、审批卡（`:270`）、发送↔停止（`input_bar.dart:237-255`）、Connect→Chat 整体替换（`main.dart:292-299`）、扫码切换（`connect_screen.dart:191`） | 展开/折叠 `AnimatedSize(220ms, fastOutSlowIn)`；发送/停止 `AnimatedSwitcher(150ms)`；FAB/横幅 `AnimatedOpacity+AnimatedScale(200ms)`；Connect→Chat fade-through |
| M6 | **呼吸发光让整张卡每帧重建** | `chat_screen.dart:940-941` `AnimatedBuilder` 不传 `child:`，整个工具卡（含 `_ToolResultCard`）以 60–120fps 重建；`connect_screen.dart:302-345,749-810` 同样；`input_bar.dart:181-207` 是正确示范 | 静态子树放 `child:`，只动画 `DecoratedBox` |
| M7 | **输入框单行** | `input_bar.dart:208-233` 无 `maxLines/minLines`，`onSubmitted: _send`；ask_user 自由输入固定 `maxLines:3` | `minLines:1,maxLines:6,keyboardType:multiline`；ask_user `minLines:2,maxLines:6` |
| M8 | **忽略 reduced motion** | 0 处 `disableAnimations`/`withReducedMotion`；循环永不暂停 | `MediaQuery.withReducedMotion` → 跳过 `repeat()`，时长 0，`jumpTo` 代替 `animateTo` |
| M9 | **缩略图每次 build 重新解码** | `message_bubble.dart:127-156`、`input_bar.dart:142-147` 在 `build` 里 `base64Decode`；`MemoryImage` 按 bytes 身份比较 → 每次 cache miss；InputBar 每键重建（`:231`） | 图到达时解码一次保存 `Uint8List`，`cacheWidth:240`，`gaplessPlayback:true` |
| M10 | **展开/折叠状态按位置而非按消息** | 行无 key（`chat_screen.dart:259-265`），`_ToolResultCardState._expanded`、`_CodeBlockWidgetState._collapsed`、`_ReasoningBubble` 持有状态 → 切会话后 Flutter 按索引复用 State | 行 `ValueKey(msg.id)` |
| M11 | **36 处 `Colors.*` 绕过调色板** | `approval_sheet.dart:28-87`、`session_rail.dart:131-286`（blueAccent/greenAccent/orangeAccent ×10）、`chat_screen.dart:430,558-559,817,834-838`…；`Colors.green` 对 light 背景 2.6:1 | 映射到 `AppColors.success/warning/danger/accent` |
| M12 | **静态 `AppColors` 架构阻断 const 与主题响应** | 268 处 `AppColors.*` 对 11 处 Theme 读取；`watchAppTheme(ref)` 散落 7 个 build；结果 TextStyle 4/105 const、Padding 0/25、Container 0/55 | `ThemeExtension<AppPalette>` 放进 `buildAppTheme()`，经 `Theme.of(context).extension` 读取（`AnimatedTheme` 自动过渡，const 友好） |
| M13 | **裸 `ColorScheme` 只设 8 个角色** | `app_theme.dart:260-270`；SDK 回退 `surfaceContainer*→surface`、`onSurfaceVariant→onSurface`、`outline→onBackground` → 文件卡无层次、描边与正文同重 | `ColorScheme.fromSeed(seedColor: accent, brightness).copyWith(...)` 补全角色 |
| M14 | **排版/圆角/等宽字体涣散** | 11 种内联字号（10–28，含 10.5）共 86 处 vs `TextTheme` 仅 3 个样式；18 种圆角（气泡 22/22/18/18/20）；无 `fontFamily`/`fonts:`；11 处 `fontFamily:'monospace'`（iOS 无此族名 → 代码可能变比例字体，需真机确认）；`google_fonts`/`animations`/`flutter_highlight`/`highlight` 四个依赖**0 处 import** | 6 级字阶 + 4 个圆角 token；捆绑 OFL 等宽字体或 `fontFamilyFallback:['RobotoMono','Menlo']`；接线或删除死依赖 |
| M15 | **每行重阴影，暗色下不可见但照付成本** | `AppShadows.panel` blur 24/offset 10 用了 11 处 | 暗色调色板 `shadows: []`，light 用 blur 12/offset 4 |
| M16 | **平板布局只看宽度** | `session_rail.dart:15-18` `width >= 600` → 手机横屏（844×390）也上 300px 侧栏，消息区只剩 ~190px | `width >= 600 && height >= 600`（或 `shortestSide >= 600`） |
| M17 | **启动图与默认主题矛盾** | iOS LaunchScreen 背景纯白，App 默认 Midnight `#0B1020` → 每次冷启动白→深闪；Android `windowBackground` 黑色（light 用户反向闪） | storyboard 改深色；`values-v31` 设 `windowSplashScreenBackground` |
| M18 | **状态栏样式从未设置** | 0 处 `SystemUiOverlayStyle`/`AnnotatedRegion`；ConnectScreen 无 AppBar；Info.plist 无 `UIStatusBarStyle` | 每屏 `AnnotatedRegion<SystemUiOverlayStyle>` |
| M19 | **触觉反馈只有发送 1 处** | `input_bar.dart:297`；审批/拒绝/always_allow、选择、中断、复制、关 tab 均无 | 审批 `selectionClick`，中断 `mediumImpact`，失败 `notificationError` |
| M20 | **可访问性** | 0 处 `Semantics(`；9 个 tooltip 中 7 个硬编码英文；无 `textScaler` 钳制却固定 `Tab(height:36)`、两行标题塞 56dp 工具栏、7 个 10–10.5px 标签；<44pt 目标：tab 关闭、缩略图删除、代码复制 | `IconButton(constraints: minWidth/Height 44)`；`withClampedTextScaling(maxScaleFactor:1.3)`；补 `Semantics` |
| M21 | i18n 非响应式 | `t()` 读全局 map（`app_localizations.dart:98-106`），切语言只 `setState` 了 ChatScreen，`const StatusBar()` 保持旧语言 | 翻译改 `Notifier` 并 `ref.watch` |

### 3.4 P2

- 三种"运行中"指示同时出现（发光边框 + spinner + Working 徽章，`chat_screen.dart:940-977,1057-1087`）。
- `TabController` 在 `build` 里销毁重建（`:204 → :116-157`），agent 列表一变指示器就重置。
- 气泡无时间戳/头像/分组（`message_bubble.dart` 0 处 `message.time`）；空会话无空状态；无下拉刷新/shimmer。
- 会话栏对每个展开的工作区急切构建全部会话（`session_rail.dart:44-57,204-298`，非 builder 列表）。
- 13 个文件 import 已弃用的 `package:flutter/material.dart`；`WorkspaceGroup` 缺 `key` 参数。

## 4. 统一动效规范（两端共用语义，一次定义）

| 层级 | 时长 | 缓动 | 用于 |
|---|---|---|---|
| micro | 100–120ms | standard `cubic-bezier(.2,0,0,1)` | hover / 按压（scale .98）/ 颜色 / 发送↔停止 / 复制打勾 |
| base | 150–180ms | standard | 大多数状态切换、展开/折叠高度、侧栏 hover |
| enter | 200–250ms | decelerate `cubic-bezier(.16,1,.3,1)` | 弹窗、Sheet、侧栏、命令面板、FAB、横幅（fade + 6–16px 位移 / scale .985→1） |
| exit | 120–150ms | accelerate `cubic-bezier(.4,0,1,1)` | 一切退场（必须短于进场） |
| emphasis | 300ms | linear / standard | 进度条（`transform: scaleX`）、大面切换、Connect→Chat fade-through |
| loop | 1.2–1.6s（状态脉冲）/ 1.4s（shimmer） | ease-in-out | 只动 `opacity`；不可见时暂停；reduced motion 下关闭 |

- **允许动画的属性**：`opacity`、`transform`、`color/background-color/border-color/box-shadow`；禁止 `width/height/top/left/margin`（用 `scaleX/translate` 或 `grid-template-rows: 0fr→1fr`；Flutter 用 `AnimatedSize`）。
- **reduced motion**：桌面保留现有全局开关并补 `scroll-behavior:auto`；移动端新增 `MediaQuery.disableAnimationsOf` 分支（循环关闭、时长 0、`jumpTo`）。
- **触觉（移动）**：发送 `lightImpact`；审批/选择/tab `selectionClick`；中断 `mediumImpact`；失败 `notificationError`。
- **落地为 token**：桌面在 `App.css` 增加 `--dur-micro/base/enter/exit/emphasis`、`--ease-standard/decelerate/accelerate`、`--shadow-sm/md/lg`、`--z-popover/menu/toast`、`--text-xs/sm/base/md/lg/xl`；移动端新增 `abstract final class AppMotion { static const micro = Duration(milliseconds: 110); … }` 与 `AppPalette` ThemeExtension。

## 5. 整改路线图

**阶段 0 — 止血（1–2 天，全部 S 量，不改架构）**
桌面：D1 IME 守卫、D2 审批焦点门 + 400ms 宽限、D3 `body{zoom}`、D4/D5 对比度 token、D7 Mermaid 依赖、D8 删全局 smooth-scroll、D9 气泡对齐 + 时间戳、D10 删 10s 拉回、D12 清图片、D19 ⌘7/⌘8、D23 主题切换、D27 Cancel 兜底。
移动：M1 ANSI 默认色、M2 `PopScope`、M6 三处 `child:`、M4 `onAccent`、M5 的发送/停止 `AnimatedSwitcher`、M7 多行输入、M10 行 key、M9 缩略图缓存、M11 `Colors.*`→token、M16 横屏判定、M17 启动图。
验收：两端 light/dark 目检无不可读文字；IME 不再误发；长会话跟随不再失效；扫码页返回不退出。

**阶段 1 — 丝滑（约 1 周）**
桌面：统一 motion/字阶/阴影/z-index token，删死 CSS 与重复 `:root`，补 8 个缺失变量；弹窗/菜单/Toast 进退场；侧栏/右栏/折叠过渡；进度条 `scaleX`；焦点环与 `.field:focus-within`；流式管线 §2.5 的 1–4 步；字体自托管 + CJK/Segoe 回退；emoji → lucide；状态栏信息去重与用语；"加载更早"与滚动恢复；diff 修复。
移动：`AppMotion` + `AppPalette` ThemeExtension；`AnimatedSize` 五处展开折叠；FAB/横幅/遮罩过渡；Connect→Chat fade-through；chunk 合并 + 投影去抖 + 尾部独立 Provider；`ColorScheme.fromSeed`；字阶/圆角 token + 等宽字体捆绑；触觉；状态栏样式；reduced motion；Semantics 与 44pt。
验收：流式 60fps（120Hz 设备不掉帧）；所有状态切换有进有出；reduced motion 下零循环动画；对比度抽检 ≥4.5:1。

**阶段 2 — 系统化（2–4 周）**
桌面：`style={{}}` 迁移到类名/CSS 层（按组件分 PR），hover 从 JS 回到 CSS；消息列表虚拟化 + 历史分页；markdown 管线统一到 react-markdown（含消毒）；`useDialog` 共享钩子；LAN Chat 对齐聊天规范。
移动：会话栏惰性构建 + 分组动效；气泡时间戳/分组/空状态；i18n 响应式；平板/横屏布局策略；死依赖清理；`material.dart` 弃用迁移。

## 7. 实施状态（v1.3.297）

阶段 0 全部完成；阶段 1 中不依赖"内联样式迁移到类名"的项目已完成。未做的项目保留在上表，留给阶段 2。

| 范围 | 已落地 | 未落地（阶段 2） |
|---|---|---|
| 桌面 P0 | D1–D12 全部 | — |
| 桌面 P1 | D13（合批 ≥30ms + 高亮缓存 + 关闭自动检测，未做"稳定前缀/活动尾部"拆分）、D14、D15（scaleX）、D16、D17（状态栏药丸按钮、AskUser 行 ARIA）、D18（About 对话框 role）、D19、D20（ContextMenu/ShareDialog danger）、D21、D22、D23、D24、D26、D27、D28、D32（代码块文案 i18n） | D25 历史分页、D30 diff 修复、D31 行长限制、D18 的 `useDialog` 共享钩子、CommandPalette/ContextMenu 完整 ARIA、Windows 字形、`style={{}}` 迁移 |
| 移动 P0 | M1、M2、M3（chunk 合并 16ms + 投影去抖 350ms + 行 RepaintBoundary；"尾部独立 Provider"未做） | — |
| 移动 P1 | M4、M5（AnimatedSize 五处 + 发送/停止 AnimatedSwitcher + FAB/审批/同步遮罩过渡 + Connect→Chat fade-through）、M6（工具卡 + logo 的 `child:`）、M7、M8、M9、M10、M11（chat_screen/approval_sheet/session_rail/subagent_panel）、M13、M16、M17（iOS 启动图）、M18、M19、M20 部分（tab 关闭/缩略图删除/复制目标 ≥32–40px、Semantics） | M12 ThemeExtension 迁移、M14 字阶/圆角 token 与等宽字体捆绑、M15（阴影已按亮度为空）、M21 i18n 响应式 |

验证：桌面前端 vitest 156/156 + tsc 0 错 + vite build；wails app `go vet`/三平台交叉编译/测试通过；Flutter analyze 0 error/warning，`flutter test` 145/145（新增 `chat_stream_coalesce_test.dart` 钉住合并语义）。

## 8. 追加：用 idb-first `mobile_device` 从零实测时发现并修复的缺陷（2026-11-28）

模拟器由 `mobile_device` 工具（idb 驱动）真实操控，每一步经 `ggcode -p` 的真实工具调用路径，截图在 `/tmp/gg-probe/D0–D21`。

| # | 发现 | 根因 | 修复 | 验证 |
|---|---|---|---|---|
| 1 | idb 自动安装失败（`Refusing to load formula meta/fb/idb-companion from untrusted tap`） | Homebrew 7 拒绝加载未信任 tap 的 formula；旧安装链同步执行还会撞上 180s 工具超时并把失败永久缓存 | 安装器改为分离进程异步执行（不占工具超时），只信任 idb 维护方 tap（`meta/fb`/`facebook/fb`）后重试；客户端装进 `~/.ggcode/idb` venv；失败/进行中原因写入结果 | 卸载 idb 后从零安装三次迭代，最终 `rc=0`（companion 1.7.8 + fb-idb） |
| 2 | `type` 输入 357 字符的 wss URL 变成 `wss：//gateway。ggcode。dev…恶意J一般29天…` | `idb ui text` 走 HID 键盘事件，经模拟器的中文拼音输入法重组 | 改为剪贴板粘贴：写 pasteboard 并 `simctl pbpaste` 读回验证（设备剪贴板是宿主的 ~0.8s 懒同步镜像，`simctl pbcopy` 在本机是 no-op）→ 等待字段 `IsEditing` 焦点 → 空字段长按取编辑菜单 "Paste"（真实用户手势，不触发跨 app 粘贴确认）；非空字段先 Cmd+V + 确认气泡，再退回菜单，菜单含 Cut/Copy 时放弃以免替换选中文本；结果标明落地路径 | URL 逐字节 EXACT；纯中文+emoji 在 URL 框与 composer 均 EXACT |
| 3 | 字段未聚焦时 Cmd+V 被静默丢弃，工具却报 "Typed" | 无焦点校验 | 粘贴前轮询 accessibility 树等待 `IsEditing`，否则明确报错 "no text field has keyboard focus" | 单测 + 真机 |
| 4 | `/share stop` 之后同一会话里 agent 再调 `start_share` 永远被拒 "already active" | TUI 关闭路径 `detachTunnelLifecycle` 只清 Model 侧并 `DetachOnlineBroker`，从不清理 `TunnelHost.activeShare`（`StopShare()` 生产代码零调用） | 新增 `TunnelHost.ReleaseShare()`（标记结束 + 释放 P2P manager，不停 session/broker），关闭路径调用它；`stop_share` 在 Model 无 session 时兜底 `StopShare()` | 新二进制真实 TUI：`/share` → `/share stop` → agent `start_share` 返回 "Share session started." |
| 5 | "Clear all cached data" 之后重启仍停在"缓存历史会话"死胡同（回不到 Connect，弹层也没有 Clear 按钮） | `clearAll()` 清了 SQLite 和内存 state，没把 `_selectionDirty` 置位，SharedPreferences 里的 `selected_session_id` 下次启动被恢复 | `clearAll()` 置 `_selectionDirty = true` 并调度持久化 | 新增 `workspace_cache_clear_all_test.dart`；修复版 app 真机：清缓存 + 重启落在 Connect 页 |

顺带发现、未修（记录）：连接页输入非法 URL 后 "Connecting…" 不超时也不报错；底部弹层的拖动把手带 "Dismiss" 语义但点按无效（需点 Scrim）。→ 两项均在 §9 修复。

## 9. 追加：进程重启到 `65bc2446f` 后，经真实工具分发层复核并修复（2026-11-28 12:07–12:50）

`restart` 加载新二进制后（进程 txt inode 与磁盘 inode 一致；`snapshot` 返回 `Driver: idb`），本会话的 `mobile_device` 即 idb-first 版本，以下全部经本会话真实工具调用完成。共享模拟器（iPhone 17 Pro 等）正被其他 agent 的截图流水线 `shutdown`/`delete`（idb 报 `dtuhidd … invalidated`），因此新建专用设备 "GGCode-Verify"（iPhone 17 Pro / iOS 26.5）。

| # | 发现 | 根因 | 修复 | 实测证据 |
|---|---|---|---|---|
| 6 | 连接页进度视图是死胡同：握手挂起或主机永不发 `session_info` 时只有标题、副标题，没有任何控件 | `_buildConnectingView` 没有返回/取消控件；`relaySync` 停滞 30s 只改标题、不放弃 | 进度视图增加 **Cancel**（调 `disconnect()`）；relay 同步两阶段看门狗：30s 无进展标记 stalled，再 30s 仍无进展则 `_abandonConnect` 回连接页并报错；握手 45s 兜底超时（service 层已有 30s） | 真机：输入黑洞地址 `wss://10.255.255.1/...` → 进度视图出现 Cancel（截图内联于会话）→ 点击回到 URL 表单；重试后 30s 超时报错 "Connection failed: TimeoutException…"，Cancel 仍在；`widget_test`（Cancel 返回表单）+ `session_provider_test`（handshake timeout、stall 两阶段） |
| 7 | Cancel 后立即重试被静默吞掉（app 日志无 `_connectImpl`） | `disconnect()` 不清 `_connectInFlight`，同 URL 重试被合并进已取消、仍挂起的握手 future | `disconnect()`/`_abandonConnect()` 清 `_connectInFlight(_Url)` | 真机日志：Cancel 后重试出现 `_connectImpl gen=3`；新增单测 `retrying right after Cancel starts a fresh connect attempt` |
| 8 | 工作区弹层把手带 "Dismiss" 语义但点按无效 | `showDragHandle: true` 只是装饰，无点击、无语义；AX 树里的 "Dismiss" 其实是 barrier | 新增 `SheetDismissHandle`（44pt 命中区、`InkWell` + `Semantics(button, label=modalBarrierDismissLabel)`）替换内置把手 | 真机：打开弹层 → AX 树 `"Dismiss" rect={16,545,370,44}` → `tap @e2` 弹层关闭；单测 3 条（`sheet_dismiss_handle_test.dart`） |
| 9 | Agent 运行中，输入框占位文案承诺"仍可发送"，但只有 Stop 没有 Send | `isRunning ? Stop : Send` | 有草稿时 Stop 旁显示 Send；空草稿仍只显示 Stop | 真机截图 `/tmp/gg-probe/74-chat-user-bubble.png`（多行草稿 + Stop/Send 并列）；`widget_test` `InputBar offers Send next to Stop while busy once drafted` |

同时验证通过（真实会话投影）：`type` 中英混排 + emoji 逐字节一致（中文拼音输入法开启）；`tap(ref)`、`swipe`（以 iOS Settings 做对照实验，确认方向语义标准）、`screenshot`、`launch`、`list_apps`；用户气泡右对齐、accent 渐变 + `onAccent` 文字、多行保留（`/tmp/gg-probe/75-chat-user-bubble-full.png`）；重装后自动恢复会话。

未修、新发现（记录）：(a) **运行中从移动端发送的消息显示两条**（`/tmp/gg-probe/75-chat-user-bubble-full.png`）：本地乐观气泡被 host 回显 rename 后，另一条同文本事件（不带 `message_id`）走 `addRemoteUserMessage` 追加；疑似 host 对运行中消息同时记录"客户端消息"与"guidance 注入"两条 `user_message` 事件，或 `seedHistoryDirect` 不带 `message_id`；空闲态发送是否复现未验证，需双侧事件级追踪后再修。(b) 握手阶段文案显示 "Syncing with relay / Waiting for authoritative session data"（实际尚未连上 relay）。(c) 连接失败横幅是原始异常文本（`WebSocketException: … HTTP status code: 400`），应映射为可读文案。(d) `mobile_device logs` 在 iOS 返回设备系统日志（backboardd 刷屏），应按 app 进程过滤。(e) 实时刷新的界面上 `@eN` 引用会漂移（一次 `tap @e16` 落到了 "Attach image"），点按前应重新 `snapshot`。(f) 共享 Mac 上其他 agent 会 `shutdown`/`delete` 模拟器，验证需专用设备。

## 6. 附：证据与截图

- 桌面截图（注入 Wails shim 渲染的真实前端，1440×900）：`/tmp/gg-probe/01-chat-dark.png`（暗色主页：欢迎页 emoji 建议卡、顶部/底部重复状态芯片、10px 状态栏）、`/tmp/gg-probe/02-sidebar-dark.png`（侧栏空状态）、`/tmp/gg-probe/03-settings-dark.png`（设置表单：原生 select、右侧 60% 空白、拥挤的 Context Window 行）。
- 移动端：iOS 模拟器（iPhone 17 Pro，Midnight 默认主题）首屏 `/tmp/gg-probe/10-mobile-connect.png`。目检：排版干净、标题层级清晰；但 (1) "Scan QR Code"（主路径）与 "Connect"（提交）外观几乎一样，都是深色填充 + 细描边，主次不分；(2) URL 为空时 "Connect" 仍为可用态；(3) 内容全部压在上半屏，底部 40% 空白，没有"最近连接 / 工作原理 / 帮助"等次级入口；(4) 占位符 `wss://host/ws?token=xxx` 对普通用户过于技术化。
- 量化数据来源：ripgrep，统计口径见各表"证据"列。
