# 平台系统级 OCR 能力调研（选型依据）

日期：2026-10 | 状态：调研完成；**实现已搁置（2026-10，理由见 docs/plans/ocr-local-implementation.md 头部）**，本文档转为平台能力参考资料 | 范围：为 `read_file` 图片/扫描 PDF 的本地 OCR 增强提供选型依据

## 背景

当前 `read_file`（`internal/tool/read_file.go:120-133`）对图片的处理是：`image.Decode` 校验后整体 base64 交给多模态视觉模型，PDF 提取（`internal/extract/pdf.go`）只取文本层，扫描件无兜底。全仓无任何本地 OCR。本文档调研各平台系统级 OCR 的可用性与集成路径。

## 五平台对照表

| 平台 | 系统 API | 中文支持 | 离线 | 关键限制 | Go/Flutter 集成路径 |
|---|---|---|---|---|---|
| **macOS** | Vision `VNRecognizeTextRequest`（macOS 10.15+） | zh-Hans/zh-Hant 自 macOS 13 Ventura，韩文自 macOS 14 Sonoma [3][4]；Apple 官方要求中简/繁必须作为 `recognitionLanguages` 的**前两个元素** [5] | 是 | accurate 档较慢；cgo + Objective-C | darwin 构建标签下 cgo 调 Vision（与 `internal/image` 现有平台拆分一致）；亦有现成 CLI 工具可参考（macos-vision-ocr） |
| **Windows** | `Windows.Media.Ocr`（WinRT）；另有 Windows App SDK 1.7+ 新版 Text Recognition API [6] | 需用户安装对应语言包（`AvailableRecognizerLanguages` 可枚举），简繁中文均有 | 是 | ① 官方文档：该命名空间"仅支持有 package identity（MSIX）的桌面应用" [1]，裸 Go 二进制是主要障碍；② `MaxImageDimension` 为运行时可查询属性（UInt32），官方文档未给出常量值，社区普遍报告 4096px 边长 [2]，超限需自行缩图；③ PowerShell 7 无法加载 WinRT，仅 Windows PowerShell 5.1 可用 [7] | Go 无现成绑定：`saltosystems/winrt-go` 仓库已确认只含蓝牙/SMTC 相关 API，无 `windows/media/ocr` 包且从不打 tag [8]。可行路径：PowerShell 5.1 子进程（PsOcr 模块已验证该模式 [7]）或自生成 WinRT 绑定 |
| **Linux** | **无系统级 API** | 取决于 tesseract 语言包（`chi_sim`） | n/a | 无统一接口；依赖用户安装 | 运行时探测 `tesseract` 可执行文件，子进程调用（零 cgo）；扫描 PDF 可选 `ocrmypdf` |
| **iOS** | Vision（iOS 13+），Live Text（iOS 16+） | 同 macOS 版本规则（中文 iOS 16+） | 是 | — | ggcode-mobile 原生通道或 Flutter 插件 |
| **Android** | 非 AOSP 系统级；事实标准为 Google Play Services 的 **ML Kit Text Recognition v2** | 官方确认支持中文、日文、韩文、梵文、拉丁字符集 [9] | 模型下载后 on-device | 需 Play Services；非 AOSP 自带 | Flutter 插件 `google_mlkit_text_recognition`（v2 捆绑 CJK 模型） |

## 已验证来源

1. Windows.Media.Ocr 命名空间 package identity 要求：https://learn.microsoft.com/en-us/uwp/api/windows.media.ocr （"only supported for desktop apps with package identity"）
2. OcrEngine.MaxImageDimension 属性（运行时查询，官方未给常量值）：https://learn.microsoft.com/en-us/uwp/api/windows.media.ocr.ocrengine.maximagedimension
3. macOS 13 起支持简/繁中文与日文，macOS 14 增加韩文（Live Text/Vision）：https://kaedeeeeeeeeee.github.io/OCR_LLM/blog/ocr-asian-languages-on-mac/ 及 https://kaedeeeeeeeeee.github.io/OCR_LLM/blog/how-to-ocr-foreign-language-screenshot-on-mac/ （社区来源，与 Apple 文档一致性已交叉核对）
4. Apple 官方 zh-Hans/zh-Hant 需置于 recognitionLanguages 前两元素：https://developer.apple.com/documentation/vision/recognizing-text-in-images
5. VNRecognizeTextRequest API：https://developer.apple.com/documentation/vision/vnrecognizetextrequest
6. Windows App SDK Text Recognition（新 API）：https://learn.microsoft.com/en-us/windows/ai/apis/text-recognition
7. PsOcr（PowerShell 调 Windows OCR，5.1-only 说明）：https://github.com/TobiasPSP/PsOcr
8. winrt-go 仅含蓝牙 API、无 OCR 绑定、无 tag：https://pkg.go.dev/github.com/saltosystems/winrt-go （README 与子包目录已核实）
9. ML Kit Text Recognition v2 语言支持：https://developers.google.com/ml-kit/vision/text-recognition/v2

## 未决风险

- **Windows package identity**：即使走 PowerShell 子进程绕过 MSIX 要求，也依赖目标机器存在 Windows PowerShell 5.1（PowerShell 7 不行）；服务器版/精简系统可能缺失。
- **Windows 语言包依赖**：用户未装中文 OCR 语言包时 `TryCreateFromLanguage` 失败，需给出安装指引或回退。
- **MaxImageDimension 数值未官方确定**：实现时必须运行时读取该属性并对超限图片缩放，不可硬编码 4096。
- **macOS 版本矩阵来自社区**：实现时应以 `supportedRecognitionLanguages()` 运行时探测为准，而非按 OS 版本硬编码。
- **Linux**：tesseract/chi_sim 非默认安装，OCR 属尽力而为的增强路径。

## 落地建议（按优先级）

1. **Linux（最简）**：探测 `tesseract`，存在则对图片与无文本层 PDF 子进程 OCR；检测 `chi_sim`；不存在则回退现状。
2. **macOS（效果最好）**：cgo + Vision，darwin 构建标签下新增 `ocr_darwin.go`；语言用运行时 `supportedRecognitionLanguages()` 探测。
3. **Windows**：PowerShell 5.1 子进程调 WinRT OCR（零 cgo、可立即验证）；语言包缺失时输出安装指引；长期可评估 Windows App SDK 新 API 或自生成绑定。
4. **Android/iOS（ggcode-mobile）**：ML Kit v2 插件，中文开箱即用。
5. **兜底策略（所有平台）**：无本地 OCR 时保持现状——图片 base64 交给多模态视觉模型，PDF 仅取文本层。OCR 是降本增强路径，不是功能前置条件。
