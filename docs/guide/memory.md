
## 用户偏好自动沉淀（r266）

会话中用户明确表达的持久偏好（如 "from now on always run tests with -p=1"、
"以后都不要直接改 pubspec.yaml"）会在 run 结束时被确定性蒸馏（无 LLM 调用，
高置信双语标记词匹配）并存入项目记忆 `user-preferences` 条目：
- 每次 run 最多捕获 2 条，句界扫描不误切文件名（pubspec.yaml）
- 与既有条目去重合并，总量上限 30 条（最旧的先淘汰）
- 存储为普通项目记忆，可随时用 delete_memory 删除

## 按需符号表（#5446 P3）

系统提示词不再内联 Go/TS/Py 的包导出清单——每语言只注入包数/符号数与最大包
Top-8。需要某个包的完整导出时用 `symbols` 工具按需查询：

- `{"query":"agent"}`：按包路径或符号名子串匹配（大小写不敏感），跨 go/ts/py
- `{"language":"go","query":"Server"}`：限定语言精确到符号名
- 空查询 = 最大包概览；`limit` 控制返回包数（默认 12，上限 40）
- Go 符号的**位置**（行/列）仍走 `lsp_workspace_symbols`/`lsp_definition`

提取器与旧内联版完全同源（同一套跳过 test/spec/vendor/unexported 的规则），
仅注入方式从"常驻"改为"按需"。
