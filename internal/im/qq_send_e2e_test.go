//go:build integration_service

package im

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

func TestE2EQQSend10Scenarios(t *testing.T) {
	if os.Getenv("GGCODE_E2E") == "" {
		t.Skip("GGCODE_E2E not set, skipping")
	}

	cfg := loadE2EConfig(t)
	if cfg == nil {
		t.Skip("config not found")
	}

	adapterName, adapterCfg, found := findQQAdapterConfig(t, cfg)
	if !found {
		t.Skip("no enabled QQ adapter")
	}

	adapter, err := newQQAdapter(adapterName, cfg.IM, adapterCfg, nil)
	if err != nil {
		t.Fatalf("create adapter: %v", err)
	}

	ctx := context.Background()
	if _, tokenErr := adapter.ensureToken(ctx); tokenErr != nil {
		t.Fatalf("ensureToken: %v", tokenErr)
	}
	t.Logf("Token OK (adapter=%s)", adapterName)

	channelID := os.Getenv("GGCODE_E2E_QQ_CHANNEL")
	if channelID == "" {
		t.Skip("GGCODE_E2E_QQ_CHANNEL not set (set to a QQ channel ID to send test messages to)")
	}

	chatType := os.Getenv("GGCODE_E2E_QQ_CHAT_TYPE")
	if chatType == "" {
		chatType = "c2c"
	}
	adapter.rememberChatType(channelID, chatType)

	adapter.mu.Lock()
	adapter.connected = true
	adapter.ws = dummyWSConn(t)
	adapter.mu.Unlock()

	pngBase64 := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8/5+hHgAHggJ/PchI7wAAAABJRU5ErkJggg=="

	scenarios := []struct {
		name    string
		content string
	}{
		{
			name:    "01_plain_text",
			content: "E2E 测试 #1：纯文本消息发送测试",
		},
		{
			name:    "02_multiline_text",
			content: "E2E 测试 #2：多行文本\n第二行内容\n第三行内容\n完成。",
		},
		{
			name:    "03_markdown_image",
			content: fmt.Sprintf("E2E 测试 #3：Markdown 图片\n\n![test](data:image/png;base64,%s)\n\n图片已发送。", pngBase64),
		},
		{
			name:    "04_text_with_code_block",
			content: "E2E 测试 #4：包含代码块\n\n```go\nfunc main() {\n    fmt.Println(\"hello\")\n}\n```\n\n代码结束。",
		},
		{
			name:    "05_chinese_special_chars",
			content: "E2E 测试 #5：中文特殊字符 ~!@#$%^&*()_+-={}[]|\\:\";'<>?,./",
		},
		{
			name:    "06_markdown_image_plus_text",
			content: fmt.Sprintf("E2E 测试 #6：图片+文字\n分析结果如下：\n\n![chart](data:image/png;base64,%s)\n\n以上是分析图表，请查看。", pngBase64),
		},
		{
			name:    "07_long_text",
			content: "E2E 测试 #7：长文本消息。这是一段较长的测试消息，用于验证 QQ 消息发送在较长内容时的稳定性。" + strings.Repeat(" 重复内容。", 20),
		},
		{
			name:    "08_emoji_and_unicode",
			content: "E2E 测试 #8：Emoji 和 Unicode ✅🎉🚀💡 你好世界 こんにちは 안녕하세요",
		},
		{
			name:    "09_multiple_markdown_images",
			content: fmt.Sprintf("E2E 测试 #9：多张图片\n\n![img1](data:image/png;base64,%s)\n\n![img2](data:image/png;base64,%s)\n\n两张图片发送完毕。", pngBase64, pngBase64),
		},
		{
			name:    "10_mixed_content",
			content: fmt.Sprintf("E2E 测试 #10：混合内容\n\n1. 纯文本段落\n2. 代码: `x = 42`\n3. 图片:\n\n![final](data:image/png;base64,%s)\n\n✅ 所有场景测试完成！", pngBase64),
		},
	}

	passed := 0
	failed := 0
	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			sendCtx, cancel := context.WithCancel(ctx)
			defer cancel()

			binding := ChannelBinding{
				ChannelID: channelID,
				Adapter:   adapterName,
			}

			sendErr := adapter.Send(sendCtx, binding, OutboundEvent{
				Kind: OutboundEventText,
				Text: sc.content,
			})
			if sendErr != nil {
				t.Errorf("Send failed: %v", sendErr)
				failed++
				return
			}
			passed++
			t.Logf("Sent OK (%d bytes)", len(sc.content))

			time.Sleep(2 * time.Second)
		})
	}

	t.Logf("\n=== Results: %d/%d passed, %d failed ===", passed, len(scenarios), failed)
	if failed > 0 {
		t.Errorf("%d scenarios failed", failed)
	}
}

