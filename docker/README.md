# ggcode 容器镜像（分层 tag 矩阵 + 企业制品仓库 + 定制 CA）

自带常用开发工具链的 ggcode 容器，**所有可调点均环境变量参数化**。传入模型配置即可运行。

## 1. Tag 矩阵（按语种选镜像）

| Tag | 内容 | 适用 |
|---|---|---|
| `base-<v>` | 核心工具（git/jq/rg/fd/ssh/build-essential/vim…）+ ggcode | 纯 AI 协作/脚本 |
| `python-<v>` | base + Python3/pip/venv | Python 项目 |
| `node-<v>` | base + Node22/pnpm/yarn/tsc/tsx | JS/TS 项目 |
| `go-<v>` | base + 官方 Go 工具链 | Go 项目 |
| `java-<v>` | base + OpenJDK21/Maven/Gradle | Java 项目 |
| `dotnet-<v>` | base + .NET SDK 9 | C#/.NET 项目 |
| `web-<v>` | base + Ruby/PHP | Ruby/PHP 项目 |
| `full-<v>` | 以上全部 | 不确定/混合项目 |

## 2. 规格承诺（per spec）

- 默认用户 `ggcode`（**uid/gid 1024**），**无密码 sudo**
- `WORKDIR` = `HOME` = `/ggcode`
- 默认命令 `--plain`；`daemon` 模式透传
- tini 信号处理；配置/工作区/CA 全挂载

## 3. 构建（全部参数化）

```bash
make docker-build                              # 全矩阵
make docker-build-python DOCKER_TAG=v1         # 单 tag
# 企业内部参数（构建期）：
make docker-build-full \
  APT_MIRROR=https://mirrors.internal.com/ubuntu \
  GO_VERSION=1.26.10 GOARCH=arm64 \
  DOCKER_REGISTRY=harbor.internal/devops DOCKER_TAG=prod
```

| 构建期变量 | 作用 | 默认 |
|---|---|---|
| `APT_MIRROR` | apt 源替换（ubuntu.sources sed） | 官方源 |
| `GO_VERSION` | Go 工具链版本 | `1.26.10` |
| `GOARCH` | `amd64`/`arm64`（Go tarball 适配） | `amd64` |
| `DOCKER_REGISTRY`/`DOCKER_TAG` | 镜像命名 | `topcheer`/`dev` |
| `DOCKER_BUILD_ARGS` | 透传任意 `--build-arg` | 空 |

## 4. 运行期环境变量（企业制品仓库 + CA + 模型）

### 4.1 制品仓库镜像源（启动时 entrypoint 自动生效）

| 变量 | 覆盖 | 机制 |
|---|---|---|
| `PIP_INDEX_URL` | pip | 原生 env |
| `NPM_CONFIG_REGISTRY` | npm/pnpm/yarn | 原生 env |
| `GOPROXY` / `GOPRIVATE` | Go modules | 原生 env |
| `MAVEN_MIRROR_URL` | Maven | 生成 `~/.m2/settings.xml` |
| `GRADLE_REPO_URL` | Gradle | 生成 `~/.gradle/init.gradle` |
| `CARGO_REGISTRIES_CRATE_IO_INDEX` | cargo | 生成 `~/.cargo/config.toml` |
| `GEM_MIRROR_URL` | ruby gems | 生成 `~/.gemrc` |
| `DOTNET_NUGET_SOURCE` | nuget | 生成 `~/nuget.config` |

### 4.2 定制 CA（挂载即信任）

```bash
docker run --rm -it \
  -v ./corp-certs:/certs:ro \          # *.crt/*.pem/*.cer
  topcheer/ggcode:go-dev bash -c 'curl -I https://harbor.internal'
```
- 挂载目录内证书自动 `update-ca-certificates` 装入系统信任链（首次有变更时，fingerprint 幂等）
- 同时导出全栈 env：`SSL_CERT_FILE`（curl/go）、`REQUESTS_CA_BUNDLE`（py）、`NODE_EXTRA_CA_CERTS`（node）、`GIT_SSL_CAINFO`（git）
- 目录可用 `GGCODE_CA_DIR` 改（默认 `/certs`）

### 4.3 模型配置（极简启动）

| 变量 | 作用 |
|---|---|
| `GGCODE_ENDPOINT` | API 端点（首次运行落 `~/.ggcode/config.yaml`，600 权限） |
| `GGCODE_API_KEY` | 密钥 |
| `GGCODE_MODEL` | 模型名 |

## 5. 使用姿势

```bash
# ① 零文件极简启动（python 项目）
docker run --rm -it \
  -e GGCODE_ENDPOINT=... -e GGCODE_API_KEY=... \
  -e PIP_INDEX_URL=https://pypi.internal/simple \
  -v ggwork:/ggcode topcheer/ggcode:python-dev

# ② 挂宿主配置
docker run --rm -it -v ~/.ggcode:/ggcode/.ggcode -v $(pwd):/ggcode topcheer/ggcode:node-dev

# ③ daemon（IM 预配置）
docker run -d --name gg-daemon \
  -v ~/.ggcode:/ggcode/.ggcode -v $(pwd)/ws:/ggcode \
  --restart unless-stopped topcheer/ggcode:full-dev daemon

# ④ 任意透传
docker run --rm topcheer/ggcode:base-dev --version
```

## 6. 自检

```bash
make docker-smoke DOCKER_TAG=dev   # --version + uid=1024 + CA 挂载冒烟
```
