# 平台服务器（Platform Server）

> 自托管的多用户后台 agent API：提交任务 → headless 运行 → 轮询状态 → 取结果。
> 用户代码始终留在运行 `ggcode serve` 的那台机器上，不上传任何云端。

## 概念

对标 2026 年主流"后台/远程 agent"形态（Cursor Cloud Agents、Codex cloud、
GitHub Copilot coding agent）：一个 JWT 鉴权的 HTTP 控制面，把 headless
agent 运行绑定到本机白名单 workspace 目录。与它们的关键差异：**无云端 VM、
无仓库克隆——执行环境就是你的机器**。

## 快速开始

```bash
# 1. 注册用户（密码 >= 8 位；交互式输入或 --password-stdin）
ggcode platform user add alice --admin
ggcode platform user add bob --password-stdin < pw.txt

# 2. 白名单 workspace（必须是绝对路径、已存在的目录）
ggcode platform workspace add /Users/me/projects/webapp
ggcode platform workspace list

# 3. 启动服务器（默认 127.0.0.1:8420）
ggcode serve
```

## API

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/api/v1/auth/login` | `{username,password}` → `{token}`（HS256 JWT，24h） |
| POST | `/api/v1/jobs` | `{workspace,prompt}` → 201 job；workspace 必须在白名单内 |
| GET | `/api/v1/jobs` | 列出自己的任务（admin 看全部；不含 output） |
| GET | `/api/v1/jobs/{id}` | 任务详情（含 output）；非本人且非 admin 返回 404（不可枚举） |
| DELETE | `/api/v1/jobs/{id}` | 取消排队/运行中的任务 |
| GET | `/api/v1/healthz` | 存活探针（无需鉴权） |

```bash
TOKEN=$(curl -s localhost:8420/api/v1/auth/login -d '{"username":"alice","password":"..."}' | jq -r .token)
curl -s localhost:8420/api/v1/jobs -H "Authorization: Bearer $TOKEN" \
  -d '{"workspace":"/Users/me/projects/webapp","prompt":"修复登录页的空指针"}'
curl -s localhost:8420/api/v1/jobs/<id> -H "Authorization: Bearer $TOKEN"
```

## 行为与安全模型

- **执行**：任务在服务器本机以子进程方式运行 `ggcode --prompt <prompt>`，
  工作目录 = 任务 workspace；同一时刻最多 1 个任务在跑，其余排队；
  output 上限 256 KiB（保留尾部）。
- **workspace 防逃逸**：提交路径必须是白名单根的子目录（两侧均做
  symlink 解析后用 filepath.Rel 判定）；相对路径/不存在/非目录一律拒绝。
- **鉴权**：PBKDF2-HMAC-SHA256（100k 迭代）口令散列；登录失败固定 250ms
  延迟；签名密钥为首启自动生成的 32 字节随机值（`secret.key`，0600）。
  所有 token 绑定用户名，用户删除后旧 token 立即失效。
- **权限语义**：普通用户只能看到/取消自己的任务；`--admin` 用户可看全部。
- **bypass**：默认任务无 `--bypass`（受限权限运行）；在
  `~/.ggcode/platform/config.json` 写 `{"bypass":true}` 可放开——仅在
  完全自用、机器可信时启用。

## 文件布局

```
~/.ggcode/platform/
  users.json        # 用户注册表（PBKDF2 散列）
  workspaces.json   # workspace 白名单
  secret.key        # JWT 签名密钥（0600）
  config.json       # 可选：{listen, bypass}
  jobs/job-*.json   # 任务持久化（重启后仍可查询）
```

## 实现

`internal/platform/`（jwt/users/workspace/jobs/executor/server），
CLI 入口 `cmd/ggcode/serve_cmd.go`、`cmd/ggcode/platform_cmd.go`。
