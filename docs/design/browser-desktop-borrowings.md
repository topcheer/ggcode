# Browser / Desktop Control 借鉴设计（源自 ZCode browser use / computer use 研究）

> 状态：**待 review**（四个决策点见 §6，均有建议裁决，review 时逐点确认或翻案）
> 来源：../ZCode 源码研究（2026-11-07），证据级=源码行号实读
> 影响面：`internal/tool/browser.go`（1493 行）、`internal/tool/desktop_control*.go`

## 0. 研究结论摘要（五借鉴点，均经源码核实）

| # | ZCode 机制（证据锚） | 我方现状 | 处置 |
|---|---|---|---|
| 1 | aria snapshot 分层预算（browserPlaywrightDomSnapshot.ts L8-9/L14/L164/L365-390） | snapshot 无独立预算 | **§1 采纳** |
| 2 | visible-fallback strict 选择器（browserPlaywrightLocatorExecutor.ts STRICT_VISIBLE_SELECTOR_HELPER） | click/hover 裸 querySelector 文档序首个（L694/L1250） | **§2 采纳** |
| 3 | 输入前焦点 token 校验（browserVirtualClipboard.ts L245 assertFocusedInputTarget + LocatorExecutor L476 调用） | focus 与注入间无校验 | **§3 采纳（轻量化）** |
| 4 | 结构化错误码（zcode-cua/broker.d.ts L19-24：notAuthorized/foregroundRequired/elementUnavailable…） | errResult 纯字符串（browser.go 64 处 IsError） | **§4 采纳** |
| 5 | CUA helper 进程分离+probeHelperHealth 健康探针（broker.d.ts L46） | desktop_control 进程内直调 | **暂缓**（架构级，崩溃域隔离收益需重自动化负载证明） |

我方已有优势不抄：@eN ref 系+导航失效指引；Go 原生 CDP 零 Node 依赖；browser_leak 跨平台泄漏防护专测。

---

## 1. snapshot 分层预算 + 取消传播

**现状锚**：snapshot 动作（browser.go L329）无独立遍历预算；wait_timeout（L135）是"等元素"语义非遍历预算。深层 SPA iframe 树遍历可拖满 CDP 默认超时。

**设计**：
```
doSnapshot 三层 ctx 派生：
  顶层预算   = min(wait_timeout 缺省 3s, 8s 硬帽)      // 对齐 ZCode TOP_LEVEL_TIMEOUT_MS=3000
  iframe 总预算 = 1s（共享 deadline）                   // IAB_IFRAME_TOTAL_BUDGET_MS=1000
  单子 frame  = 500ms                                   // IAB_IFRAME_CHILD_BUDGET_MS=500
遍历器每进 frame 检查 ctx.Err()；超时帧标记 "[timeout: budget exhausted]"
——不 fail 整个 snapshot，输出已收集树 + 截断标记（部分结果优于全弃，Go ctx 原生同构 ZCode AbortSignal）
```

**边界**：@eN ref 只对完整收集子树发放；超时帧内元素无 ref（防半树 ref 点错靶）。

## 2. visible-fallback 选择器解析（resolveTarget 统一入口）

**现状锚**：五处 `document.querySelector` 裸调用（L694/L912/L1200/L1250/L1349），文档序首个匹配，隐藏元素命中即静默错靶；响应式页双 DOM 常态。

**设计**（单次注入 JS 往返）：
```
matches = querySelectorAll(selector)
  0 个   → 报错（现状）
  1 个   → 直接用
  >1 个  → 过滤 visible（offsetParent 非空 + rect 面积>0 + visibility!=hidden）
    可见恰 1 → 用它
    可见 >1 或 0 → 报错附 top-5 候选（标签+文本 30 字+index+visible 位）
```
作用面：click/type/hover/select/extract/wait 七动作收编；@eN ref 路径不走（ref 已定位，快路径保留）；evaluate 不限（用户显式 JS 自由度不收）。

## 3. type 前焦点 token 校验（轻量防漂移）