func TestE2EQQImageUploadOnly(t *testing.T) {
	if os.Getenv("GGCODE_E2E") == "" {
		t.Skip("GGCODE_E2E not set, skipping")
	}

	cfg := loadE2EConfig(t)
	if cfg == nil {
		t.Skip("config not found")
	}

	adapterName, adapterCfg, found := findQQAdapterConfig(t, cfg)
	if !found {
		t.Skip("no enabled QQ adapter")
	}

	adapter, err := newQQAdapter(adapterName, cfg.IM, adapterCfg, nil)
	if err != nil {
		t.Fatalf("create adapter: %v", err)
	}

	channelID := os.Getenv("GGCODE_E2E_QQ_CHANNEL")
	if channelID == "" {
		t.Skip("GGCODE_E2E_QQ_CHANNEL not set")
	}

	chatType := os.Getenv("GGCODE_E2E_QQ_CHAT_TYPE")
	if chatType == "" {
		chatType = "c2c"
	}

	ctx := context.Background()

	pngData := createTestPNG(t)
	b64 := base64.StdEncoding.EncodeToString(pngData)

	fileInfo, uploadErr := adapter.uploadMedia(ctx, chatType, channelID, b64)
	if uploadErr != nil {
		t.Fatalf("uploadMedia: %v", uploadErr)
	}
	if fileInfo == "" {
		t.Fatal("uploadMedia returned empty file_info")
	}
	t.Logf("Image uploaded successfully: file_info=%s", truncateStr(fileInfo, 60))

	fileInfo2, cacheErr := adapter.uploadMedia(ctx, chatType, channelID, b64)
	if cacheErr != nil {
		t.Fatalf("uploadMedia (cached): %v", cacheErr)
	}
	if fileInfo2 != fileInfo {
		t.Errorf("cache miss: got %q, want %q", fileInfo2, fileInfo)
	}
	t.Log("Upload cache hit verified")
}

func createTestPNG(t *testing.T) []byte {
	t.Helper()

	b64 := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8/5+hHgAHggJ/PchI7wAAAABJRU5ErkJggg=="
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("decode test PNG: %v", err)
	}
	return data
}

