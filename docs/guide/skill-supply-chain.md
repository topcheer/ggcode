# Skill 供应链安全（provenance lock）

## 威胁面

克隆含恶意 `.ggcode/skills/` 的仓库 = 静默注入系统提示级指令。2026 研究实证该攻击面已成体系：SkillPoison（arXiv:2610.07645，95.71% ASR 且过词法审查）、CoordPoison（arXiv:2609.39352，孤立单 skill 审计是盲区）、SkillBloat（arXiv:2608.21929，token 放大 5.4-10.1x）、BadSkill（arXiv:2604.09378，明确呼吁来源验证）。**内容扫描不可充分防御——来源/完整性锁定是正交必要层**。

## 机制

### 锁定（pin）

```sh
ggcode skill lock          # 遍历 project 面 skill/command，sha256 写入 .ggcode/skills-lock.json
```

- 锁只管 **project 面**（`<project>/.ggcode/skills`、`.agents/skills`、`commands/`）——克隆注入防御面；user 面（`~/.ggcode`、`~/.agents`）是操作者自有资产，永不锁定
- 锁文件含 name/path/source/sha256/lockedAt，0600 权限

### 加载门控

- **锁定文件 hash 不匹配** → 拒载该 skill（debug 日志含期望/实际 hash 前 8 位）——改动后需重新 pin
- **无锁文件** → 默认正常加载（warn-once 提示 `ggcode skill lock`）
- **strict 模式**（`GGCODE_SKILL_LOCK_STRICT=1`）→ 未入锁的 project skill 一律拒载（新克隆仓库/新增 skill 须显式 pin 才生效）

### 与既有防线正交

| 层 | 机制 | 边界 |
|---|---|---|
| skill 内容扫描 | `skill_safety_scan`（词法/静态） | SkillPoison 证明可绕过 |
| 自改防护 | `self_mod_guard`（skills-lock.json 列为防自改路径） | 锁文件自身不被 agent 篡改 |
| **来源完整性**（本节） | hash 锁定+strict 门控 | 防 repo 注入+静默替换 |
| 二进制供应链 | `install.verifyArchive` sha256 | 只覆盖 release 包 |

## 验证

`internal/commands/zz_skilllock_test.go`：篡改拒载（改字后 hash 失配拒载+未改正常载）/strict 未入锁拒载（新 skill 拒+已 pin 照常）。全量 `-tags goolm`。
