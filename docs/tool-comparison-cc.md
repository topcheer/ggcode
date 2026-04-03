# ggcode vs cc (Claude Code) 工具对比分析

> 参考项目: https://github.com/topcheer/cc.git (Claude Code v2.1.88 source map)

## cc 项目中的工具清单

cc 是 Claude Code (Anthropic 官方 CLI) 的编译产物，包含以下内置工具:

### 核心文件操作
| 工具名 | 类型 | 说明 |
|--------|------|------|
| `Bash` | 内置 | Shell 命令执行（含沙箱/非沙箱模式） |
| `Read` / `FileReadTool` | 内置 | 读取文件内容 |
| `Write` / `FileWriteTool` | 内置 | 写入文件 |
| `Edit` / `FileEditTool` | 内置 | 编辑文件（str_replace_based_edit_tool） |
| `MultiEdit` | 内置 | 多处同时编辑 |
| `Glob` | 内置 | 文件模式匹配 |
| `Grep` | 内置 | 内容搜索（正则表达式） |
| `NotebookEdit` | 内置 | Jupyter Notebook 编辑 |

### 搜索与网络
| 工具名 | 类型 | 说明 |
|--------|------|------|
| `WebFetch` | 内置 | 抓取网页内容 |
| `WebSearch` | 内置 | 网络搜索（web_search_tool） |
| `LSP` | 内置 | Language Server Protocol 集成（代码导航/诊断） |

### Agent 编排
| 工具名 | 类型 | 说明 |
|--------|------|------|
| `Task` / `AgentOutputTool` | 内置 | 子任务/子代理执行与输出 |
| `TodoWrite` | 内置 | 待办事项管理 |

### 系统与集成
| 工具名 | 类型 | 说明 |
|--------|------|------|
| `BashOutputTool` | 内置 | Bash 输出流式处理 |
| `SystemTool` | 内置 | 系统级操作 |
| `Memory` | 内置 | 记忆存储 |

### MCP (Model Context Protocol)
| 工具名 | 类型 | 说明 |
|--------|------|------|
| `mcp_tool` | MCP | 调用 MCP 服务器工具 |
| `ListMcpResourcesTool` | MCP | 列出 MCP 资源 |
| `ReadMcpResourceTool` | MCP | 读取 MCP 资源 |

### 浏览器自动化 (MCP 扩展)
| 工具名 | 类型 | 说明 |
|--------|------|------|
| `Browser` / `Computer` | MCP扩展 | 浏览器/桌面自动化（点击、输入、截图、滚动） |
| `read_page` | MCP扩展 | 读取页面可访问性树 |
| `find` | MCP扩展 | 查找页面元素 |
| `navigate` | MCP扩展 | 页面导航 |
| `type` / `click` / `scroll` | MCP扩展 | 交互操作 |
| `screenshot` / `zoom` | MCP扩展 | 截图与缩放 |
| `javascript_tool` | MCP扩展 | 在页面中执行 JavaScript |
| `tabs_context_mcp` | MCP扩展 | 管理 MCP 浏览器标签页组 |
| `tabs_create_mcp` | MCP扩展 | 创建新标签页 |
| `console_mcp` | MCP扩展 | 读取浏览器控制台日志 |
| `network_mcp` | MCP扩展 | 读取网络请求 |
| `shortcuts_list` / `shortcuts_execute` | MCP扩展 | 执行快捷键/工作流 |
| `gif_record` / `gif_export` | MCP扩展 | GIF 录制与导出 |

### Bash 权限系统
cc 的 Bash 工具有细粒度权限控制:
- 按命令模式匹配（如 `git:*`, `npm:*`, `rm -rf:*`）
- 支持通配符、前缀匹配
- 沙箱/非沙箱双模式
- 自动拒绝危险命令（如访问 `/proc/*/environ`）

---

## ggcode 工具清单

