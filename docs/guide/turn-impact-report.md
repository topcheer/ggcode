# Turn Impact Report（回合级爆炸半径报告）

每个 run 结束后，agent 会聚合本回合的写入面（edit/write 类工具触碰的文件、改变源码树的 shell 命令、保守判定的破坏性命令数），并在**下一个 run 开始时**作为只读观察注入上下文。

- **只提示不阻塞**：报告仅供参考（防止"环境已变、沿用旧结论"），绝不拦截任务执行
- 风险分级：`high`（存在破坏性 shell）/`medium`（有 shell 突变或 >5 文件）/`low`（其余）
- 文件去重，保留首个触碰工具名
- **worktree 路径归组**（#5524）：`.ggcode/worktrees/<name>/` 下的改动归入独立区段 `Files touched in worktree <name>`（点名该 worktree，标注"NOT the main tree"），绝不混入主树文件列表；相对路径录入时按会话工作目录锚定，worktree 会话的归属不丢失。纯主树改动的渲染格式不变
- 破坏性判定为保守关键词集（git push/reset/clean、rm -rf 等），误报可接受、漏报不可接受

设计依据：arXiv:2611.02815（预测式护栏诊断——预测注入过多反而降低任务完成率，故 inform-only）。