func TestE2ERealLLMToQQCallRealLLM(t *testing.T) {
	if os.Getenv("GGCODE_E2E") == "" {
		t.Skip("GGCODE_E2E not set, skipping")
	}

	cfg := loadE2EConfig(t)
	if cfg == nil {
		t.Skip("config not found")
	}

	prov := resolveE2EProvider(t, cfg)
	if prov == nil {
		t.Skip("no valid provider in config, skipping")
	}

	adapterName, adapterCfg, found := findQQAdapterConfig(t, cfg)
	if !found {
		t.Skip("no enabled QQ adapter")
	}

	if envAdapter := os.Getenv("GGCODE_E2E_QQ_ADAPTER"); envAdapter != "" {
		if a, ok := cfg.IM.Adapters[envAdapter]; ok && a.Enabled && a.Platform == "qq" {
			adapterName = envAdapter
			adapterCfg = a
		} else {
			t.Fatalf("adapter %q not found or not enabled", envAdapter)
		}
	}

	adapter, err := newQQAdapter(adapterName, cfg.IM, adapterCfg, nil)
	if err != nil {
		t.Fatalf("create adapter: %v", err)
	}

	ctx := context.Background()
	if _, tokenErr := adapter.ensureToken(ctx); tokenErr != nil {
		t.Fatalf("ensureToken: %v", tokenErr)
	}
	t.Logf("Token OK (adapter=%s)", adapterName)

	channelID := os.Getenv("GGCODE_E2E_QQ_CHANNEL")
	if channelID == "" {
		t.Skip("GGCODE_E2E_QQ_CHANNEL not set (set to a QQ channel ID to send test messages to)")
	}

	chatType := os.Getenv("GGCODE_E2E_QQ_CHAT_TYPE")
	if chatType == "" {
		chatType = "c2c"
	}
	adapter.rememberChatType(channelID, chatType)

	adapter.mu.Lock()
	adapter.connected = true
	adapter.ws = dummyWSConn(t)
	adapter.mu.Unlock()

	resolved, resolveErr := cfg.ResolveActiveEndpoint()
	if resolveErr != nil {
		t.Fatalf("resolve endpoint: %v", resolveErr)
	}

	prompts := []struct {
		name   string
		prompt string
	}{
		{
			name:   "01_bar_chart",
			prompt: "用HTML+JavaScript画一个柱状图展示以下销售数据：Q1=120, Q2=180, Q3=150, Q4=200。保存为 chart.html，完成后用open命令打开并截图保存为 chart_screenshot.png，在回复中附上截图。",
		},
		{
			name:   "02_pie_chart",
			prompt: "用HTML+JavaScript+Canvas画一个简单的饼图展示以下数据：技术30%, 产品25%, 设计20%, 运营15%, 其他10%。保存为 pie.html，完成后用open命令打开并截图保存为 pie_screenshot.png，在回复中附上截图。",
		},
		{
			name:   "03_counter",
			prompt: "做一个简单的网页计数器（counter.html），有一个数字显示和加减按钮。完成后用open命令打开并截图保存为 counter_screenshot.png，在回复中附上截图。",
		},
		{
			name:   "04_color_blocks",
			prompt: "做一个简单的网页，展示6个不同颜色的方块（红橙黄绿蓝紫），排列成两行三列。保存为 colors.html，完成后用open命令打开并截图保存为 colors_screenshot.png，在回复中附上截图。",
		},
		{
			name:   "05_line_chart",
			prompt: "用HTML+JavaScript+Canvas画一个折线图展示月度趋势：1月=50, 2月=65, 3月=80, 4月=72, 5月=90。保存为 line.html，完成后用open命令打开并截图保存为 line_screenshot.png，在回复中附上截图。",
		},
		{
			name:   "06_bar_and_line",
			prompt: "分别做两个简单的图表页面：\n1. 柱状图展示：A=30, B=50, C=40。保存为 bar.html，用open打开并截图保存为 bar_screenshot.png\n2. 折线图展示：1月=10, 2月=30, 3月=20。保存为 line2.html，用open打开并截图保存为 line2_screenshot.png\n\n在回复中同时附上两张截图。",
		},
		{
			name:   "07_two_buttons",
			prompt: "做两个简单的按钮网页：\n1. 红色按钮页面，保存为 red.html，用open打开并截图保存为 red_screenshot.png\n2. 蓝色按钮页面，保存为 blue.html，用open打开并截图保存为 blue_screenshot.png\n\n每个页面只有一个居中的大按钮。在回复中同时附上两张截图。",
		},
	}

	passed := 0
	failed := 0
	totalImages := 0
	for _, p := range prompts {
		t.Run(p.name, func(t *testing.T) {

			projectDir := t.TempDir()
			t.Logf("Project dir: %s", projectDir)

			registry := tool.NewRegistry()
			if regErr := tool.RegisterBuiltinTools(registry, nil, projectDir, nil, nil); regErr != nil {
				t.Fatalf("register builtin tools: %v", regErr)
			}
			maxIter := 15
			if strings.HasPrefix(p.name, "06_") || strings.HasPrefix(p.name, "07_") {
				maxIter = 20
			}
			ag := agent.NewAgent(prov, registry, "You are a helpful coding assistant. Respond in Chinese (Simplified). When creating files, use write_file tool. After creating HTML files, use run_command to open them with the 'open' command on macOS and take a screenshot with 'screencapture'. Always include the screenshot file path in your response using markdown image syntax like ![screenshot](/path/to/screenshot.png).", maxIter)
			ag.SetWorkingDir(projectDir)
			ag.SetSupportsVision(resolved.SupportsVision)

			timeout := 150 * time.Second
			if strings.HasPrefix(p.name, "06_") || strings.HasPrefix(p.name, "07_") {
				timeout = 200 * time.Second
			}
			llmCtx, llmCancel := context.WithTimeout(ctx, timeout)
			defer llmCancel()

			var responseText strings.Builder
			streamErr := ag.RunStream(llmCtx, p.prompt, func(event provider.StreamEvent) {
				if event.Type == provider.StreamEventText {
					responseText.WriteString(event.Text)
				}
			})
			if streamErr != nil {
				t.Errorf("LLM RunStream error: %v", streamErr)
				failed++
				return
			}

			output := strings.TrimSpace(responseText.String())
			if output == "" {
				t.Error("LLM returned empty response")
				failed++
				return
			}
			t.Logf("LLM response (%d chars): %s", len(output), truncateStr(output, 500))

			images, remainingText := ExtractImagesFromText(output)
			t.Logf("Extracted %d image(s) from response", len(images))
			for i, img := range images {
				t.Logf("  image[%d]: kind=%s data_len=%d preview=%s", i, img.Kind, len(img.Data), truncateStr(img.Data, 100))
			}

			sendCtx, sendCancel := context.WithCancel(ctx)
			defer sendCancel()

			binding := ChannelBinding{
				ChannelID: channelID,
				Adapter:   adapterName,
			}

			sendErr := adapter.Send(sendCtx, binding, OutboundEvent{
				Kind: OutboundEventText,
				Text: output,
			})
			if sendErr != nil {
				t.Errorf("QQ Send failed: %v", sendErr)
				failed++
				return
			}
			passed++
			totalImages += len(images)

			remaining := strings.TrimSpace(remainingText)
			if len(remaining) > 100 {
				remaining = remaining[:100] + "..."
			}
			t.Logf("Sent to QQ OK (%d chars text, %d images, remaining: %q)", len(output), len(images), remaining)

			time.Sleep(5 * time.Second)
		})
	}

	t.Logf("\n=== Real LLM→QQ E2E Results: %d/%d passed, %d failed, %d total images ===", passed, len(prompts), failed, totalImages)
	if failed > 0 {
		t.Errorf("%d scenarios failed", failed)
	}
}

