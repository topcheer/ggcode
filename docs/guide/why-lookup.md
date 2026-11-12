# why 查询（#5261）

`why` agent tool 回答"这段代码为什么存在"：git blame 只能给出提交与作者，原始请求与修改动机此前需人工翻转录。

## 用法

- 输入：文件路径（可带 `:line`，如 `internal/auth/store.go:57`）
- 解析链：锚点 → checkpoint EditIntent（`TasksWithEdits`/`EditsForTask`）→ **任务 ID + 任务描述 + 最新编辑动机**（按编辑数取 top3）
- 优雅降级：checkpoint 已清理或未接线时，回落 session 转录检索（既有 sessionidx/sessionidxd 管道，daemon-first、workspace 隔离），呈现 user 消息摘录 = 原始请求；无索引进程内给清晰文案、不报错
- 全本地，无上传

## 接线

- 生产路径：`Agent.SetCheckpointManager` → `Registry.SetCheckpoints`（镜像 `SetSessionIndex` 范式），`why` 调用时懒取
- 注册：builtin tools，与 `session_recall` 并列（见 `internal/tool/why.go`）
