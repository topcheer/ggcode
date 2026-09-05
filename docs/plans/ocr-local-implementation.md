# 本地 OCR 落地计划与设计

日期：2026-10 | 前置调研：`internal/image/OCR_PLATFORM_RESEARCH.md` | 状态：**已搁置（2026-10）**——决策：不做内置实现。现代模型可自行挖掘平台能力并用临时脚本/程序调用系统 OCR（如 PowerShell 调 WinRT、shortcuts/osascript），比固定集成更灵活。本设计保留作为未来重启时的基线，勿按此文档自动开工。

## 1. 目标与非目标

**目标**
- `read_file` 读图片时，若本地有可用 OCR 引擎，识别文本并附在结果文本通道中（base64 视觉通道保持不变），让无视觉能力的模型也能读到图中文字，同时降低视觉 token 成本。
- 跨平台引擎抽象：Linux/macOS/Windows 各自最优路径，运行时探测、自动回退。

**非目标（本轮不做）**
- 扫描版 PDF 的 OCR（需页面栅格化，依赖 poppler/pdftoppm，列为 Phase 5 可选）。
- 移动端（ggcode-mobile ML Kit）——独立工程，另行立项。
- 截图工具的实时 OCR（desktop_control / screenshot 后续可复用本包，但不在接线范围）。

## 2. 架构设计

### 2.1 新包 `internal/ocr`

不放进 `internal/image`（那是采集/解码职责）也不放进 `internal/extract`（那是文档文本层），OCR 是独立能力，参照 `extract.Registry` 的成熟模式建新包：

```
internal/ocr/
  ocr.go               // Engine 接口、Result、Registry、回退链、公共入口 OCR()
  ocr_test.go
  lang.go              // 语言探测/请求语言规范化（zh-Hans 等）
  tesseract.go         // 全平台：tesseract 子进程引擎（探测式，零 cgo）
  tesseract_test.go    // 缺二进制时 t.Skip
  ocr_darwin.go        // !cgo 回退（Available()=false），保证 CGO_ENABLED=0 构建
  vision_darwin.go     // +build darwin,cgo —— Vision 框架实现
  vision_darwin_test.go// 带 //go:build darwin,cgo，CI 跳过
  ocr_windows.go       // PowerShell 5.1 子进程调 WinRT Windows.Media.Ocr
  ocr_windows_test.go
  script_windows.ps1   // 嵌入的 PS 脚本（go:embed）
  ocr_unsupported.go   // 其余平台（linux 由 tesseract 覆盖，此文件兜底）
```

**关键构建约束**（来自 GGCODE.md）：CI 跑 `CGO_ENABLED=0 go build -tags goolm ./...`。因此 darwin 的 Vision 实现必须用 `//go:build darwin && cgo` 标签，并配一个 `//go:build darwin && !cgo` 的空实现——与 `internal/image` 的 clipboard/screenshot 平台拆分模式一致。

### 2.2 核心接口

```go
// Engine 是一个平台 OCR 引擎的抽象。
type Engine interface {
    // Name 返回引擎标识："vision" | "tesseract" | "winrt" | "none"
    Name() string
    // Available 惰性探测引擎可用性（缓存结果）。必须廉价、可重入。
    Available() bool
    // Recognize 对解码后的图片执行 OCR。实现必须尊重 ctx 超时。
    Recognize(ctx context.Context, img image.Image, opts Options) (Result, error)
}

type Options struct {
    Languages []string      // 请求语言，BCP-47 小写；空 = 引擎默认（含中文优先）
    Timeout   time.Duration // 默认 30s
}

type Result struct {
    Text       string   // 全文（引擎已排序）
    Lines      []string // 行级结果（供未来布局敏感场景）
    Language   string   // 实际使用的语言
    EngineName string
}

// OCR 是包级入口：按注册顺序取第一个 Available 的引擎。
// 无可用引擎时返回 ErrNoEngine（sentinel），调用方回退。
func OCR(ctx context.Context, img image.Image, opts Options) (Result, error)
```

设计决策：
- 入参用 `image.Image`（已解码），由调用方（read_file 已有 `image.Decode` 产物）传入，避免重复解码；Windows 引擎内部按 `MaxImageDimension` 运行时值自行缩放再编码，**不硬编码 4096**。
- `ErrNoEngine` sentinel + `Available()` 分离，回退判定零歧义。
- 注册顺序即优先级：darwin=cgo 时 `vision → tesseract`；windows=`winrt → tesseract`；其他=`tesseract`。

### 2.3 各引擎实现要点

**tesseract.go（全平台，Phase 1）**
- `Available()`：`exec.LookPath("tesseract")` + `tesseract --list-langs` 缓存探测；检查 `chi_sim`/`chi_tra` 是否在语言列表。
- 调用：`tesseract stdin stdout -l <langs> --psm 3`，stdin 传 PNG 字节（无临时文件、无 shell 注入面）。
- 语言缺失时降级为 eng 并在 Result 中如实标注，不静默谎报。

**vision_darwin.go（Phase 2）**
- cgo + Objective-C：`VNRecognizeTextRequest`，`recognitionLevel=accurate`，`recognitionLanguages` 探测顺序：`supportedRecognitionLanguages()` 运行时探测（**不按 OS 版本硬编码**），zh-Hans/zh-Hant 存在时置于前两位（Apple 官方要求）。
- 结果取 `topCandidates(1)`，拼接 `boundingBox` 排序后的文本。