### 核心文件操作
| 工具名 | 文件 | 状态 |
|--------|------|------|
| `read_file` | read_file.go | ✅ 已实现 |
| `write_file` | write_file.go | ✅ 已实现 |
| `edit_file` | edit_file.go | ✅ 已实现 |
| `list_dir` | list_dir.go | ✅ 已实现 |
| `search_files` | search_files.go | ✅ 已实现（内容搜索） |
| `glob` | glob.go | ✅ 已实现 |
| `run_command` | run_command.go | ✅ 已实现（Shell 执行） |

### Git 操作
| 工具名 | 文件 | 状态 |
|--------|------|------|
| `git_status` | git_status.go | ✅ 已实现 |
| `git_diff` | git_diff.go | ✅ 已实现 |
| `git_log` | git_log.go | ✅ 已实现 |

### Agent 编排
| 工具名 | 文件 | 状态 |
|--------|------|------|
| `spawn_agent` | spawn_agent.go | ✅ 已实现 |
| `wait_agent` | wait_agent.go | ✅ 已实现 |
| `list_agents` | list_agents.go | ✅ 已实现 |

### 其他
| 工具名 | 文件 | 状态 |
|--------|------|------|
| `save_memory` | save_memory.go | ✅ 已实现 |

### MCP 集成
| 功能 | 文件 | 状态 |
|------|------|------|
| MCP 客户端 | internal/mcp/ | ✅ 已实现 |
| MCP 工具调用 | mcp adapter | ✅ 已实现 |

---

## 对比总结

### ✅ ggcode 已覆盖的 cc 工具
| cc 工具 | ggcode 对应 | 备注 |
|---------|-------------|------|
| `Bash` | `run_command` | ggcode 有权限策略系统 |
| `Read` | `read_file` | 功能等价 |
| `Write` | `write_file` | 功能等价 |
| `Edit` | `edit_file` | ggcode 用 search/replace |
| `Glob` | `glob` | 功能等价 |
| `Grep` | `search_files` | 功能等价（内容搜索） |
| `Task`/子代理 | `spawn_agent` + `wait_agent` | ggcode 有完整子代理系统 |
| `Memory` | `save_memory` | 功能等价 |
| `mcp_tool` | MCP client | 已实现 |
| `ListMcpResourcesTool` | MCP client | 已实现 |
| `ReadMcpResourceTool` | MCP client | 已实现 |

### ❌ ggcode 缺失的 cc 工具
| cc 工具 | 优先级 | 说明 | 实现难度 |
|---------|--------|------|----------|
| `WebFetch` | 🔴 高 | 抓取网页内容，agent 经常需要 | 中等 |
| `WebSearch` | 🔴 高 | 网络搜索能力 | 中等 |
| `MultiEdit` | 🟡 中 | 单次调用多处编辑（edit_file 可多次调用替代） | 低 |
| `TodoWrite` | 🟡 中 | 待办管理，辅助 agent 规划 | 低 |
| `LSP` | 🟡 中 | 代码智能（诊断、跳转定义、引用查找） | 高 |
| `NotebookEdit` | 🟢 低 | Jupyter Notebook 支持 | 中等 |
| `BashOutputTool` | 🟢 低 | Bash 流式输出（ggcode 已有流式机制） | 低 |
| `Browser`/`Computer` | 🟢 低 | 桌面/浏览器自动化（cc 通过 MCP 扩展实现） | 高 |
| `javascript_tool` | 🟢 低 | 页面 JS 执行（cc 通过 MCP 扩展实现） | 高 |
| `GIF录制` | 🟢 低 | 浏览器操作录制（cc 通过 MCP 扩展实现） | 高 |

### 📊 覆盖率统计
- **cc 内置工具**: ~15 个（不含 MCP 扩展的浏览器工具）
- **ggcode 已实现**: 15 个本地工具 + MCP 集成
- **核心覆盖率**: ~80%（核心文件操作、执行、搜索、Git、子代理、MCP 均已覆盖）
- **主要缺口**: WebFetch、WebSearch、TodoWrite、LSP

### 🎯 建议优先实现
1. **WebFetch** — agent 获取外部信息的基础能力
2. **WebSearch** — agent 搜索信息的基础能力
3. **TodoWrite** — 辅助 agent 进行任务规划和追踪
4. **LSP** — 代码智能能力，提升代码编辑质量（复杂度高，可后续迭代）
