# r77: Evidence-Grounding Gate（证据接地门禁）

## 研究来源

arXiv:2605.08828 *When Agents Overtrust Environmental Evidence: An Extensible
Agentic Framework for Benchmarking Evidence-Grounding Defects in LLM Agents*
（2026-05）。该文定义 **EGD（Evidence-Grounding Defect）**：agent 将一条环境观
察（文件内容、命令输出、网页）当作行动的充分证据，而未与当前真实环境状态对
齐，导致在真实状态下走入错误路径。其系统分解为：context admission、evidence
provenance、**freshness checking**、**verification policy**、**action gating**、
model reasoning。

## ggcode 的既有覆盖与缺口

既有机制（`internal/tool/file_integrity.go`，#881/#1358/#1408/#2318 系列演进）：

- `FileIntegrityTracker` 以 mtime 为基线做 stale 检测；
- `run_command` 执行后经 `detectChangedFilesFromCommand` 通知哪些已读文件被
  外部命令（gofmt / sed -i / git checkout / 生成器）修改；
- `edit_file`/`multi_edit` 仅在 old_text 匹配**失败**时附加 stale 提示；
  `write_file`/`multi_file_write`/`notebook_edit` 有 CheckStale 硬门禁。

缺口（正是 EGD 的 "overtrust" 失效模式在本 harness 的真实存在）：

1. **`ChangedSince` 抬升基线**：命令外部修改检测到后，`modtimes[path]` 被
   更新为外部 mtime。此后 agent 的“知识边界”被抹掉——后续编辑基于过期内存
   内容时既不会被拦截，连匹配失败路径上的 `staleReadHint` 也被抑制。
2. **`edit_file`/`multi_edit` 成功路径零门禁**：old_text 仍匹配（外部修改只
   碰了别的区域）时静默落盘。

即：本可检测的过期编辑被 harness 自己转化成静默编辑。

## 实现

- `FileIntegrityTracker` 新增 `dirty map[string]time.Time`：
  - `ChangedSince` 检测到外部修改时同时置 dirty（保留基线 bump 以维持
    CheckStale 记账一致性）；
  - `RecordRead`（重新观察）与 `RecordWrite`（agent 自身的全量权威写入）
    清除 dirty；`RemoveTracking`/`Reset` 同步清理。
- 新增 `externalModGate(path)`：dirty 时返回阻断性错误，指引
  `read_file` 后重试（action gating，Claude Code "File has been modified
  since read" 同型）。
- 接线 5 个写入工具：`edit_file`、`multi_edit_file`、`write_file`、
  `multi_file_write`、`notebook_edit`。
- **不接线 `batch_replace`**：其替换基于本次调用内部的即时全量读取，行动时
  证据是新鲜的，不属于 stale-evidence 问题；强行门禁只会制造误报。

## 权衡

- 命令修改已读文件后，agent 若继续编辑将收到一次明确阻断，`read_file` 即解
  锁。这与 Claude Code 行为一致，代价是每文件一次重读。
- dirty 为进程级状态，与既有 tracker 同生命周期；跨进程外部修改（用户 IDE
  编辑）仍由 CheckStale 匹配失败提示兜底，不在本轮范围。

## 测试

`internal/tool/zz_r77_grounding_gate_test.go`：dirty 生命周期（置位/双清
除/RemoveTracking/Reset）、`edit_file`/`multi_edit`/`write_file` 的
“外部修改 → 阻断且文件未动 → 重读 → 成功”全链路。
