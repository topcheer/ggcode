# r23: Knight Skill Content Safety Scan（skill poisoning 确定性防线）

日期: 2026-10-09 | 轮次: r23 | 状态: 已实施

## 研究背景

- 前沿对标: agent memory/skill poisoning 防御（r409 预取来源: arXiv 2601.05504 MINJA、arXiv 2605.15338 Sleeper、OWASP Agentic ASI06；本仓设计文档 knight-design.md P2 明确提出借鉴 Hermes skills_guard.py，一直未实现）
- r22 backlog 复核: 项1（场景日志→回归 gate）EXISTS（scheduler.go:1054 AB replay 阻断门已闭环）；项2（knight→AGENTS.md promote）判定为设计上有意未做，不实施

## Gap 论证

skill 文件与 memory 同属"持久化→每会话 inline 进 prompt"的注入面。r409 已为 save_memory 写/读路径建立确定性防线；skill 路径平行存在且无内容级防线:

| 面 | 现状 | 证据 |
|---|---|---|
| 写侧 | 仅结构校验+快照 | skill_promoter.go Promote/WriteStaging |
| auto-promote gate | "no destructive/credentials" 仅 LLM prompt 约束（fail-open） | scheduler.go evaluateAutoPromoteCandidate |
| 内容 sanitizer | scenario_sanitize.go 只扫场景日志，不扫 skill 正文 | scenario_sanitize.go |

## 实施

- `internal/knight/skill_safety_scan.go`: 确定性扫描器。7 个高精度危险模式（rm-rf-root、curl/wget-pipe-shell、base64-pipe-shell、eval-base64、chmod-777-root、git-push-force[排除 --force-with-lease]、sensitive-file-exfil）+ credential 检测复用同包 sanitizationRules（单一真源；已 redact 占位符不误报）。findings 只报规则名+行号，绝不回显匹配内容（命中本身可能是凭据）
- 挂载: evaluateAutoPromoteCandidate（readSkillContent 后、任何 eval 成本前，记 `failure_mode:"safety_scan"` 降级人工审查）+ promoteStagingEntry（ValidateSkill 后，手动 promote 同样拦截）
- 测试: zz_issue_r23_safety_scan_test.go——11 危险样例逐规则命中、7 干净/安全变体放行（含 --force-with-lease、scoped rm、redacted 占位符）、摘要不回显凭据、毒 skill promote 被拒、干净 skill 放行

## 设计取舍

- 高精度优于高召回: 误报只降级人工审查（notify 路径），漏报才放毒 skill 出门
- 降级而非删除: 命中→require review，不自动 reject（保留人工裁决）

## 验证

- 定向: 5 测试组全过（0.554s）
- vet: OK
- 全包: `go test -tags goolm ./internal/knight/` ok（11.6s）

## 与相近机制差异化

- r409（memory poisoning）: save_memory 面，正交；credential 模式集包内共享
- overlap.go: 冗余维度确定性检查，本扫描是安全维度的对应物
- trust_level/auto_policy: 权限治理，不扫内容