**现状锚**：doType（L218）focus+sendKey 间无校验；JS 重渲染/弹窗抢焦点时文本进错框（密码框/搜索框错注=安全面）。

**设计**（省略 ZCode 虚拟剪贴板，只取防漂移核）：
```
1. resolveTarget(§2) 得 e
2. e.dataset.ggTk = 随机 8 字节（一次性 token）
3. e.focus() → evaluate 读回 document.activeElement.dataset.ggTk
4. 读回 != 注入 → 报错 "focus hijacked ... active element is <tag#id>, refusing to type into wrong target"
5. 校验过 → 现有逐字符注入 → finally 清 token
```
成本：+1 次 CDP evaluate（~5ms），仅 type 动作付。iframe 内目标带 frameId 读回（复用 snapshot frame 上下文链）。press 全局键不校验（无靶概念）。

## 4. 结构化错误码（errcode 前缀协议）

**现状锚**：errResult（L351）纯字符串 64 处；desktop 同平面。agent 无法编程分支（foregroundRequired 应重试 vs notAuthorized 不应）。

**设计**：**Result 契约零破坏**，Content 头插前缀：
```
[browser:errcode=element_not_found] <人读消息>
[desktop:errcode=foreground_required] <人读消息>
```
码表（两工具共用核心+扩展）：
```
browser:  element_not_found / element_ambiguous{candidates} / focus_hijacked /
          nav_timeout / budget_exhausted / target_closed / profile_locked / cdp_unreachable
desktop:  foreground_required / not_authorized / app_not_found / action_unsupported{platform} / timeout
可恢复类（提示 agent 可重试）: *_timeout / cdp_unreachable / transient
```
配 `errCoded(tool, code, fmt, ...)` 收编调用点（机械替换分批）；前缀正则 `^\[(\w+):errcode=(\w+)\]`；TUI 渲染层剥前缀显人读部（+2 行）。

---

## 5. 实现切片（估 ~700 行，依赖序 D1→D2→D3/D4 并行）

| # | 片 | 面 | 估行 | 验收测 |
|---|---|---|---|---|
| D1 | resolveTarget 统一入口 | §2：注入 JS+visible 过滤+候选清单；五调用点收编 | ~180 | 多匹配响应式页命中唯一可见；歧义报错含 top-5 候选；@eN 快路径不回归 |
| D2 | snapshot 分层预算 | §1：三层 ctx+部分返回+截断标记；ref 只发完整子树 | ~150 | 慢 iframe 页顶层 3s 部分返回；超时帧无 ref；快页无回归 |
| D3 | type 焦点 token | §3：token 注入/读回/清除；iframe frameId 链 | ~120 | 焦点被抢场景拒打+报错文案含 active element；正常 type 无回归；token 无残留 |
| D4 | errcode 前缀协议 | §4：码表+errCoded+两工具收编（分批）+TUI 剥前缀 | ~250 | 正则可解析全码面；TUI 显示无前缀；既有测试字符串断言适配 |

## 6. 决策点（review 时逐点裁定）

| # | 议题 | 建议 | 反案 |
|---|---|---|---|
| ① | snapshot 超时帧：标记+部分返回 vs 整体失败 | **部分返回**（长页至少拿可交互区） | 整体失败语义更简单，agent 不误信半树 |
| ② | 多匹配可见>1：报错+候选 vs 自动选面积最大 | **报错+候选**（叠层 modal 易选错背景层） | 自动选减少一轮交互，但静默错靶风险 |
| ③ | 焦点被抢：直接报错 vs 重 focus 重试 1 次 | **直接报错**（明确信号归 agent 处理弹窗） | 重试 1 次对瞬时弹窗有效，但对抗重渲染双输 |
| ④ | errcode 范围：本两工具 vs 全仓规范 | **本两工具+文档留扩展位**（本切片闭环） | 全仓治理动作另立项 |

## 7. 明确不做（本轮）

- ZCode #5（helper 进程分离）：架构级，崩溃域隔离收益需重自动化负载证明，暂缓
- 我方优势项不动：@eN ref 系/Go 原生 CDP/leak 专测