func pngChunk(chunkType []byte, data []byte) []byte {
	length := make([]byte, 4)
	length[0] = byte(len(data) >> 24)
	length[1] = byte(len(data) >> 16)
	length[2] = byte(len(data) >> 8)
	length[3] = byte(len(data))

	crcInput := make([]byte, len(chunkType)+len(data))
	copy(crcInput, chunkType)
	copy(crcInput[len(chunkType):], data)
	crc := crc32Checksum(crcInput)
	crcBytes := make([]byte, 4)
	crcBytes[0] = byte(crc >> 24)
	crcBytes[1] = byte(crc >> 16)
	crcBytes[2] = byte(crc >> 8)
	crcBytes[3] = byte(crc)

	result := make([]byte, 0, 4+4+len(data)+4)
	result = append(result, length...)
	result = append(result, chunkType...)
	result = append(result, data...)
	result = append(result, crcBytes...)
	return result
}

func crc32Checksum(data []byte) uint32 {

	crc := uint32(0xFFFFFFFF)
	for _, b := range data {
		crc ^= uint32(b)
		for i := 0; i < 8; i++ {
			if crc&1 != 0 {
				crc = (crc >> 1) ^ 0xEDB88320
			} else {
				crc >>= 1
			}
		}
	}
	return crc ^ 0xFFFFFFFF
}

func zlibCompress(data []byte) []byte {

	var result []byte
	result = append(result, 0x78, 0x01)

	offset := 0
	for offset < len(data) {
		blockLen := len(data) - offset
		final := byte(1)
		if blockLen > 65535 {
			blockLen = 65535
			final = 0
		}
		result = append(result, final)
		result = append(result, byte(blockLen), byte(blockLen>>8))
		result = append(result, ^byte(blockLen), ^byte(blockLen>>8))
		result = append(result, data[offset:offset+blockLen]...)
		offset += blockLen
	}

	s1, s2 := uint32(1), uint32(0)
	for _, b := range data {
		s1 = (s1 + uint32(b)) % 65521
		s2 = (s2 + s1) % 65521
	}
	adler := (s2 << 16) | s1
	result = append(result, byte(adler>>24), byte(adler>>16), byte(adler>>8), byte(adler))

	return result
}
