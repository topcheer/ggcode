# 跨平台兼容性审查报告

**日期**: 2025-05-20  
**范围**: TUI (CLI) + Desktop GUI，Linux 和 Windows 兼容性  
**审查团队**: 4 个并行 reviewer（platform-code, desktop-gui, build-ci, tui-compat）

---

## 总览

| 维度 | Critical | High | Medium | Low | 总计 |
|------|----------|------|--------|-----|------|
| 平台代码 | 1 | 5 | 6 | 4 | 16 |
| Desktop GUI | 0 | 3 | 7 | 9 | 19 |
| CI/CD 构建 | 0 | 2 | 3 | 1 | 6 |
| TUI 终端 | 1 | 2 | 3 | 4 | 10 |
| **合计** | **2** | **12** | **19** | **18** | **51** |

---

## Critical（必须修复）

### C1: `os.Symlink` 在 Windows 上无条件使用
- **文件**: `internal/debug/debug.go`, `internal/harness/worktree.go`
- **影响**: Windows 非 Developer Mode 下直接报错
- **修复**: 加 `_windows.go` 构建标签，Windows 上用 `os.Link` 或 copy 替代

### C2: `syscall.Exec` 在 TUI 重启路径会崩溃
- **文件**: `internal/tui/repl.go`（restart 函数）
- **影响**: Windows 不支持 `syscall.Exec`（进程替换），TUI `/restart` 命令会 panic
- **修复**: Windows 上改用 `os.StartProcess` + `os.Exit`

---

## High（应当修复）

### H1: Unix-only 临时目录检测
- **文件**: `internal/knight/analyzer.go`（`isTempDir()` 只检查 `/tmp`, `/var/folders`）
- **影响**: Windows 临时文件无法识别
- **修复**: 加入 `os.TempDir()`, `C:\Temp`, `%TEMP%`, `%TMP%` 路径

### H2: `homeDir()` 实现分散且不一致
- **文件**: `internal/auth/` vs `internal/config/` 各有自己的 `homeDir()`，对 `HOME`/`USERPROFILE`/`APPDATA` 处理不同
- **影响**: Windows 上 auth token 缓存路径可能错误
- **修复**: 统一使用 `os.UserHomeDir()`

### H3: `syscall.Signal(0)` 无平台抽象
- **文件**: `internal/config/instance_detect.go`
- **影响**: Windows 上 `syscall.Signal(0)` 行为不同
- **修复**: 加 `_windows.go` 构建标签

### H4: `sh -c` 硬编码
- **文件**: `internal/tui/signal_panel.go`
- **影响**: Windows 没有 `sh`
- **修复**: Windows 上改用 `cmd /c` 或 `powershell -Command`

### H5: 进程树 kill 差异
- **文件**: `internal/tool/run_command_unix.go` vs `run_command_other.go`
- **状态**: 已有 `taskkill /T /F` fallback，但 smoke test 只覆盖 `--help`
- **建议**: 扩展 Windows smoke test 覆盖子进程管理

### H6: Linux Fyne CGO 依赖未文档化
- **文件**: Desktop 构建文档
- **影响**: 用户构建 desktop 需要安装 `libgl1-mesa-dev`, `libx11-dev` 等 8 个开发包
- **修复**: 在 README 或 docs 中列出完整依赖

### H7: Windows Desktop 剪贴板图片粘贴不可靠
- **文件**: `desktop/ggcode-desktop/chat_view.go:245-258`
- **影响**: PowerShell 首次启动慢 2-5 秒，精简 Windows 可能没有 PowerShell
- **修复**: 改用 CGO + Windows native API (`user32.dll`)

### H8: Windows CGO 编译工具链难配置
- **文件**: `desktop/ggcode-desktop/titlebar_windows.go`（`#cgo LDFLAGS: -lgdi32 -luser32 -ldwmapi`）
- **影响**: 需要 MinGW-w64 或 MSYS2
- **修复**: 文档化 Windows 构建步骤

### H9: 缺少 Windows 构建和打包脚本
- **文件**: `scripts/release/`（只有 `build-desktop-darwin.sh`）
- **影响**: Windows desktop 无法自动化构建
- **修复**: 创建 `build-desktop-windows.ps1`

### H10: TUI 缺少 Windows TTY 看门狗
- **文件**: `internal/tui/` 
- **影响**: Windows 管道断开时 TUI 可能 hang
- **修复**: 添加 Windows 专用 TTY 检测

