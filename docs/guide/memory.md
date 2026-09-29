
## 用户偏好自动沉淀（r266）

会话中用户明确表达的持久偏好（如 "from now on always run tests with -p=1"、
"以后都不要直接改 pubspec.yaml"）会在 run 结束时被确定性蒸馏（无 LLM 调用，
高置信双语标记词匹配）并存入项目记忆 `user-preferences` 条目：
- 每次 run 最多捕获 2 条，句界扫描不误切文件名（pubspec.yaml）
- 与既有条目去重合并，总量上限 30 条（最旧的先淘汰）
- 存储为普通项目记忆，可随时用 delete_memory 删除