**ocr_windows.go（Phase 3）**
- `Available()`：探测 `powershell.exe`（Windows PowerShell 5.1；**PowerShell 7 (pwsh) 无法加载 WinRT，明确排除**，见调研 [7]）。
- 嵌入 PS 脚本（`go:embed`）通过 `powershell -NoProfile -ExecutionPolicy Bypass -File` 执行：脚本内 `[Windows.Media.Ocr.OcrEngine,Windows.Foundation,ContentType=WindowsRuntime]` 加载类型、`AsTask` 等待（PS 单线程不能直接 await）、读图（System.Drawing 或 WPF BitmapDecoder）→ `RecognizeAsync` → stdout 输出 JSON（含行文本）。
- 图片经临时 PNG 文件传递（PS 读 stdin 二进制不可靠）；临时文件用 `os.CreateTemp` + defer 删除。
- 语言包缺失：脚本输出结构化错误码，Go 侧转成带安装指引的错误信息（"设置 → 时间和语言 → 语言 → 添加中文 → OCR 组件"）。
- 注意 package identity 风险（调研 [1]）：PS 子进程路径在社区（PsOcr/PowerToys 用法）已验证可行，但若微软未来收紧需准备 Windows App SDK 新 API（调研 [6]）作为替代实现，接口不变。

### 2.4 read_file 接线（Phase 4）

`internal/tool/read_file.go` 图片分支（现 120-133 行）扩展为：

```
1. image.Decode（不变）
2. base64 视觉通道（不变，Result.Images）
3. 新增：若 ocr 引擎可用（全局配置开关开启）：
   r, err := ocr.OCR(ctx, img, defaultOpts)   // 30s 超时
   - 成功且有文本 → Content = 占位符 + "\n\n--- OCR text (<engine>) ---\n" + r.Text
   - ErrNoEngine → Content = 原占位符（现状）
   - 其他错误 → 占位符 + 一行 "[OCR unavailable: <err>]"（不 fail，图片本身仍可用）
4. OCR 文本超长截断：与 maxOutputLines 同预算，尾部加 more hint
```

关键原则：**OCR 是增强，不是前置条件**——任何 OCR 失败都不能让 read_file 图片读取失败（现状是唯一兜底）。

### 2.5 配置（Phase 4）

`ggcode.yaml` 新增：

```yaml
ocr:
  enabled: true        # false 时完全跳过（含探测）
  languages: []        # 空 = 自动（中文优先的默认序）
  timeout: 30s
  engine: ""           # 空 = 自动回退链；可强制 "tesseract"/"vision"/"winrt"
```

默认 `enabled: true`——探测本身廉价（LookPath + 缓存），无引擎时行为与现状完全一致。

## 3. 分阶段任务（每阶段一个 PR，可独立合并）

| 阶段 | 内容 | 验证标准 | 风险 |
|---|---|---|---|
| **PR-1** | `internal/ocr` 包骨架：接口、Registry、`ErrNoEngine`、`none` 引擎、单元测试 | `go build -tags goolm ./...` + `CGO_ENABLED=0` 交叉构建通过；单测绿 | 低 |
| **PR-2** | tesseract 引擎 + read_file **不接线**（仅包内可用） | 测试在无 tesseract 环境自动 skip；本机有 tesseract+chi_sim 时跑全量断言 | 低 |
| **PR-3** | darwin Vision 引擎（cgo/!cgo 双文件） | 本机 `go test -tags goolm ./internal/ocr/` 实测中英混合图；CI 跳过 cgo 路径 | 中：cgo 工具链、recognizeLanguages 排序 |
| **PR-4** | Windows WinRT-PowerShell 引擎 | Windows 真机验证走 GitHub Actions（既有流程，见记忆 windows-real-machine-verify-via-actions）；脚本单元测试用 mock powershell | 高：package identity、语言包、5.1 缺失 |
| **PR-5** | read_file 接线 + 配置项 + TUI/文档 | e2e：读含中文文字的 PNG，断言 Content 含 OCR 文本且 Images 不变；`make verify-ci` | 低 |
| **PR-6（可选）** | 扫描 PDF 兜底（pdftoppm 栅格化 → 逐页 OCR）、screenshot/desktop_control 复用 | 另行设计评审 | 高，暂缓 |

依赖关系：PR-1 → {PR-2, PR-3, PR-4} 可并行 → PR-5。

## 4. 测试策略

- **确定性 fixture**：测试用 Go 标准库 `image` + `golang.org/x/image/font/basicfont` 在测试内生成含已知文本的小 PNG（英文可确定性绘制；中文用固定二进制 fixture 一张，存 `testdata/`，<10KB）。断言识别结果包含关键子串而非全等。
- **环境自适应 skip**：`exec.LookPath("tesseract")` 失败 → `t.Skip`；darwin cgo 测试带构建标签自动只在具备条件时编译运行。
- **Windows**：PS 脚本逻辑单测（mock powershell 路径）+ Actions 真机 job。
- **read_file 接线**：mock ocr.Engine（包级注入点 `ocr.SetRegistryForTest`），断言三种路径：成功附文本 / ErrNoEngine 原样 / 引擎错误不阻断。

## 5. 安全与合规

- 子进程全部无 shell（直接 argv），PS 用 `-NoProfile -ExecutionPolicy Bypass`（仓库既有约定）。
- 无临时文件残留（defer 删除）；无网络请求（全部本地引擎）。
- 不引入新的 Go 依赖（tesseract/PS/Vision 均为系统资源；PNG 编码用标准库）。

## 6. 回退矩阵（最终行为保证）

| 环境 | 行为 |
|---|---|
| 任意平台 + 无任何引擎 | 与现状 100% 一致（base64 给视觉模型） |
| macOS 13+ (cgo) | Vision 中文 OCR + 视觉双通道 |
| Linux + tesseract | tesseract OCR + 视觉双通道 |
| Windows + PS5.1 + 中文语言包 | WinRT OCR + 视觉双通道 |
| 任何引擎报错 | 降级为现状，附一行提示 |