### H11: TUI Shell 模式 Unix 语义
- **文件**: `internal/tui/`（shell 模式使用 `bash -c`）
- **影响**: Windows 上 shell 模式不可用
- **修复**: Windows 上 fallback 到 `cmd /c` 或 PowerShell

### H12: `signal_panel.sh` 使用 Unix shell 脚本
- **文件**: `desktop/ggcode-desktop/signal_panel.go`
- **影响**: Windows desktop 无法执行 signal panel
- **修复**: Windows 上用 Go 原生实现替代 shell 脚本

---

## Medium（建议修复）

### 路径问题
- `internal/debug/debug.go`: 硬编码 `/tmp/` 路径
- `internal/config/config.go`: `/root` fallback（应为空或使用 `os.UserHomeDir()`）
- `internal/config/config.go`: `HOME` vs `USERPROFILE` 不一致

### Desktop GUI
- **X-1**: `AllWindows()[0]` 无索引检查，可能 panic
- **X-2**: 缺少 Linux `.desktop` 文件和 Windows `.manifest`/`.rc` 资源文件
- **X-3**: Fyne 文件对话框在 Windows 上返回 URI 编码路径（`/C:/Users/...`）
- **X-7**: CJK 字体依赖系统字体，Linux 需安装 `fonts-noto-cjk`
- **X-8**: 缺少 `FyneApp.toml` 配置文件
- **L-1**: Wayland 下 `xclip`/`wl-paste` 剪贴板依赖不可靠
- **L-4**: Fyne/OpenGL 在 Wayland 环境可能需要环境变量

### TUI
- **W-3**: 旧版 cmd.exe 不支持 ANSI 转义序列（需启用 VTP）
- **W-4**: Braille spinner 字符在 Windows 终端可能显示异常
- **L-2**: TUI 剪贴板缺少 `xsel` fallback（daemon 有但 TUI 没有）

### Windows 文件权限
- `os.Chmod` 在 Windows 上只影响只读属性，0644/0755 等权限位被静默忽略

### CI/CD
- GoReleaser 配置已覆盖 linux/windows/amd64/arm64，但 desktop 构建脚本缺失

---

## Low（可选改进）

- `strings.Title` deprecated（改用 `cases.Title`）
- Unicode 圆角边框在 Windows 终端的 fallback
- Emoji（📨 ⚠️ ✅ ❌）在旧终端显示为方块
- 多实例临时文件名冲突（用 `os.CreateTemp` 替代固定文件名）
- 快捷键文档应显示 "Ctrl+B / Cmd+B" 格式
- Linux 通知依赖 `libnotify-dev`
- `.ico` 图标文件缺失（Windows 高 DPI 模糊）
- Desktop 临时文件名多实例冲突风险
- Fyne `KeyModifierShortcutDefault` 文档建议

---

## 构建依赖汇总

### Linux Desktop 构建
```bash
# Debian/Ubuntu
apt install gcc libgl1-mesa-dev libx11-dev libxcursor-dev \
    libxrandr-dev libxinerama-dev libxi-dev libxxf86vm-dev \
    libgles2-dev xclip wl-clipboard fonts-noto-cjk

# Fedora/RHEL
dnf install gcc mesa-libGL-devel libX11-devel libXcursor-devel \
    libXrandr-devel libXinerama-devel libXi-devel libXxf86vm-devel \
    mesa-libGLES-devel xclip wl-clipboard google-noto-sans-cjk-fonts
```

### Windows Desktop 构建
```
MSYS2 + MinGW-w64 (gcc)
或 Visual Studio Build Tools + clang
```

---

## 优先修复建议

1. **立即修复** (Critical):
   - `os.Symlink` → Windows fallback
   - `syscall.Exec` → Windows 用 `StartProcess`

2. **短期修复** (High, 本周内):
   - 统一 `homeDir()` 实现
   - `sh -c` → 平台适配
   - Windows 构建脚本
   - Desktop 构建依赖文档

3. **中期改进** (Medium, 下个版本):
   - Linux `.desktop` 文件
   - Windows `.manifest` + `.rc` 资源
   - `FyneApp.toml` 配置
   - CJK 字体 fallback
   - TUI 剪贴板 xsel fallback

4. **长期优化** (Low, 持续改进):
   - Unicode/Emoji fallback
   - Wayland 文档
   - `strings.Title` 替换
   - 多实例临时文件安全
